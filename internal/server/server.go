package server

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"mobileshell/internal/auth"
	"mobileshell/internal/executor"
	"mobileshell/internal/fileeditor"
	"mobileshell/internal/process"
	"mobileshell/internal/sysmon"
	"mobileshell/internal/terminal"
	"mobileshell/internal/workspace"
	"mobileshell/internal/wshub"
	"mobileshell/pkg/httperror"
	"mobileshell/pkg/markdown"
	"mobileshell/pkg/outputlog"
	"mobileshell/pkg/outputtype"

	"github.com/gorilla/websocket"
	"golang.org/x/net/html"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	stateDir  string
	tmpl      *template.Template
	wsHub     *wshub.Hub
	debugHTML bool
}

func New(stateDir string, debugHTML bool) (*Server, error) {
	funcMap := template.FuncMap{
		"formatDuration": formatDuration,
		"split": func(s, sep string) []string {
			return strings.Split(s, sep)
		},
		"divf": func(a int64, b float64) float64 {
			return float64(a) / b
		},
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.gohtml")
	if err != nil {
		return nil, err
	}

	s := &Server{
		stateDir:  stateDir,
		tmpl:      tmpl,
		wsHub:     wshub.NewHub(),
		debugHTML: debugHTML,
	}

	return s, nil
}

// formatDuration formats a duration in seconds to a human-readable string
// Returns empty string if duration is less than 1 second
func formatDuration(start, end time.Time) string {
	if end.IsZero() {
		return ""
	}
	duration := end.Sub(start)
	if duration < time.Second {
		return ""
	}

	// Format duration
	seconds := int(duration.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	remainingSeconds := seconds % 60
	if minutes < 60 {
		if remainingSeconds > 0 {
			return fmt.Sprintf("%dm %ds", minutes, remainingSeconds)
		}
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	remainingMinutes := minutes % 60
	if remainingMinutes > 0 {
		return fmt.Sprintf("%dh %dm", hours, remainingMinutes)
	}
	return fmt.Sprintf("%dh", hours)
}

func getFileExtensionFromContentType(contentType string) string {
	// Extract base type without parameters (e.g., "text/plain; charset=utf-8" -> "text/plain")
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = strings.TrimSpace(contentType[:idx])
	}

	// Use standard library to get extensions for this MIME type
	exts, err := mime.ExtensionsByType(contentType)
	if err == nil && len(exts) > 0 {
		// Return the first extension (most common)
		return exts[0]
	}

	// Default to .output for unknown types
	return ".output"
}

// handlerFunc is the new signature for all handlers
type handlerFunc func(context.Context, *http.Request) ([]byte, error)

// wrapHandler adapts a handlerFunc to http.HandlerFunc
func (s *Server) wrapHandler(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		data, err := h(ctx, r)
		if err != nil {
			// Check for special error types that need custom handling
			if re, ok := err.(*redirectError); ok {
				http.Redirect(w, r, re.url, re.statusCode)
				return
			}
			if cre, ok := err.(*cookieRedirectError); ok {
				if cre.cookie != nil {
					http.SetCookie(w, cre.cookie)
				}
				if cre.redirect != "" {
					http.Redirect(w, r, cre.redirect, cre.statusCode)
					return
				}
				return
			}
			if hxre, ok := err.(*hxRedirectError); ok {
				if hxre.cookie != nil {
					http.SetCookie(w, hxre.cookie)
				}
				w.Header().Set("HX-Redirect", hxre.url)
				w.WriteHeader(http.StatusOK)
				return
			}
			if cte, ok := err.(*contentTypeError); ok {
				w.Header().Set("Content-Type", cte.contentType)
				if _, err := w.Write(cte.data); err != nil {
					slog.Error("Failed to write response", "error", err)
				}
				return
			}
			if de, ok := err.(*downloadError); ok {
				w.Header().Set("Content-Type", de.contentType)
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", de.filename))
				w.Header().Set("Content-Length", strconv.Itoa(len(de.data)))
				if _, err := w.Write(de.data); err != nil {
					slog.Error("Failed to write response", "error", err)
				}
				return
			}
			// Check if it's an HTTPError with status code
			if he, ok := err.(httperror.HTTPError); ok {
				slog.Error("HTTP handler error",
					"method", r.Method,
					"path", r.URL.Path,
					"status", he.StatusCode,
					"error", he.Message)

				// Render error page using template
				var buf bytes.Buffer
				title := http.StatusText(he.StatusCode)
				if title == "" {
					title = "Error"
				}

				err := s.tmpl.ExecuteTemplate(&buf, "error.gohtml", map[string]interface{}{
					"StatusCode": he.StatusCode,
					"Title":      title,
					"Message":    he.Message,
					"BasePath":   s.getBasePath(r),
				})
				if err != nil {
					// Fallback to plain text if template fails
					http.Error(w, he.Message, he.StatusCode)
					return
				}

				w.WriteHeader(he.StatusCode)
				if _, err := w.Write(buf.Bytes()); err != nil {
					slog.Error("Failed to write response", "error", err)
				}
				return
			}
			// Log internal server errors
			slog.Error("HTTP handler error",
				"method", r.Method,
				"path", r.URL.Path,
				"status", http.StatusInternalServerError,
				"error", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// If we have data to write, write it
		if len(data) > 0 {
			if _, err := w.Write(data); err != nil {
				slog.Error("Failed to write response", "error", err)
			}
		}
	}
}

// redirectError represents an HTTP redirect
type redirectError struct {
	url        string
	statusCode int
}

func (e *redirectError) Error() string {
	return fmt.Sprintf("redirect to %s", e.url)
}

// cookieRedirectError represents setting a cookie and redirecting
type cookieRedirectError struct {
	cookie     *http.Cookie
	redirect   string
	statusCode int
}

func (e *cookieRedirectError) Error() string {
	return "cookie and redirect"
}

// contentTypeError represents a response with a specific content type
type contentTypeError struct {
	contentType string
	data        []byte
}

func (e *contentTypeError) Error() string {
	return fmt.Sprintf("response with content-type: %s", e.contentType)
}

// downloadError represents a file download response
type downloadError struct {
	contentType string
	filename    string
	data        []byte
}

func (e *downloadError) Error() string {
	return fmt.Sprintf("download: %s", e.filename)
}

// hxRedirectError represents an htmx redirect using HX-Redirect header
type hxRedirectError struct {
	url    string
	cookie *http.Cookie
}

func (e *hxRedirectError) Error() string {
	return fmt.Sprintf("htmx redirect to %s", e.url)
}

// validateHTMLResponse checks if HTML is well-formed
func validateHTMLResponse(body []byte) error {
	bodyStr := string(body)

	// Check for malformed Go template syntax (spaces between braces)
	if strings.Contains(bodyStr, "{ {") || strings.Contains(bodyStr, "} }") {
		lines := strings.Split(bodyStr, "\n")
		for i, line := range lines {
			if strings.Contains(line, "{ {") || strings.Contains(line, "} }") {
				// Extract context: 7 lines before and 7 lines after
				startLine := i - 7
				if startLine < 0 {
					startLine = 0
				}
				endLine := i + 8
				if endLine > len(lines) {
					endLine = len(lines)
				}

				var contextLines []string
				for j := startLine; j < endLine; j++ {
					lineNum := j + 1
					var prefix string
					if j == i {
						prefix = "!!!"
					} else {
						prefix = fmt.Sprintf("%3d", lineNum)
					}
					contextLines = append(contextLines, fmt.Sprintf("%s %s", prefix, lines[j]))
				}

				// Find which pattern matched to provide helpful explanation
				explanation := ""
				if strings.Contains(line, "{ {") {
					explanation = "Template delimiters should be '{{' not '{ {' (no space between braces)"
				} else if strings.Contains(line, "} }") {
					explanation = "Template delimiters should be '}}' not '} }' (no space between braces)"
				}

				return fmt.Errorf("malformed template syntax at line %d: %s\n\nContext:\n%s",
					i+1, explanation, strings.Join(contextLines, "\n"))
			}
		}
	}

	// Parse HTML to check for structural validity
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("HTML parsing failed: %w", err)
	}

	// Check for basic HTML structure in full pages
	if !strings.Contains(bodyStr, "<html") && !strings.Contains(bodyStr, "<!DOCTYPE") {
		// This is an HTML fragment (e.g., for htmx), which is OK
		return nil
	}

	// For full HTML pages, verify essential elements exist
	hasHTML := false
	hasHead := false
	hasBody := false

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "html":
				hasHTML = true
			case "head":
				hasHead = true
			case "body":
				hasBody = true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}
	traverse(doc)

	if strings.Contains(bodyStr, "<!DOCTYPE") || strings.Contains(bodyStr, "<html") {
		if !hasHTML {
			return fmt.Errorf("HTML document missing <html> element")
		}
		if !hasHead {
			return fmt.Errorf("HTML document missing <head> element")
		}
		if !hasBody {
			return fmt.Errorf("HTML document missing <body> element")
		}
	}

	return nil
}

// htmlValidationResponseWriter wraps http.ResponseWriter to validate HTML responses
type htmlValidationResponseWriter struct {
	http.ResponseWriter
	buffer      *bytes.Buffer
	statusCode  int
	wroteHeader bool
	debugHTML   bool
}

func (w *htmlValidationResponseWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.statusCode = code
		w.wroteHeader = true
		// Don't write header yet, we need to validate first
	}
}

