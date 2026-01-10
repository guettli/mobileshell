package server

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
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
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"mobileshell/internal/auth"
	"mobileshell/internal/claude"
	"mobileshell/internal/executor"
	"mobileshell/internal/fileeditor"
	"mobileshell/internal/outputlog"
	"mobileshell/internal/sysmon"
	"mobileshell/internal/terminal"
	"mobileshell/internal/workspace"
	"mobileshell/internal/wshub"
	"mobileshell/pkg/markdown"
	"mobileshell/pkg/outputtype"
	"mobileshell/pkg/httperror"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	stateDir string
	tmpl     *template.Template
	wsHub    *wshub.Hub
}

func New(stateDir string) (*Server, error) {
	funcMap := template.FuncMap{
		"formatDuration": formatDuration,
		"split": func(s, sep string) []string {
			return strings.Split(s, sep)
		},
		"divf": func(a int64, b float64) float64 {
			return float64(a) / b
		},
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		stateDir: stateDir,
		tmpl:     tmpl,
		wsHub:    wshub.NewHub(),
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
				w.Header().Set("HX-Redirect", hxre.url)
				return
			}
			if cte, ok := err.(*contentTypeError); ok {
				w.Header().Set("Content-Type", cte.contentType)
				_, _ = w.Write(cte.data)
				return
			}
			if de, ok := err.(*downloadError); ok {
				w.Header().Set("Content-Type", de.contentType)
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", de.filename))
				w.Header().Set("Content-Length", strconv.Itoa(len(de.data)))
				_, _ = w.Write(de.data)
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

				err := s.tmpl.ExecuteTemplate(&buf, "error.html", map[string]interface{}{
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
				_, _ = w.Write(buf.Bytes())
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
			_, _ = w.Write(data)
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
	url string
}

func (e *hxRedirectError) Error() string {
	return fmt.Sprintf("htmx redirect to %s", e.url)
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
		slog.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
		)
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

	// Workspace routes
	mux.HandleFunc("/workspaces/hx-create", s.authMiddleware(s.wrapHandler(s.hxHandleWorkspaceCreate)))
	mux.HandleFunc("/workspaces/{id}", s.authMiddleware(s.wrapHandler(s.handleWorkspaceByID)))
	mux.HandleFunc("/workspaces/{id}/edit", s.authMiddleware(s.wrapHandler(s.handleWorkspaceEdit)))
	mux.HandleFunc("/workspaces/{id}/hx-execute", s.authMiddleware(s.wrapHandler(s.hxHandleExecute)))
	mux.HandleFunc("/workspaces/{id}/hx-execute-claude", s.authMiddleware(s.wrapHandler(s.hxExecuteClaude)))
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

	// Wrap all routes with logging middleware
	return s.loggingMiddleware(mux)
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
		err := s.tmpl.ExecuteTemplate(&buf, "login.html", map[string]interface{}{
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
		err := s.tmpl.ExecuteTemplate(&buf, "login.html", map[string]interface{}{
			"error":    "Invalid password",
			"BasePath": basePath,
		})
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// Return cookie and redirect to home (which will show workspaces)
	return nil, &cookieRedirectError{
		cookie: &http.Cookie{
			Name:     "session",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			MaxAge:   86400, // 24 hours
		},
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

func (s *Server) handleWorkspaces(ctx context.Context, r *http.Request) ([]byte, error) {
	basePath := s.getBasePath(r)

	// Get all workspaces for the list
	workspaces, _ := executor.ListWorkspaces(s.stateDir)
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
	err := s.tmpl.ExecuteTemplate(&buf, "workspaces.html", map[string]any{
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
		renderErr := s.tmpl.ExecuteTemplate(&buf, "hx-workspace-form.html", map[string]any{
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
	err = s.tmpl.ExecuteTemplate(&buf, "workspaces.html", map[string]any{
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
		err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.html", map[string]any{
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
		directory := r.FormValue("directory")
		preCommand := r.FormValue("pre_command")
		defaultTerminalCommand := r.FormValue("default_terminal_command")

		if name == "" || directory == "" {
			var buf bytes.Buffer
			err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.html", map[string]any{
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
		_, err := workspace.UpdateWorkspace(s.stateDir, workspaceID, name, directory, preCommand, defaultTerminalCommand)
		if err != nil {
			var buf bytes.Buffer
			err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.html", map[string]any{
				"BasePath": basePath,
				"Workspace": map[string]any{
					"ID":                     ws.ID,
					"Name":                   name,
					"Directory":              directory,
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

	proc, err := executor.Execute(s.stateDir, ws, command)
	if err != nil {
		return nil, err
	}

	// Return minimal hidden div that triggers immediate JSON polling via hx-on::after-request
	// The polling will fetch and display the full process details from the JSON endpoint
	var buf bytes.Buffer
	basePath := s.getBasePath(r)
	fmt.Fprintf(&buf, `<div data-process-id="%s" style="display:none" data-output-url="%s/workspaces/%s/processes/%s/hx-output">%s</div>`,
		proc.ID, basePath, workspaceID, proc.ID, command)
	return buf.Bytes(), nil
}

func (s *Server) hxExecuteClaude(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	// Parse form to get prompt
	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	prompt := r.FormValue("prompt")
	if prompt == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Prompt is required"}
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

	// Build Claude command in dialog mode for interactive session
	claudeArgs := claude.BuildCommand(prompt, claude.CommandOptions{
		DialogMode: true,
		StreamJSON: true,
		NoSession:  true,
		WorkDir:    ws.Directory,
	})

	// Join args to create command string
	command := "claude " + strings.Join(claudeArgs, " ")

	// Execute as background process via nohup (like other commands)
	proc, err := executor.Execute(s.stateDir, ws, command)
	if err != nil {
		return nil, err
	}

	// Return minimal hidden div that triggers immediate JSON polling via hx-on::after-request
	// The polling will fetch and display the full process details from the JSON endpoint
	var buf bytes.Buffer
	basePath := s.getBasePath(r)
	fmt.Fprintf(&buf, `<div data-process-id="%s" style="display:none" data-output-url="%s/workspaces/%s/processes/%s/hx-output">%s</div>`,
		proc.ID, basePath, workspaceID, proc.ID, command)
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

	allProcesses, err := executor.ListWorkspaceProcesses(ws)
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
			if p.ID == id {
				found = true
				// Check if process is actually still running
				if !p.Completed && p.PID > 0 {
					process, err := os.FindProcess(p.PID)
					if err == nil {
						err = process.Signal(syscall.Signal(0))
						if err != nil {
							// Process doesn't exist anymore, mark as completed
							slog.Info("Detected dead process, updating status", "pid", p.PID, "id", p.ID)
							_ = workspace.UpdateProcessExit(ws, p.Hash, -1, "")
							p.Completed = true
						}
					}
				}

				if p.Completed {
					// Render finished process HTML (view only, like initial page load)
					html, err := s.renderFinishedProcessSnippet(p, workspaceID, r)
					if err != nil {
						slog.Error("Failed to render finished process", "error", err, "id", p.ID)
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
						slog.Error("Failed to render process output", "error", err, "id", p.ID)
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
	var runningProcesses []*executor.Process
	for _, p := range allProcesses {
		if !p.Completed && !receivedIDs[p.ID] {
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
			slog.Error("Failed to render new running process", "error", err, "id", p.ID)
			continue
		}
		updates = append(updates, ProcessUpdate{
			ID:     p.ID,
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

func (s *Server) renderRunningProcessSnippet(p *executor.Process, workspaceID string, r *http.Request) (string, error) {
	var buf bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&buf, "hx-running-process-single.html", map[string]interface{}{
		"Process":     p,
		"BasePath":    s.getBasePath(r),
		"WorkspaceID": workspaceID,
	})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Server) renderFinishedProcessSnippet(p *executor.Process, workspaceID string, r *http.Request) (string, error) {
	var buf bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&buf, "hx-finished-process-single.html", map[string]interface{}{
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
	allProcesses, err := executor.ListWorkspaceProcesses(ws)
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	// Send running processes
	var runningProcesses []*executor.Process
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
				"id":   p.ID,
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
	allProcesses, err := executor.ListWorkspaceProcesses(ws)
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	// Build map of current process states
	currentProcesses := make(map[string]bool) // processID -> completed status

	for _, p := range allProcesses {
		if !p.Completed {
			// Check if actually running
			if p.PID > 0 {
				process, err := os.FindProcess(p.PID)
				if err == nil {
					err = process.Signal(syscall.Signal(0))
					if err != nil {
						// Process died, mark as completed and log any update errors
						slog.Info("Detected dead process, updating status", "pid", p.PID, "id", p.ID)
						if err := workspace.UpdateProcessExit(ws, p.Hash, -1, ""); err != nil {
							slog.Error("Failed to update dead process status", "error", err, "pid", p.PID, "id", p.ID)
						}
						p.Completed = true
					}
				}
			}
		}

		currentProcesses[p.ID] = p.Completed

		wasKnown, existed := knownProcesses[p.ID]
		
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
						"id":   p.ID,
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
					"id": p.ID,
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
			if s.wsHub.ShouldSendUpdate(p.ID, minInterval) {
				outputHTML, err := s.renderProcessOutputHTML(p, ws.ID, r)
				if err != nil {
					slog.Error("Failed to render process output", "error", err)
					continue
				}

				msg := wshub.Message{
					Type: "process_output",
					Data: map[string]interface{}{
						"id":          p.ID,
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
		_, _ = fmt.Sscanf(offsetStr, "%d", &offset)
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	allProcesses, err := executor.ListWorkspaceProcesses(ws)
	if err != nil {
		return nil, err
	}

	// Filter for finished processes only
	var finishedProcesses []*executor.Process
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
	templateName := "hx-finished-processes-page.html"
	if offset == 0 {
		templateName = "hx-finished-processes-initial.html"
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

	// Get the process
	proc, ok := executor.GetProcess(s.stateDir, processID)
	if !ok {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Process not found"}
	}

	// Check for binary-data marker
	processDir := filepath.Dir(proc.OutputFile)
	binaryMarkerFile := filepath.Join(processDir, "binary-data")
	isBinary := false
	if _, err := os.Stat(binaryMarkerFile); err == nil {
		isBinary = true
	}

	// Read full output
	stdout, stderr, stdin, err := outputlog.ReadCombinedOutput(proc.OutputFile)
	if err != nil {
		stdout = ""
		stderr = ""
		stdin = ""
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
	err = s.tmpl.ExecuteTemplate(&buf, "process.html", map[string]interface{}{
		"Process":       proc,
		"Stdout":        stdout,
		"StdoutHTML":    template.HTML(stdoutHTML),
		"Stderr":        stderr,
		"Stdin":         stdin,
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

	proc, ok := executor.GetProcess(s.stateDir, processID)
	if !ok {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Process not found"}
	}

	expand := r.URL.Query().Get("expand") == "true"
	workspaceID := filepath.Base(filepath.Dir(filepath.Dir(proc.OutputFile)))

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
	stdout, stderr, stdin, err := outputlog.ReadCombinedOutput(outputFile)
	if err != nil {
		stdout = ""
		stderr = ""
		stdin = ""
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
		needsExpand: needsExpand,
		isBinary:    isBinary,
		contentType: contentType,
	}, nil
}

func (s *Server) renderProcessOutput(proc *executor.Process, workspaceID string, expand bool, r *http.Request) (string, error) {
	outputData, err := s.prepareProcessOutput(proc.OutputFile, expand)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "hx-output.html", map[string]interface{}{
		"Process":     proc,
		"Stdout":      outputData.stdout,
		"StdoutHTML":  template.HTML(outputData.stdoutHTML), // Mark as safe HTML
		"Stderr":      outputData.stderr,
		"Stdin":       outputData.stdin,
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

func (s *Server) renderProcessOutputHTML(p *executor.Process, workspaceID string, r *http.Request) (string, error) {
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
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Get process directory
	processDir := workspace.GetProcessDir(ws, processID)
	pipePath := filepath.Join(processDir, "stdin.pipe")

	// Write to named pipe in a goroutine with timeout
	// The readStdinPipe goroutine in the nohup process keeps the pipe open for reading
	go func() {
		// Use a timeout channel to avoid blocking forever
		done := make(chan struct{})
		go func() {
			defer close(done)

			// Try to open in blocking mode with a reasonable timeout via the goroutine
			file, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
			if err != nil {
				slog.Error("Failed to open stdin pipe", "error", err, "path", pipePath)
				return
			}
			defer func() { _ = file.Close() }()

			_, err = file.WriteString(stdinData + "\n")
			if err != nil {
				slog.Error("Failed to write to stdin pipe", "error", err)
			}
		}()

		// Wait for write to complete or timeout after 5 seconds
		select {
		case <-done:
			// Write completed
		case <-time.After(5 * time.Second):
			slog.Error("Timeout writing to stdin pipe", "path", pipePath)
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

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Workspace not found"}
	}

	// Get process to find PID
	proc, ok := executor.GetProcess(s.stateDir, processID)
	if !ok {
		return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Process not found"}
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

	// Log the signal send to output.log
	processDir := workspace.GetProcessDir(ws, processID)
	outputFile := filepath.Join(processDir, "output.log")

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	content := fmt.Sprintf("%d %s", signalNum, signalName)
	logLine := fmt.Sprintf("signal-sent %s %d: %s\n", timestamp, len(content), content)

	// Append to output.log
	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.WriteString(logLine)
		_ = f.Close()
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

// Run starts the server with the given configuration
func Run(stateDir, port string) error {
	var err error
	stateDir, err = GetStateDir(stateDir, false)
	if err != nil {
		return err
	}

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

	srv, err := New(stateDir)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
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
proc, found := executor.GetProcess(s.stateDir, processID)
if !found {
return nil, httperror.HTTPError{StatusCode: http.StatusNotFound, Message: "Process not found"}
}

basePath := s.getBasePath(r)

data := struct {
BasePath     string
WorkspaceID  string
WorkspaceName string
Process      *executor.Process
}{
BasePath:     basePath,
WorkspaceID:  workspaceID,
WorkspaceName: ws.Name,
Process:      proc,
}

var buf bytes.Buffer
if err := s.tmpl.ExecuteTemplate(&buf, "terminal.html", data); err != nil {
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
proc, err := executor.Execute(s.stateDir, ws, command)
if err != nil {
return nil, fmt.Errorf("failed to execute command: %w", err)
}

// Redirect to terminal view
basePath := s.getBasePath(r)
redirectURL := fmt.Sprintf("%s/workspaces/%s/processes/%s/terminal", basePath, workspaceID, proc.ID)
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
proc, found := executor.GetProcess(s.stateDir, processID)
if !found {
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
	if err := s.tmpl.ExecuteTemplate(&buf, "file-editor.html", data); err != nil {
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
	if err := s.tmpl.ExecuteTemplate(&buf, "hx-file-content.html", data); err != nil {
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
			ProposedDiff:     fileeditor.GenerateDiff(currentSession.OriginalContent, newContent),
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
		if err := s.tmpl.ExecuteTemplate(&buf, "hx-file-save-result.html", data); err != nil {
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
	if err := s.tmpl.ExecuteTemplate(&buf, "hx-file-save-result.html", data); err != nil {
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
		err = s.tmpl.ExecuteTemplate(&buf, "file-browser.html", map[string]interface{}{
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
	err = s.tmpl.ExecuteTemplate(&buf, "file-view.html", map[string]interface{}{
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