func (w *htmlValidationResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	// Buffer the response
	return w.buffer.Write(data)
}

func (w *htmlValidationResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
}

func (w *htmlValidationResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// htmlValidationMiddleware validates HTML responses when debugHTML is enabled
func (s *Server) htmlValidationMiddleware(next http.Handler) http.Handler {
	if !s.debugHTML {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip validation for WebSocket upgrades and non-HTML endpoints
		if r.Header.Get("Upgrade") == "websocket" ||
			strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasPrefix(r.URL.Path, "/ws-") ||
			strings.Contains(r.URL.Path, "/download") {
			next.ServeHTTP(w, r)
			return
		}

		// Create buffering response writer
		wrapped := &htmlValidationResponseWriter{
			ResponseWriter: w,
			buffer:         &bytes.Buffer{},
			statusCode:     http.StatusOK,
			debugHTML:      s.debugHTML,
		}

		// Call the next handler
		next.ServeHTTP(wrapped, r)

		body := wrapped.buffer.Bytes()

		// Only validate if it looks like HTML (has HTML tags or is text/html)
		contentType := w.Header().Get("Content-Type")
		isHTML := strings.Contains(contentType, "text/html") ||
			bytes.Contains(body, []byte("<html")) ||
			bytes.Contains(body, []byte("<!DOCTYPE")) ||
			bytes.Contains(body, []byte("<div")) ||
			bytes.Contains(body, []byte("<body"))

		if isHTML && len(body) > 0 {
			if err := validateHTMLResponse(body); err != nil {
				slog.Error("HTML validation failed",
					"path", r.URL.Path,
					"error", err.Error())

				// Write 500 error with formatted context
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				errorMsg := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<title>HTML Validation Error</title>
<style>
body { font-family: monospace; margin: 2rem; }
h1 { color: #d32f2f; }
.error-details { background: #f5f5f5; padding: 1rem; border-radius: 4px; margin: 1rem 0; }
pre { background: #fff; padding: 1rem; border: 1px solid #ddd; overflow-x: auto; }
</style>
</head>
<body>
<h1>HTML Validation Error (Debug Mode)</h1>
<div class="error-details">
<p><strong>Path:</strong> %s</p>
<p><strong>Error:</strong></p>
<pre>%s</pre>
</div>
<hr>
<p>This error only appears when --debug-html is enabled.</p>
</body>
</html>`, html.EscapeString(r.URL.Path), html.EscapeString(err.Error()))
				_, _ = w.Write([]byte(errorMsg))
				return
			}
		}

		// Write the validated response
		w.WriteHeader(wrapped.statusCode)
		_, _ = w.Write(body)
	})
}

// loggingMiddleware logs each HTTP request
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer wrapper to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Call the next handler
		next.ServeHTTP(wrapped, r)

		// Log the request
		duration := time.Since(start)

		// Extract test context header if present
		testID := r.Header.Get("X-Test-ID")

		// Build log attributes
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
		}

		// Add test context if present
		if testID != "" {
			attrs = append(attrs, "test_id", testID)
		}

		slog.Info("HTTP request", attrs...)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker to support WebSocket upgrades
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not support hijacking")
}

// Flush implements http.Flusher to support streaming
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Public routes
	mux.HandleFunc("/", s.wrapHandler(s.handleIndex))
	mux.HandleFunc("/login", s.wrapHandler(s.handleLogin))
	mux.HandleFunc("/logout", s.wrapHandler(s.handleLogout))
	mux.HandleFunc("/server-log", s.authMiddleware(s.wrapHandler(s.handleServerLog)))

	// Workspace routes
	mux.HandleFunc("/workspaces/hx-create", s.authMiddleware(s.wrapHandler(s.hxHandleWorkspaceCreate)))
	mux.HandleFunc("/workspaces/{id}", s.authMiddleware(s.wrapHandler(s.handleWorkspaceByID)))
	mux.HandleFunc("/workspaces/{id}/edit", s.authMiddleware(s.wrapHandler(s.handleWorkspaceEdit)))
	mux.HandleFunc("/workspaces/{id}/hx-execute", s.authMiddleware(s.wrapHandler(s.hxHandleExecute)))
	mux.HandleFunc("/workspaces/{id}/hx-finished-processes", s.authMiddleware(s.wrapHandler(s.hxHandleFinishedProcesses)))
	mux.HandleFunc("/workspaces/{id}/json-process-updates", s.authMiddleware(s.wrapHandler(s.jsonHandleProcessUpdates)))
	mux.HandleFunc("/workspaces/{id}/ws-process-updates", s.authMiddleware(s.handleWSProcessUpdates))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}", s.authMiddleware(s.wrapHandler(s.handleProcessByID)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/hx-output", s.authMiddleware(s.wrapHandler(s.hxHandleOutput)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/hx-send-stdin", s.authMiddleware(s.wrapHandler(s.hxHandleSendStdin)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/hx-send-signal", s.authMiddleware(s.wrapHandler(s.hxHandleSendSignal)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/download", s.authMiddleware(s.wrapHandler(s.handleDownloadOutput)))

	// Interactive terminal routes
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/terminal", s.authMiddleware(s.wrapHandler(s.handleTerminal)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/ws-terminal", s.authMiddleware(s.handleWebSocketTerminal))
	mux.HandleFunc("/workspaces/{id}/terminal-execute", s.authMiddleware(s.wrapHandler(s.handleTerminalExecute)))

	// File editor routes
	mux.HandleFunc("/workspaces/{id}/files", s.authMiddleware(s.wrapHandler(s.handleFileEditor)))
	mux.HandleFunc("/workspaces/{id}/files/read", s.authMiddleware(s.wrapHandler(s.handleFileRead)))
	mux.HandleFunc("/workspaces/{id}/files/save", s.authMiddleware(s.wrapHandler(s.handleFileSave)))
	mux.HandleFunc("/workspaces/{id}/files/autocomplete", s.authMiddleware(s.wrapHandler(s.handleFileAutocomplete)))

	// File browser routes (for all local files)
	mux.HandleFunc("/files", s.authMiddleware(s.wrapHandler(s.handleFileBrowser)))
	mux.HandleFunc("/files/view", s.authMiddleware(s.wrapHandler(s.handleFileView)))
	mux.HandleFunc("/files/download", s.authMiddleware(s.wrapHandler(s.handleFileDownload)))

	// System monitor routes
	sysmon.RegisterRoutes(mux, s.tmpl, s.getBasePath, s.authMiddleware,
		func(h func(context.Context, *http.Request) ([]byte, error)) http.HandlerFunc {
			return s.wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
				return h(ctx, r)
			})
		})

	// Legacy/compatibility routes (can be removed later if needed)
	mux.HandleFunc("/workspace/clear", s.authMiddleware(s.wrapHandler(s.handleWorkspaceClear)))

	// Wrap all routes with HTML validation middleware (if enabled), then logging middleware
	handler := s.htmlValidationMiddleware(mux)
	return s.loggingMiddleware(handler)
}

func (s *Server) handleIndex(ctx context.Context, r *http.Request) ([]byte, error) {
	token := s.getSessionToken(r)
	if token != "" {
		valid, err := auth.ValidateSession(s.stateDir, token)
		if err != nil {
			return nil, fmt.Errorf("failed to validate session: %w", err)
		}
		if valid {
			// User is logged in, show workspaces
			return s.handleWorkspaces(ctx, r)
		}
	}

	// User is not logged in, redirect to login
	basePath := s.getBasePath(r)
	return nil, &redirectError{url: basePath + "/login", statusCode: http.StatusSeeOther}
}

func (s *Server) handleLogin(ctx context.Context, r *http.Request) ([]byte, error) {
	basePath := s.getBasePath(r)

	// Handle GET request - show login form
	if r.Method == http.MethodGet {
		var buf bytes.Buffer
		err := s.tmpl.ExecuteTemplate(&buf, "login.gohtml", map[string]interface{}{
			"BasePath": basePath,
		})
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// Handle POST request - authenticate
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	password := r.FormValue("password")
	token, ok := auth.Authenticate(ctx, s.stateDir, password)

	if !ok {
		var buf bytes.Buffer
		err := s.tmpl.ExecuteTemplate(&buf, "login.gohtml", map[string]interface{}{
			"error":    "Invalid password",
			"BasePath": basePath,
		})
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// Create session cookie
	cookie := &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   86400, // 24 hours
	}

	// Check if this is an HTMX request
	isHtmx := r.Header.Get("HX-Request") == "true"

	if isHtmx {
		// For HTMX requests, use HX-Redirect header
		return nil, &hxRedirectError{
			url:    basePath + "/",
			cookie: cookie,
		}
	}

	// For regular requests, use standard HTTP redirect
	return nil, &cookieRedirectError{
		cookie:     cookie,
		redirect:   basePath + "/",
		statusCode: http.StatusSeeOther,
	}
}

func (s *Server) handleLogout(ctx context.Context, r *http.Request) ([]byte, error) {
	basePath := s.getBasePath(r)
	redirectPath := basePath + "/login"

	return nil, &cookieRedirectError{
		cookie: &http.Cookie{
			Name:     "session",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		},
		redirect:   redirectPath,
		statusCode: http.StatusSeeOther,
	}
}

func (s *Server) handleServerLog(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodGet {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	basePath := s.getBasePath(r)
	logPath := filepath.Join(s.stateDir, "server.log")

	// Read the file
	content, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			content = []byte("Server log file does not exist yet.")
		} else {
			return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: fmt.Sprintf("Failed to read server log: %v", err)}
		}
	}

	// Get file info
	var size int64
	var modTime time.Time
	info, err := os.Stat(logPath)
	if err == nil {
		size = info.Size()
		modTime = info.ModTime()
	}

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "file-view.gohtml", map[string]interface{}{
		"BasePath": basePath,
		"Path":     logPath,
		"Content":  string(content),
		"Size":     size,
		"ModTime":  modTime,
		"DirURL":   basePath + "/sysmon",
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) handleWorkspaces(ctx context.Context, r *http.Request) ([]byte, error) {
	basePath := s.getBasePath(r)

	// Get all workspaces for the list
	workspaces, _ := workspace.ListWorkspaces(s.stateDir)
	var workspaceList []map[string]any
	for _, ws := range workspaces {
		workspaceList = append(workspaceList, map[string]any{
			"ID":         ws.ID,
			"Name":       ws.Name,
			"Directory":  ws.Directory,
			"PreCommand": ws.PreCommand,
		})
	}

	var buf bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&buf, "workspaces.gohtml", map[string]any{
		"BasePath":   basePath,
		"Workspaces": workspaceList,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) hxHandleWorkspaceCreate(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	name := r.FormValue("name")
	directory := r.FormValue("directory")
	preCommand := r.FormValue("pre_command")

	if name == "" || directory == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Name and directory are required"}
	}

	// Create the workspace
	ws, err := executor.CreateWorkspace(s.stateDir, name, directory, preCommand)
	if err != nil {
		// Return just the form partial with error and preserved values
		basePath := s.getBasePath(r)
		var buf bytes.Buffer
		renderErr := s.tmpl.ExecuteTemplate(&buf, "hx-workspace-form.gohtml", map[string]any{
			"BasePath": basePath,
			"Error":    err.Error(),
			"FormValues": map[string]string{
				"Name":       name,
				"Directory":  directory,
				"PreCommand": preCommand,
			},
		})
		if renderErr != nil {
			return nil, renderErr
		}
		return buf.Bytes(), nil
	}

	// Use HX-Redirect header for htmx requests
	basePath := s.getBasePath(r)
	redirectURL := fmt.Sprintf("%s/workspaces/%s", basePath, ws.ID)
	return nil, &hxRedirectError{url: redirectURL}
}

// handleWorkspaceByID handles /workspaces/{id}
func (s *Server) handleWorkspaceByID(ctx context.Context, r *http.Request) ([]byte, error) {
	// Extract workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" || workspaceID == "create" {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Not found"}
	}

	// Get the workspace by ID
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Render workspace page
	basePath := s.getBasePath(r)
	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "workspaces.gohtml", map[string]any{
		"BasePath": basePath,
		"CurrentWorkspace": map[string]any{
			"ID":         ws.ID,
			"Name":       ws.Name,
			"Directory":  ws.Directory,
			"PreCommand": ws.PreCommand,
		},
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) handleWorkspaceEdit(ctx context.Context, r *http.Request) ([]byte, error) {
	// Extract workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Not found"}
	}

	// Get the workspace by ID
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	basePath := s.getBasePath(r)

	// Handle GET request - show edit form
	if r.Method == http.MethodGet {
		var buf bytes.Buffer
		err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.gohtml", map[string]any{
			"BasePath": basePath,
			"Workspace": map[string]any{
				"ID":                     ws.ID,
				"Name":                   ws.Name,
				"Directory":              ws.Directory,
				"PreCommand":             ws.PreCommand,
				"DefaultTerminalCommand": ws.DefaultTerminalCommand,
			},
		})
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// Handle POST request - update workspace
	if r.Method == http.MethodPost {
		name := r.FormValue("name")
		preCommand := r.FormValue("pre_command")
		defaultTerminalCommand := r.FormValue("default_terminal_command")

		if name == "" {
			var buf bytes.Buffer
			err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.gohtml", map[string]any{
				"BasePath": basePath,
				"Workspace": map[string]any{
					"ID":                     ws.ID,
					"Name":                   ws.Name,
					"Directory":              ws.Directory,
					"PreCommand":             ws.PreCommand,
					"DefaultTerminalCommand": ws.DefaultTerminalCommand,
				},
				"Error": "Workspace name and directory are required",
			})
			if err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}

		// Update the workspace
		_, err := workspace.UpdateWorkspace(s.stateDir, workspaceID, name, preCommand, defaultTerminalCommand)
		if err != nil {
			var buf bytes.Buffer
			err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.gohtml", map[string]any{
				"BasePath": basePath,
				"Workspace": map[string]any{
					"ID":                     ws.ID,
					"Name":                   name,
					"PreCommand":             preCommand,
					"DefaultTerminalCommand": defaultTerminalCommand,
				},
				"Error": fmt.Sprintf("Failed to update workspace: %v", err),
			})
			if err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}

		// Redirect to workspace page
		return nil, &redirectError{url: fmt.Sprintf("%s/workspaces/%s", basePath, workspaceID), statusCode: http.StatusSeeOther}
	}

	return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
}

func (s *Server) handleWorkspaceClear(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	// Redirect to home page
	basePath := s.getBasePath(r)
	return nil, &redirectError{url: basePath + "/", statusCode: http.StatusSeeOther}
}

func (s *Server) hxHandleExecute(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	command := r.FormValue("command")
	if command == "" {
		command = "bash"
	}

	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Workspace ID is required"}
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	proc, err := executor.Execute(ws, command)
	if err != nil {
		return nil, err
	}

	// Return minimal hidden div that triggers immediate JSON polling via hx-on::after-request
	// The polling will fetch and display the full process details from the JSON endpoint
	var buf bytes.Buffer
	basePath := s.getBasePath(r)
	fmt.Fprintf(&buf, `<div data-process-id="%s" style="display:none" data-output-url="%s/workspaces/%s/processes/%s/hx-output">%s</div>`,
		proc.CommandId, basePath, workspaceID, proc.CommandId, command)
	return buf.Bytes(), nil
}

func (s *Server) jsonHandleProcessUpdates(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Workspace ID is required"}
	}

	// Parse query parameters to get current process IDs
	processIDsParam := r.URL.Query().Get("process_ids")
	var processIDs []string
	if processIDsParam != "" {
		processIDs = strings.Split(processIDsParam, ",")
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	allProcesses, err := workspace.ListProcesses(ws)
	if err != nil {
		return nil, err
	}

	// Build map of received process IDs for quick lookup
	receivedIDs := make(map[string]bool)
	for _, id := range processIDs {
		receivedIDs[id] = true
	}

	// Response structure
	type ProcessUpdate struct {
		ID         string `json:"id"`
		Status     string `json:"status"` // "running", "finished", "new", "unknown"
		HTML       string `json:"html"`
		OutputHTML string `json:"output_html,omitempty"` // For running processes - HTML content for output div
	}

	var updates []ProcessUpdate

	// Check each received ID to see if it's still running or finished
	for _, id := range processIDs {
		found := false
		for _, p := range allProcesses {
			if p.CommandId == id {
				found = true

				if p.Completed {
					// Render finished process HTML (view only, like initial page load)
					html, err := s.renderFinishedProcessSnippet(p, workspaceID, r)
					if err != nil {
						slog.Error("Failed to render finished process", "error", err, "id", p.CommandId)
						continue
					}
					updates = append(updates, ProcessUpdate{
						ID:     id,
						Status: "finished",
						HTML:   html,
					})
				} else {
					// Process is still running - send output update
					outputHTML, err := s.renderProcessOutputHTML(p, workspaceID, r)
					if err != nil {
						slog.Error("Failed to render process output", "error", err, "id", p.CommandId)
						continue
					}
					updates = append(updates, ProcessUpdate{
						ID:         id,
						Status:     "running",
						OutputHTML: outputHTML,
					})
				}
				break
			}
		}
		if !found {
			// ID is unknown (process may have been deleted)
			slog.Warn("Unknown process ID in update request", "id", id)
			updates = append(updates, ProcessUpdate{
				ID:     id,
				Status: "unknown",
				HTML:   "",
			})
		}
	}

	// Check for new running processes not in the received list
	var runningProcesses []*process.Process
	for _, p := range allProcesses {
		if !p.Completed && !receivedIDs[p.CommandId] {
			// Check if actually running
			if p.PID > 0 {
				process, err := os.FindProcess(p.PID)
				if err == nil {
					err = process.Signal(syscall.Signal(0))
					if err != nil {
						// Dead process, skip
						continue
					}
				}
			}
			runningProcesses = append(runningProcesses, p)
		}
	}

	// Sort new running processes by start time (oldest first, to maintain creation order)
	sort.Slice(runningProcesses, func(i, j int) bool {
		return runningProcesses[i].StartTime.Before(runningProcesses[j].StartTime)
	})

	// Add new running processes to updates
	for _, p := range runningProcesses {
		html, err := s.renderRunningProcessSnippet(p, workspaceID, r)
		if err != nil {
			slog.Error("Failed to render new running process", "error", err, "id", p.CommandId)
			continue
		}
		updates = append(updates, ProcessUpdate{
			ID:     p.CommandId,
			Status: "new",
			HTML:   html,
		})
	}

	// Return JSON response
	responseData, err := json.Marshal(map[string]interface{}{
		"updates": updates,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return responseData, nil
}

func (s *Server) renderRunningProcessSnippet(p *process.Process, workspaceID string, r *http.Request) (string, error) {
	var buf bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&buf, "hx-running-process-single.gohtml", map[string]interface{}{
		"Process":     p,
		"BasePath":    s.getBasePath(r),
		"WorkspaceID": workspaceID,
	})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Server) renderFinishedProcessSnippet(p *process.Process, workspaceID string, r *http.Request) (string, error) {
	var buf bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&buf, "hx-finished-process-single.gohtml", map[string]interface{}{
		"Process":     p,
		"BasePath":    s.getBasePath(r),
		"WorkspaceID": workspaceID,
	})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// handleWSProcessUpdates handles WebSocket connections for workspace process updates
func (s *Server) handleWSProcessUpdates(w http.ResponseWriter, r *http.Request) {
	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		slog.Error("WebSocket: Workspace ID is required")
		http.Error(w, "Workspace ID is required", http.StatusBadRequest)
		return
	}

	// Verify workspace exists
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		slog.Error("WebSocket: Workspace not found", "workspaceID", workspaceID, "error", err)
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade to WebSocket", "error", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close WebSocket connection", "error", err)
		}
	}()

	// Create WebSocket client
	clientID := fmt.Sprintf("%s-%d", workspaceID, time.Now().UnixNano())
	client := &wshub.Client{
		ID:          clientID,
		WorkspaceID: workspaceID,
		Conn:        conn,
		SendChan:    make(chan wshub.Message, 100),
		Done:        make(chan struct{}),
	}

	// Register client with hub
	s.wsHub.RegisterClient(client)
	defer s.wsHub.UnregisterClient(clientID)
	defer close(client.Done)

	// Start goroutine to write messages to WebSocket
	go func() {
		for {
			select {
			case msg := <-client.SendChan:
				if err := conn.WriteJSON(msg); err != nil {
					slog.Error("Failed to write WebSocket message", "error", err)
					return
				}
			case <-client.Done:
				return
			}
		}
	}()

	// Send initial reconciliation: full current state
	if err := s.sendWSReconciliation(client, ws, r); err != nil {
		slog.Error("Failed to send reconciliation", "error", err, "workspaceID", workspaceID)
		return
	}

	// Create a ticker for periodic process checks (for detecting finished processes)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Track known process IDs and their states
	knownProcesses := make(map[string]bool) // processID -> completed status

	// Main loop for periodic checks
	for {
		select {
		case <-client.Done:
			return
		case <-ticker.C:
			// Periodic check for process state changes
			if err := s.checkWSProcessUpdates(client, ws, r, knownProcesses); err != nil {
				slog.Error("Failed to check process updates", "error", err)
				return
			}
		}
	}
}

// sendReconciliationEvents sends the full current state to a new SSE client
// sendWSReconciliation sends the full current state to a new WebSocket client
func (s *Server) sendWSReconciliation(client *wshub.Client, ws *workspace.Workspace, r *http.Request) error {
	allProcesses, err := workspace.ListProcesses(ws)
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	// Send running processes
	var runningProcesses []*process.Process
	for _, p := range allProcesses {
		if !p.Completed {
			// Check if actually running
			if p.PID > 0 {
				process, err := os.FindProcess(p.PID)
				if err == nil {
					err = process.Signal(syscall.Signal(0))
					if err != nil {
						// Dead process, skip
						continue
					}
				}
			}
			runningProcesses = append(runningProcesses, p)
		}
	}

	// Sort by start time
	sort.Slice(runningProcesses, func(i, j int) bool {
		return runningProcesses[i].StartTime.Before(runningProcesses[j].StartTime)
	})

	// Send each running process as a reconcile message
	for _, p := range runningProcesses {
		html, err := s.renderRunningProcessSnippet(p, ws.ID, r)
		if err != nil {
			slog.Error("Failed to render running process", "error", err)
			continue
		}

		msg := wshub.Message{
			Type: "reconcile_running",
			Data: map[string]interface{}{
				"id":   p.CommandId,
				"html": html,
			},
		}

		select {
		case client.SendChan <- msg:
		case <-client.Done:
			return fmt.Errorf("client disconnected during reconciliation")
		}
	}

	// Send reconcile_done message
	msg := wshub.Message{
		Type: "reconcile_done",
		Data: map[string]interface{}{
			"count": len(runningProcesses),
		},
	}

	select {
	case client.SendChan <- msg:
	case <-client.Done:
		return fmt.Errorf("client disconnected during reconciliation")
	}

	return nil
}

// checkWSProcessUpdates checks for process state changes and sends updates via WebSocket
func (s *Server) checkWSProcessUpdates(client *wshub.Client, ws *workspace.Workspace, r *http.Request, knownProcesses map[string]bool) error {
	allProcesses, err := workspace.ListProcesses(ws)
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	// Build map of current process states
	currentProcesses := make(map[string]bool) // processID -> completed status

	for _, p := range allProcesses {

		currentProcesses[p.CommandId] = p.Completed

		wasKnown, existed := knownProcesses[p.CommandId]

		if !existed {
			// New process - we haven't seen it before
			if !p.Completed {
				// New running process
				html, err := s.renderRunningProcessSnippet(p, ws.ID, r)
				if err != nil {
					slog.Error("Failed to render new process", "error", err)
					continue
				}

				msg := wshub.Message{
					Type: "process_started",
					Data: map[string]interface{}{
						"id":   p.CommandId,
						"html": html,
					},
				}

				select {
				case client.SendChan <- msg:
				case <-client.Done:
					return fmt.Errorf("client disconnected")
				}
			}
			// If new and already completed, ignore (it finished before we started monitoring)
		} else if !wasKnown && p.Completed {
			// Process transition: was not completed before (!wasKnown means knownProcesses[p.ID]=false),
			// now completed (p.Completed=true)
			msg := wshub.Message{
				Type: "process_finished",
				Data: map[string]interface{}{
					"id": p.CommandId,
				},
			}

			select {
			case client.SendChan <- msg:
			case <-client.Done:
				return fmt.Errorf("client disconnected")
			}
		} else if !p.Completed {
			// Running process - check if we should send update (rate limiting)
			minInterval := 500 * time.Millisecond // 2 updates per second max per process
			if s.wsHub.ShouldSendUpdate(p.CommandId, minInterval) {
				outputHTML, err := s.renderProcessOutputHTML(p, ws.ID, r)
				if err != nil {
					slog.Error("Failed to render process output", "error", err)
					continue
				}

				msg := wshub.Message{
					Type: "process_output",
					Data: map[string]interface{}{
						"id":          p.CommandId,
						"output_html": outputHTML,
					},
				}

				select {
				case client.SendChan <- msg:
				case <-client.Done:
					return fmt.Errorf("client disconnected")
				}
			}
		}
	}

	// Update known processes
	for id, completed := range currentProcesses {
		knownProcesses[id] = completed
	}

	// Clean up rate limiters for processes that no longer exist
	var activeIDs []string
	for id := range currentProcesses {
		activeIDs = append(activeIDs, id)
	}
	s.wsHub.CleanupRateLimiters(activeIDs)

	return nil
}

func (s *Server) hxHandleFinishedProcesses(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Workspace ID is required"}
	}

	// Get offset from query parameter
	offsetStr := r.URL.Query().Get("offset")
	offset := 0
	if offsetStr != "" {
		if _, err := fmt.Sscanf(offsetStr, "%d", &offset); err != nil {
			slog.Warn("Failed to parse offset parameter", "offset", offsetStr, "error", err)
		}
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	allProcesses, err := workspace.ListProcesses(ws)
	if err != nil {
		return nil, err
	}

	// Filter for finished processes only
	var finishedProcesses []*process.Process
	for _, p := range allProcesses {
		if p.Completed {
			finishedProcesses = append(finishedProcesses, p)
		}
	}

	// Sort finished processes by end time (newest first)
	sort.Slice(finishedProcesses, func(i, j int) bool {
		return finishedProcesses[i].EndTime.After(finishedProcesses[j].EndTime)
	})

	// Apply pagination
	const pageSize = 10
	start := offset
	end := offset + pageSize
	if start >= len(finishedProcesses) {
		// No more processes
		return []byte{}, nil
	}
	if end > len(finishedProcesses) {
		end = len(finishedProcesses)
	}

	paginatedProcesses := finishedProcesses[start:end]
	hasMore := end < len(finishedProcesses)
	newOffset := end

	var buf bytes.Buffer

	// Use different template for initial load vs pagination
	templateName := "hx-finished-processes-page.gohtml"
	if offset == 0 {
		templateName = "hx-finished-processes-initial.gohtml"
	}

	err = s.tmpl.ExecuteTemplate(&buf, templateName, map[string]interface{}{
		"FinishedProcesses": paginatedProcesses,
		"HasMore":           hasMore,
		"Offset":            newOffset,
		"BasePath":          s.getBasePath(r),
		"WorkspaceID":       workspaceID,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) handleProcessByID(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get process ID from path parameter
	processID := r.PathValue("processID") // todo: use commandId
	if processID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Process ID is required"}
	}

	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Workspace ID is required"}
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	processDir := filepath.Join(s.stateDir, "workspaces", workspaceID, "processes", processID)
	proc, err := process.LoadProcessFromDir(processDir)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: err.Error()}
	}

	// Check for binary-data marker
	binaryMarkerFile := filepath.Join(processDir, "binary-data")
	isBinary := false
	if _, err := os.Stat(binaryMarkerFile); err == nil {
		isBinary = true
	}

	// Read full output
	stdoutBytes, stderrBytes, stdinBytes, nohupStdoutBytes, nohupStderrBytes, err := outputlog.ReadFiveStreams(proc.OutputFile, "stdout", "stderr", "stdin", "nohup-stdout", "nohup-stderr")
	stdout := string(stdoutBytes)
	stderr := string(stderrBytes)
	stdin := string(stdinBytes)
	nohupStdout := string(nohupStdoutBytes)
	nohupStderr := string(nohupStderrBytes)
	if err != nil {
		stdout = ""
		stderr = ""
		stdin = ""
		nohupStdout = ""
		nohupStderr = ""
	}

	// Read content type and render markdown if needed
	contentType := ""
	outputTypeFile := filepath.Join(processDir, "output-type")
	if data, err := os.ReadFile(outputTypeFile); err == nil {
		parts := strings.Split(strings.TrimSpace(string(data)), ",")
		if len(parts) > 0 {
			contentType = parts[0]
		}
	}

	stdoutHTML := ""
	if contentType == string(outputtype.OutputTypeMarkdown) && stdout != "" {
		stdoutHTML = markdown.RenderToHTML(stdout)
	}

	// Get the process directory path for the file browser link
	processDirPath := filepath.Dir(proc.OutputFile)
	processDirURL := fmt.Sprintf("%s/files?path=%s", s.getBasePath(r), url.QueryEscape(processDirPath))

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "process.gohtml", map[string]interface{}{
		"Process":       proc,
		"Stdout":        stdout,
		"StdoutHTML":    template.HTML(stdoutHTML),
		"Stderr":        stderr,
		"Stdin":         stdin,
		"NohupStdout":   nohupStdout,
		"NohupStderr":   nohupStderr,
		"IsBinary":      isBinary,
		"ContentType":   contentType,
		"BasePath":      s.getBasePath(r),
		"WorkspaceID":   workspaceID,
		"WorkspaceName": ws.Name,
		"ProcessDirURL": processDirURL,
	})
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Server) hxHandleOutput(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get process ID from path parameter
	processID := r.PathValue("processID")
	workspaceID := r.PathValue("id")
	processDir := filepath.Join(s.stateDir, "workspaces", workspaceID, "processes", processID)
	proc, err := process.LoadProcessFromDir(processDir)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: err.Error()}
	}

	expand := r.URL.Query().Get("expand") == "true"

	html, err := s.renderProcessOutput(proc, workspaceID, expand, r)
	if err != nil {
		return nil, err
	}

	return []byte(html), nil
}

type processOutputData struct {
	stdout      string
	stdoutHTML  string // Rendered HTML from markdown
	stderr      string
	stdin       string
	nohupStdout string
	nohupStderr string
	needsExpand bool
	isBinary    bool
	contentType string // Content type from output-type file
}

func (s *Server) prepareProcessOutput(outputFile string, expand bool) (processOutputData, error) {
	// Check for binary-data marker file
	processDir := filepath.Dir(outputFile)
	binaryMarkerFile := filepath.Join(processDir, "binary-data")
	isBinary := false
	if _, err := os.Stat(binaryMarkerFile); err == nil {
		isBinary = true
	}

	// Read combined output from single file
	stdoutBytes, stderrBytes, stdinBytes, nohupStdoutBytes, nohupStderrBytes, err := outputlog.ReadFiveStreams(outputFile, "stdout", "stderr", "stdin", "nohup-stdout", "nohup-stderr")
	stdout := string(stdoutBytes)
	stderr := string(stderrBytes)
	stdin := string(stdinBytes)
	nohupStdout := string(nohupStdoutBytes)
	nohupStderr := string(nohupStderrBytes)
	if err != nil {
		stdout = ""
		stderr = ""
		stdin = ""
		nohupStdout = ""
		nohupStderr = ""
	}

	// Calculate total size and lines
	totalSize := len(stdout) + len(stderr) + len(stdin)
	stdoutLines := strings.Split(stdout, "\n")
	stderrLines := strings.Split(stderr, "\n")
	stdinLines := strings.Split(stdin, "\n")
	totalLines := len(stdoutLines) + len(stderrLines) + len(stdinLines)
	if stdout != "" && stdoutLines[len(stdoutLines)-1] == "" {
		totalLines--
	}
	if stderr != "" && stderrLines[len(stderrLines)-1] == "" {
		totalLines--
	}
	if stdin != "" && stdinLines[len(stdinLines)-1] == "" {
		totalLines--
	}

	// Decide whether to show automatically
	autoShow := totalSize < 1000 && totalLines <= 5

	// Prepare preview
	needsExpand := !autoShow && !expand

	// Read content type from output-type file
	contentType := ""
	outputTypeFile := filepath.Join(processDir, "output-type")
	if data, err := os.ReadFile(outputTypeFile); err == nil {
		// Output-type file format: "type,reason"
		parts := strings.Split(strings.TrimSpace(string(data)), ",")
		if len(parts) > 0 {
			contentType = parts[0]
		}
	}

	// Render markdown to HTML if content type is markdown
	stdoutHTML := ""
	if contentType == string(outputtype.OutputTypeMarkdown) && stdout != "" {
		stdoutHTML = markdown.RenderToHTML(stdout)
	}

	return processOutputData{
		stdout:      stdout,
		stdoutHTML:  stdoutHTML,
		stderr:      stderr,
		stdin:       stdin,
		nohupStdout: nohupStdout,
		nohupStderr: nohupStderr,
		needsExpand: needsExpand,
		isBinary:    isBinary,
		contentType: contentType,
	}, nil
}

func (s *Server) renderProcessOutput(proc *process.Process, workspaceID string, expand bool, r *http.Request) (string, error) {
	outputData, err := s.prepareProcessOutput(proc.OutputFile, expand)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "hx-output.gohtml", map[string]interface{}{
		"Process":     proc,
		"Stdout":      outputData.stdout,
		"StdoutHTML":  template.HTML(outputData.stdoutHTML), // Mark as safe HTML
		"Stderr":      outputData.stderr,
		"Stdin":       outputData.stdin,
		"NohupStdout": outputData.nohupStdout,
		"NohupStderr": outputData.nohupStderr,
		"Type":        "combined",
		"NeedsExpand": outputData.needsExpand,
		"Expanded":    expand,
		"IsBinary":    outputData.isBinary,
		"ContentType": outputData.contentType,
		"BasePath":    s.getBasePath(r),
		"WorkspaceID": workspaceID,
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (s *Server) renderProcessOutputHTML(p *process.Process, workspaceID string, r *http.Request) (string, error) {
	return s.renderProcessOutput(p, workspaceID, false, r)
}

func (s *Server) hxHandleSendStdin(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get workspace ID and process ID from path
	workspaceID := r.PathValue("id")
	processID := r.PathValue("processID")

	// Parse form data
	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	stdinData := r.FormValue("stdin")

	// Get workspace
	_, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Use the same shorter socket path as executor to avoid Unix socket path length limit
	socketPath := filepath.Join("/tmp", "ms-"+processID+".sock")

	// Write to Unix domain socket in a goroutine with timeout
	go func() {
		// Use a timeout channel to avoid blocking forever
		done := make(chan struct{})
		go func() {
			defer close(done)

			// Connect to the Unix domain socket
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				slog.Error("Failed to connect to Unix domain socket", "error", err, "path", socketPath)
				return
			}
			defer func() { _ = conn.Close() }()

			// Write stdin data in OutputLog format
			chunk := outputlog.Chunk{
				Stream:    "stdin",
				Timestamp: time.Now().UTC(),
				Line:      []byte(stdinData + "\n"),
			}
			formatted := outputlog.FormatChunk(chunk)
			_, err = conn.Write(formatted)
			if err != nil {
				slog.Error("Failed to write to Unix domain socket", "error", err)
			}
		}()

		// Wait for write to complete or timeout after 5 seconds
		select {
		case <-done:
			// Write completed
		case <-time.After(5 * time.Second):
			slog.Error("Timeout writing to Unix domain socket", "path", socketPath)
		}
	}()

	// Return empty response (form will reset automatically via hx-on::after-request)
	return []byte{}, nil
}

func (s *Server) hxHandleSendSignal(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get workspace ID and process ID from path
	workspaceID := r.PathValue("id")
	processID := r.PathValue("processID")

	// Parse form data
	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	signalStr := r.FormValue("signal")
	if signalStr == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "No signal provided"}
	}

	// Parse signal number
	var signalNum int
	_, err := fmt.Sscanf(signalStr, "%d", &signalNum)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Invalid signal number"}
	}

	// Get signal name
	signalName := syscall.Signal(signalNum).String()

	// Get process to find PID
	processDir := filepath.Join(s.stateDir, "workspaces", workspaceID, "processes", processID)
	proc, err := process.LoadProcessFromDir(processDir)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: err.Error()}
	}

	if proc.PID == 0 {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Process has no PID"}
	}

	// Send signal to process
	process, err := os.FindProcess(proc.PID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: "Failed to find process"}
	}

	err = process.Signal(syscall.Signal(signalNum))
	if err != nil {
		slog.Error("Failed to send signal to process", "error", err, "pid", proc.PID, "signal", signalName)
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: "Failed to send signal"}
	}

	slog.Info("Signal sent to process", "pid", proc.PID, "signal", signalName, "signal_num", signalNum)

	// Return empty response
	return []byte{}, nil
}

func (s *Server) handleDownloadOutput(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get process ID from path parameter
	processID := r.PathValue("processID")
	if processID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Process ID is required"}
	}

	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Workspace ID is required"}
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Get process directory
	processDir := workspace.GetProcessDir(ws, processID)
	outputFile := filepath.Join(processDir, "output.log")

	// Read raw stdout bytes
	stdoutBytes, err := outputlog.ReadRawStdout(outputFile)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: "Failed to read output"}
	}

	// Read content type from file, or detect it
	contentTypeFile := filepath.Join(processDir, "content-type")
	var contentType string
	if data, err := os.ReadFile(contentTypeFile); err == nil {
		contentType = string(data)
	} else {
		// Fallback: detect content type
		contentType = executor.DetectContentType(stdoutBytes)
	}

	// Determine file extension based on content type
	fileExtension := getFileExtensionFromContentType(contentType)

	// Return download error which will be handled by wrapHandler
	return nil, &downloadError{
		contentType: contentType,
		filename:    processID + fileExtension,
		data:        stdoutBytes,
	}
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := s.getSessionToken(r)
		valid := false
		var expiry time.Time
		if token != "" {
			var err error
			valid, expiry, err = auth.ValidateSessionWithExpiry(s.stateDir, token)
			if err != nil {
				slog.Error("Failed to validate session", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
		}
		if !valid {
			slog.Info("ValidateSession returned false")
			basePath := s.getBasePath(r)
			redirectPath := basePath + "/login"
			http.Redirect(w, r, redirectPath, http.StatusSeeOther)
			return
		}

		// Check if session expires in less than 30 minutes
		timeUntilExpiry := time.Until(expiry)
		if timeUntilExpiry < 30*time.Minute {
			// Extend the session by creating a new token
			newToken, ok := auth.ExtendSession(s.stateDir, token)
			if ok {
				// Set new session cookie
				http.SetCookie(w, &http.Cookie{
					Name:     "session",
					Value:    newToken,
					Path:     "/",
					HttpOnly: true,
					MaxAge:   86400, // 24 hours
				})
				slog.Debug("Session extended", "old_expiry", expiry, "time_until_expiry", timeUntilExpiry)
			} else {
				slog.Error("Failed to extend session")
			}
		}

		next(w, r)
	}
}

func (s *Server) getSessionToken(r *http.Request) string {
	cookie, err := r.Cookie("session")
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) getBasePath(r *http.Request) string {
	// Check for reverse proxy header (standard convention)
	if prefix := r.Header.Get("X-Forwarded-Prefix"); prefix != "" {
		return strings.TrimSuffix(prefix, "/")
	}
	return ""
}

func (s *Server) Start(addr string) error {
	// Clean expired sessions periodically
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		for range ticker.C {
			auth.CleanExpiredSessions(s.stateDir)
		}
	}()

	log.Printf("Starting server on http://%s", addr)
	return http.ListenAndServe(addr, s.SetupRoutes())
}

// GetStateDir returns the state directory, using the provided value,
// or falling back to $STATE_DIRECTORY environment variable, or .mobileshell.
// If createIfMissing is true, it will create the directory if it doesn't exist.
func GetStateDir(stateDir string, createIfMissing bool) (string, error) {
	if stateDir == "" {
		stateDir = os.Getenv("STATE_DIRECTORY")
		if stateDir == "" {
			stateDir = ".mobileshell"
		}
	}

	_, err := os.Stat(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			if createIfMissing {
				if err := os.MkdirAll(stateDir, 0o700); err != nil {
					return "", fmt.Errorf("failed to create state directory: %w", err)
				}
				return stateDir, nil
			}
			return "", fmt.Errorf("STATE_DIRECTORY not set, and %q does not exist. Provide either the env variable or the directory: %w", stateDir, err)
		}
		return "", fmt.Errorf("STATE_DIRECTORY=%s: %w", stateDir, err)
	}

	return stateDir, nil
}

// LogFileHandle wraps a log file and its associated goroutines
type LogFileHandle struct {
	file         *os.File
	wg           *sync.WaitGroup
	stdoutWriter *os.File
	stderrWriter *os.File
	origStdoutFD int
	origStderrFD int
}

// Close restores original stdout/stderr, waits for goroutines to finish, then closes the file
func (h *LogFileHandle) Close() error {
	// Restore original stdout/stderr by duplicating the saved FDs back
	// This causes the pipes to receive EOF
	_ = syscall.Dup2(h.origStdoutFD, int(os.Stdout.Fd()))
	_ = syscall.Dup2(h.origStderrFD, int(os.Stderr.Fd()))

	// Close the pipe writers to signal EOF to the goroutines
	_ = h.stdoutWriter.Close()
	_ = h.stderrWriter.Close()

	// Now wait for goroutines to finish reading
	// Use a channel to signal completion
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	// Wait with a timeout of 2 seconds
	select {
	case <-done:
		// Goroutines finished
	case <-time.After(2 * time.Second):
		// Timeout - this shouldn't happen now that we closed the pipes
		slog.Warn("Timeout waiting for log goroutines to finish")
	}

	// Don't close origStdoutFD and origStderrFD here - they're now the active stdout/stderr
	// after Dup2, and closing them would make stdout/stderr invalid

	// Sync the file to ensure all pending writes are flushed
	_ = h.file.Sync()

	return h.file.Close()
}

// setupServerLog redirects stdout/stderr to both their original destinations and server.log file
// Uses syscall-level file descriptor duplication to ensure all output is captured
// Returns a LogFileHandle (to be closed by caller) and any error
func setupServerLog(stateDir string) (*LogFileHandle, error) {
	logPath := filepath.Join(stateDir, "server.log")

	// Open log file in append mode with create flag
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open server log file: %w", err)
	}

	// Duplicate original stdout (FD 1) and stderr (FD 2) to save them
	origStdoutFD, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to dup stdout: %w", err)
	}

	origStderrFD, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		_ = syscall.Close(origStdoutFD)
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to dup stderr: %w", err)
	}

	// Create new File objects from the duplicated FDs
	origStdout := os.NewFile(uintptr(origStdoutFD), "/dev/stdout")
	origStderr := os.NewFile(uintptr(origStderrFD), "/dev/stderr")

	// Create pipes for stdout
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		_ = origStdout.Close()
		_ = origStderr.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Create pipes for stderr
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = origStdout.Close()
		_ = origStderr.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Redirect FD 1 (stdout) to the write end of stdout pipe
	if err := syscall.Dup2(int(stdoutWriter.Fd()), int(os.Stdout.Fd())); err != nil {
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = origStdout.Close()
		_ = origStderr.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to dup2 stdout: %w", err)
	}

	// Redirect FD 2 (stderr) to the write end of stderr pipe
	if err := syscall.Dup2(int(stderrWriter.Fd()), int(os.Stderr.Fd())); err != nil {
		_ = stderrReader.Close()
		_ = stderrWriter.Close()
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		_ = origStdout.Close()
		_ = origStderr.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to dup2 stderr: %w", err)
	}

	// Create WaitGroup to track goroutines
	var wg sync.WaitGroup

	// Start goroutine to tee stdout
	wg.Add(1)
	go func() {
		defer wg.Done()
		mw := io.MultiWriter(origStdout, logFile)
		_, _ = io.Copy(mw, stdoutReader) // errors are expected when pipes close
		_ = origStdout.Close()
	}()

	// Start goroutine to tee stderr
	wg.Add(1)
	go func() {
		defer wg.Done()
		mw := io.MultiWriter(origStderr, logFile)
		_, _ = io.Copy(mw, stderrReader) // errors are expected when pipes close
		_ = origStderr.Close()
	}()

	return &LogFileHandle{
		file:         logFile,
		wg:           &wg,
		stdoutWriter: stdoutWriter,
		stderrWriter: stderrWriter,
		origStdoutFD: origStdoutFD,
		origStderrFD: origStderrFD,
	}, nil
}

// Run starts the server with the given configuration
func Run(stateDir, port string, debugHTML bool) error {
	var err error
	stateDir, err = GetStateDir(stateDir, false)
	if err != nil {
		return err
	}

	// Set up server logging to both stdout/stderr and server.log
	logFile, err := setupServerLog(stateDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			slog.Error("failed to close server log file", "error", err)
		}
	}()

	// Create hashed-passwords directory if it doesn't exist
	passwordDir := filepath.Join(stateDir, "hashed-passwords")
	if err := os.MkdirAll(passwordDir, 0o700); err != nil {
		return fmt.Errorf("failed to create hashed-passwords directory: %w", err)
	}

	// Check if any passwords are configured
	entries, err := os.ReadDir(passwordDir)
	if err != nil {
		return fmt.Errorf("failed to read hashed-passwords directory: %w", err)
	}
	if len(entries) == 0 {
		slog.Warn("No passwords configured yet. Add one with: mobileshell add-password")
	}

	// Initialize auth
	if err := auth.InitAuth(stateDir); err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Initialize executor
	if err := executor.InitExecutor(stateDir); err != nil {
		return fmt.Errorf("failed to initialize executor: %w", err)
	}

	srv, err := New(stateDir, debugHTML)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	if debugHTML {
		slog.Info("HTML validation enabled - invalid HTML will return 500 errors")
	}

	addr := fmt.Sprintf("localhost:%s", port)
	return srv.Start(addr)
}

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin: func(r *http.Request) bool {
		// Check if the Origin header matches the Host header
		// This prevents cross-site WebSocket hijacking attacks
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Allow requests without Origin header (e.g., from native apps)
			return true
		}

		// Parse origin and compare with expected host
		host := r.Host
		expectedOrigins := []string{
			"http://" + host,
			"https://" + host,
		}

		for _, expected := range expectedOrigins {
			if origin == expected {
				return true
			}
		}

		slog.Warn("Rejected WebSocket connection from unauthorized origin", "origin", origin, "host", host)
		return false
	},
}

// handleTerminal shows the interactive terminal page
func (s *Server) handleTerminal(ctx context.Context, r *http.Request) ([]byte, error) {
	workspaceID := r.PathValue("id")
	processID := r.PathValue("processID")

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Get process
	processDir := filepath.Join(s.stateDir, "workspaces", workspaceID, "processes", processID)
	proc, err := process.LoadProcessFromDir(processDir)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: err.Error()}
	}

	basePath := s.getBasePath(r)

	data := struct {
		BasePath      string
		WorkspaceID   string
		WorkspaceName string
		Process       *process.Process
	}{
		BasePath:      basePath,
		WorkspaceID:   workspaceID,
		WorkspaceName: ws.Name,
		Process:       proc,
	}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "terminal.gohtml", data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// handleTerminalExecute executes a command in interactive terminal mode
func (s *Server) handleTerminalExecute(ctx context.Context, r *http.Request) ([]byte, error) {
	workspaceID := r.PathValue("id")

	// Parse form data
	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	command := r.FormValue("command")
	if command == "" {
		// Use workspace default if set, otherwise auto-detect
		if ws.DefaultTerminalCommand != "" {
			command = ws.DefaultTerminalCommand
		} else {
			// Check if tmux is available
			if _, err := exec.LookPath("tmux"); err == nil {
				command = "tmux"
			} else {
				command = "bash"
			}
		}
	}

	// Create the process
	proc, err := executor.Execute(ws, command)
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %w", err)
	}

	// Redirect to terminal view
	basePath := s.getBasePath(r)
	redirectURL := fmt.Sprintf("%s/workspaces/%s/processes/%s/terminal", basePath, workspaceID, proc.CommandId)
	return nil, &redirectError{url: redirectURL, statusCode: http.StatusSeeOther}
}

// handleWebSocketTerminal handles WebSocket connections for interactive terminals
func (s *Server) handleWebSocketTerminal(w http.ResponseWriter, r *http.Request) {
	// Authenticate
	token := s.getSessionToken(r)
	if token == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	valid, err := auth.ValidateSession(s.stateDir, token)
	if err != nil || !valid {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	workspaceID := r.PathValue("id")
	processID := r.PathValue("processID")

	// Get the process to get the command
	processDir := filepath.Join(s.stateDir, "workspaces", workspaceID, "processes", processID)
	proc, err := process.LoadProcessFromDir(processDir)
	if err != nil {
		http.Error(w, "Process not found", http.StatusNotFound)
		return
	}

	// Upgrade to WebSocket
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade to WebSocket", "error", err)
		return
	}

	// Create terminal session
	session, err := terminal.NewSession(ws, s.stateDir, workspaceID, proc.Command)
	if err != nil {
		slog.Error("Failed to create terminal session", "error", err)
		_ = ws.Close()
		return
	}

	// Start the session
	session.Start()

	// Wait for session to complete
	session.Wait()

	// Clean up
	_ = session.Close()
}

// handleFileEditor shows the file editor page
func (s *Server) handleFileEditor(ctx context.Context, r *http.Request) ([]byte, error) {
	workspaceID := r.PathValue("id")

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	basePath := s.getBasePath(r)

	data := struct {
		BasePath      string
		WorkspaceID   string
		WorkspaceName string
		Directory     string
	}{
		BasePath:      basePath,
		WorkspaceID:   workspaceID,
		WorkspaceName: ws.Name,
		Directory:     ws.Directory,
	}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "file-editor.gohtml", data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// handleFileRead reads a file and returns its content with session info
func (s *Server) handleFileRead(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	workspaceID := r.PathValue("id")

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	relativePath := r.FormValue("file_path")
	if relativePath == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "File path is required"}
	}

	// Resolve file path relative to workspace directory
	filePath := filepath.Join(ws.Directory, relativePath)

	// Read file
	session, err := fileeditor.ReadFile(filePath)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: fmt.Sprintf("Failed to read file: %v", err)}
	}

	basePath := s.getBasePath(r)

	data := struct {
		BasePath         string
		WorkspaceID      string
		FilePath         string
		Content          string
		OriginalChecksum string
		IsNewFile        bool
	}{
		BasePath:         basePath,
		WorkspaceID:      workspaceID,
		FilePath:         relativePath,
		Content:          session.OriginalContent,
		OriginalChecksum: session.OriginalChecksum,
		IsNewFile:        session.OriginalContent == "",
	}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "hx-file-content.gohtml", data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// handleFileSave saves a file with conflict detection
func (s *Server) handleFileSave(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	workspaceID := r.PathValue("id")

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	relativePath := r.FormValue("file_path")
	newContent := r.FormValue("content")
	originalChecksum := r.FormValue("original_checksum")

	if relativePath == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "File path is required"}
	}

	// Resolve file path relative to workspace directory
	filePath := filepath.Join(ws.Directory, relativePath)

	// Read current file state
	currentSession, err := fileeditor.ReadFile(filePath)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: fmt.Sprintf("Failed to read file: %v", err)}
	}

	// Check if file has been modified since the user loaded it
	if currentSession.OriginalChecksum != originalChecksum {
		// File has been modified externally - create a conflict response
		result := &fileeditor.FileEditResult{
			Success:          false,
			ConflictDetected: true,
			Message:          "File has been modified externally. Please review the current content and try again.",
			// We can only show the diff between current and proposed since we don't have the original
			ProposedDiff: fileeditor.GenerateDiff(currentSession.OriginalContent, newContent),
		}

		basePath := s.getBasePath(r)
		data := struct {
			BasePath         string
			WorkspaceID      string
			FilePath         string
			Success          bool
			Message          string
			ConflictDetected bool
			ExternalDiff     string
			ProposedDiff     string
			NewChecksum      string
			CurrentContent   string
		}{
			BasePath:         basePath,
			WorkspaceID:      workspaceID,
			FilePath:         relativePath,
			Success:          result.Success,
			Message:          result.Message,
			ConflictDetected: result.ConflictDetected,
			ProposedDiff:     result.ProposedDiff,
			CurrentContent:   currentSession.OriginalContent,
		}

		var buf bytes.Buffer
		if err := s.tmpl.ExecuteTemplate(&buf, "hx-file-save-result.gohtml", data); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// No conflict - proceed with saving
	session := currentSession

	// Try to write the file
	result, err := fileeditor.WriteFile(session, newContent)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: fmt.Sprintf("Failed to write file: %v", err)}
	}

	basePath := s.getBasePath(r)

	data := struct {
		BasePath         string
		WorkspaceID      string
		FilePath         string
		Success          bool
		Message          string
		ConflictDetected bool
		ExternalDiff     string
		ProposedDiff     string
		NewChecksum      string
	}{
		BasePath:         basePath,
		WorkspaceID:      workspaceID,
		FilePath:         relativePath,
		Success:          result.Success,
		Message:          result.Message,
		ConflictDetected: result.ConflictDetected,
		ExternalDiff:     result.ExternalDiff,
		ProposedDiff:     result.ProposedDiff,
		NewChecksum:      result.NewChecksum,
	}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "hx-file-save-result.gohtml", data); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// handleFileAutocomplete provides autocomplete suggestions for file paths
func (s *Server) handleFileAutocomplete(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodGet {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	workspaceID := r.PathValue("id")

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Get the pattern from query parameter
	pattern := r.URL.Query().Get("pattern")
	if pattern == "" {
		// Return empty results for empty pattern
		result := &fileeditor.AutocompleteResult{
			Matches:      []fileeditor.FileMatch{},
			TotalMatches: 0,
			HasMore:      false,
			TimedOut:     false,
		}
		jsonBytes, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		return jsonBytes, nil
	}

	// Create a context with timeout for the search (5 seconds)
	searchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Perform the search
	result, err := fileeditor.SearchFiles(searchCtx, ws.Directory, pattern, 10)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: fmt.Sprintf("Failed to search files: %v", err)}
	}

	// Return JSON response
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return jsonBytes, nil
}

// handleFileBrowser handles browsing local files and directories
func (s *Server) handleFileBrowser(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodGet {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	basePath := s.getBasePath(r)

	// Get the path from query parameter, default to root
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		filePath = "/"
	}

	// Clean and resolve the path
	filePath = filepath.Clean(filePath)

	// Get file info
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: fmt.Sprintf("Path not found: %v", err)}
	}

	// If it's a directory, show directory listing
	if info.IsDir() {
		entries, err := os.ReadDir(filePath)
		if err != nil {
			return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: fmt.Sprintf("Failed to read directory: %v", err)}
		}

		// Get file info for each entry
		type FileInfo struct {
			Name        string
			Path        string
			IsDir       bool
			Size        int64
			ModTime     time.Time
			Mode        os.FileMode
			Owner       string
			DownloadURL string
			ViewURL     string
			EditURL     string
		}

		var files []FileInfo
		for _, entry := range entries {
			entryPath := filepath.Join(filePath, entry.Name())
			entryInfo, err := entry.Info()
			if err != nil {
				continue // Skip entries we can't stat
			}

			// Get owner (Unix only)
			owner := ""
			if stat, ok := entryInfo.Sys().(*syscall.Stat_t); ok {
				owner = fmt.Sprintf("%d:%d", stat.Uid, stat.Gid)
			}

			fileInfo := FileInfo{
				Name:        entry.Name(),
				Path:        entryPath,
				IsDir:       entry.IsDir(),
				Size:        entryInfo.Size(),
				ModTime:     entryInfo.ModTime(),
				Mode:        entryInfo.Mode(),
				Owner:       owner,
				DownloadURL: fmt.Sprintf("%s/files/download?path=%s", basePath, url.QueryEscape(entryPath)),
				ViewURL:     fmt.Sprintf("%s/files/view?path=%s", basePath, url.QueryEscape(entryPath)),
				EditURL:     "",
			}

			files = append(files, fileInfo)
		}

		// Sort: directories first, then by name
		sort.Slice(files, func(i, j int) bool {
			if files[i].IsDir != files[j].IsDir {
				return files[i].IsDir
			}
			return files[i].Name < files[j].Name
		})

		// Get parent directory
		parentDir := filepath.Dir(filePath)
		if filePath == "/" {
			parentDir = ""
		}

		var buf bytes.Buffer
		err = s.tmpl.ExecuteTemplate(&buf, "file-browser.gohtml", map[string]interface{}{
			"BasePath":  basePath,
			"Path":      filePath,
			"ParentDir": parentDir,
			"Files":     files,
		})
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// If it's a file, redirect to view
	return nil, &redirectError{
		url:        fmt.Sprintf("%s/files/view?path=%s", basePath, url.QueryEscape(filePath)),
		statusCode: http.StatusSeeOther,
	}
}

// handleFileView handles viewing a file in read-only mode
func (s *Server) handleFileView(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodGet {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	basePath := s.getBasePath(r)
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Missing path parameter"}
	}

	// Clean the path
	filePath = filepath.Clean(filePath)

	// Read the file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: fmt.Sprintf("Failed to read file: %v", err)}
	}

	// Get file info
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: fmt.Sprintf("Failed to get file info: %v", err)}
	}

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "file-view.gohtml", map[string]interface{}{
		"BasePath": basePath,
		"Path":     filePath,
		"Content":  string(content),
		"Size":     info.Size(),
		"ModTime":  info.ModTime(),
		"DirURL":   fmt.Sprintf("%s/files?path=%s", basePath, url.QueryEscape(filepath.Dir(filePath))),
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// handleFileDownload handles downloading a file
func (s *Server) handleFileDownload(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodGet {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Missing path parameter"}
	}

	// Clean the path
	filePath = filepath.Clean(filePath)

	// Check if file exists and is not a directory
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: fmt.Sprintf("File not found: %v", err)}
	}
	if info.IsDir() {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Cannot download a directory"}
	}

	// Read the file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusInternalServerError, Message: fmt.Sprintf("Failed to read file: %v", err)}
	}

	return content, nil
}
