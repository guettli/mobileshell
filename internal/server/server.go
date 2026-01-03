package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"mobileshell/internal/auth"
	"mobileshell/internal/executor"
	"mobileshell/internal/terminal"
	"mobileshell/internal/workspace"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	stateDir string
	tmpl     *template.Template
}

func New(stateDir string) (*Server, error) {
	funcMap := template.FuncMap{
		"formatDuration": formatDuration,
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		stateDir: stateDir,
		tmpl:     tmpl,
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
			// Check if it's an httpError with status code
			if he, ok := err.(*httpError); ok {
				slog.Error("HTTP handler error",
					"method", r.Method,
					"path", r.URL.Path,
					"status", he.statusCode,
					"error", he.message)

				// Render error page using template
				var buf bytes.Buffer
				title := http.StatusText(he.statusCode)
				if title == "" {
					title = "Error"
				}

				err := s.tmpl.ExecuteTemplate(&buf, "error.html", map[string]interface{}{
					"StatusCode": he.statusCode,
					"Title":      title,
					"Message":    he.message,
					"BasePath":   s.getBasePath(r),
				})
				if err != nil {
					// Fallback to plain text if template fails
					http.Error(w, he.message, he.statusCode)
					return
				}

				w.WriteHeader(he.statusCode)
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

// httpError represents an HTTP error with a status code
type httpError struct {
	message    string
	statusCode int
}

func (e *httpError) Error() string {
	return e.message
}

func newHTTPError(statusCode int, message string) error {
	return &httpError{statusCode: statusCode, message: message}
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
	mux.HandleFunc("/workspaces/{id}/hx-finished-processes", s.authMiddleware(s.wrapHandler(s.hxHandleFinishedProcesses)))
	mux.HandleFunc("/workspaces/{id}/json-process-updates", s.authMiddleware(s.wrapHandler(s.jsonHandleProcessUpdates)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}", s.authMiddleware(s.wrapHandler(s.handleProcessByID)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/hx-output", s.authMiddleware(s.wrapHandler(s.hxHandleOutput)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/hx-send-stdin", s.authMiddleware(s.wrapHandler(s.hxHandleSendStdin)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/hx-send-signal", s.authMiddleware(s.wrapHandler(s.hxHandleSendSignal)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/download", s.authMiddleware(s.wrapHandler(s.handleDownloadOutput)))
	
	// Interactive terminal routes
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/terminal", s.authMiddleware(s.wrapHandler(s.handleTerminal)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/ws-terminal", s.authMiddleware(s.handleWebSocketTerminal))
	mux.HandleFunc("/workspaces/{id}/terminal-execute", s.authMiddleware(s.wrapHandler(s.handleTerminalExecute)))

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
		return nil, newHTTPError(http.StatusMethodNotAllowed, "Method not allowed")
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
		return nil, newHTTPError(http.StatusMethodNotAllowed, "Method not allowed")
	}

	name := r.FormValue("name")
	directory := r.FormValue("directory")
	preCommand := r.FormValue("pre_command")

	if name == "" || directory == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Name and directory are required")
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
		return nil, newHTTPError(http.StatusNotFound, "Not found")
	}

	// Get the workspace by ID
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
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
		return nil, newHTTPError(http.StatusNotFound, "Not found")
	}

	// Get the workspace by ID
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
	}

	basePath := s.getBasePath(r)

	// Handle GET request - show edit form
	if r.Method == http.MethodGet {
		var buf bytes.Buffer
		err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.html", map[string]any{
			"BasePath": basePath,
			"Workspace": map[string]any{
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

	// Handle POST request - update workspace
	if r.Method == http.MethodPost {
		name := r.FormValue("name")
		directory := r.FormValue("directory")
		preCommand := r.FormValue("pre_command")

		if name == "" || directory == "" {
			var buf bytes.Buffer
			err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.html", map[string]any{
				"BasePath": basePath,
				"Workspace": map[string]any{
					"ID":         ws.ID,
					"Name":       ws.Name,
					"Directory":  ws.Directory,
					"PreCommand": ws.PreCommand,
				},
				"Error": "Workspace name and directory are required",
			})
			if err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}

		// Update the workspace
		_, err := workspace.UpdateWorkspace(s.stateDir, workspaceID, name, directory, preCommand)
		if err != nil {
			var buf bytes.Buffer
			err = s.tmpl.ExecuteTemplate(&buf, "edit-workspace.html", map[string]any{
				"BasePath": basePath,
				"Workspace": map[string]any{
					"ID":         ws.ID,
					"Name":       name,
					"Directory":  directory,
					"PreCommand": preCommand,
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

	return nil, newHTTPError(http.StatusMethodNotAllowed, "Method not allowed")
}

func (s *Server) handleWorkspaceClear(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, newHTTPError(http.StatusMethodNotAllowed, "Method not allowed")
	}

	// Redirect to home page
	basePath := s.getBasePath(r)
	return nil, &redirectError{url: basePath + "/", statusCode: http.StatusSeeOther}
}

func (s *Server) hxHandleExecute(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, newHTTPError(http.StatusMethodNotAllowed, "Method not allowed")
	}

	command := r.FormValue("command")
	if command == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Command is required")
	}

	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Workspace ID is required")
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
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

func (s *Server) jsonHandleProcessUpdates(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Workspace ID is required")
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
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
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

func (s *Server) hxHandleFinishedProcesses(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Workspace ID is required")
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
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
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
		return nil, newHTTPError(http.StatusBadRequest, "Process ID is required")
	}

	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Workspace ID is required")
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
	}

	// Get the process
	proc, ok := executor.GetProcess(s.stateDir, processID)
	if !ok {
		return nil, newHTTPError(http.StatusNotFound, "Process not found")
	}

	// Check for binary-data marker
	processDir := filepath.Dir(proc.OutputFile)
	binaryMarkerFile := filepath.Join(processDir, "binary-data")
	isBinary := false
	if _, err := os.Stat(binaryMarkerFile); err == nil {
		isBinary = true
	}

	// Read full output
	stdout, stderr, stdin, err := executor.ReadCombinedOutput(proc.OutputFile)
	if err != nil {
		stdout = ""
		stderr = ""
		stdin = ""
	}

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "process.html", map[string]interface{}{
		"Process":       proc,
		"Stdout":        stdout,
		"Stderr":        stderr,
		"Stdin":         stdin,
		"IsBinary":      isBinary,
		"BasePath":      s.getBasePath(r),
		"WorkspaceID":   workspaceID,
		"WorkspaceName": ws.Name,
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
		return nil, newHTTPError(http.StatusNotFound, "Process not found")
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
	stderr      string
	stdin       string
	needsExpand bool
	isBinary    bool
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
	stdout, stderr, stdin, err := executor.ReadCombinedOutput(outputFile)
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

	return processOutputData{
		stdout:      stdout,
		stderr:      stderr,
		stdin:       stdin,
		needsExpand: needsExpand,
		isBinary:    isBinary,
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
		"Stderr":      outputData.stderr,
		"Stdin":       outputData.stdin,
		"Type":        "combined",
		"NeedsExpand": outputData.needsExpand,
		"Expanded":    expand,
		"IsBinary":    outputData.isBinary,
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
		return nil, newHTTPError(http.StatusBadRequest, "Failed to parse form")
	}

	stdinData := r.FormValue("stdin")

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
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
		return nil, newHTTPError(http.StatusBadRequest, "Failed to parse form")
	}

	signalStr := r.FormValue("signal")
	if signalStr == "" {
		return nil, newHTTPError(http.StatusBadRequest, "No signal provided")
	}

	// Parse signal number
	var signalNum int
	_, err := fmt.Sscanf(signalStr, "%d", &signalNum)
	if err != nil {
		return nil, newHTTPError(http.StatusBadRequest, "Invalid signal number")
	}

	// Get signal name
	signalName := syscall.Signal(signalNum).String()

	// Get workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
	}

	// Get process to find PID
	proc, ok := executor.GetProcess(s.stateDir, processID)
	if !ok {
		return nil, newHTTPError(http.StatusNotFound, "Process not found")
	}

	if proc.PID == 0 {
		return nil, newHTTPError(http.StatusBadRequest, "Process has no PID")
	}

	// Send signal to process
	process, err := os.FindProcess(proc.PID)
	if err != nil {
		return nil, newHTTPError(http.StatusInternalServerError, "Failed to find process")
	}

	err = process.Signal(syscall.Signal(signalNum))
	if err != nil {
		slog.Error("Failed to send signal to process", "error", err, "pid", proc.PID, "signal", signalName)
		return nil, newHTTPError(http.StatusInternalServerError, "Failed to send signal")
	}

	// Log the signal send to output.log
	processDir := workspace.GetProcessDir(ws, processID)
	outputFile := filepath.Join(processDir, "output.log")

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	logLine := fmt.Sprintf("signal-sent %s: %d %s\n", timestamp, signalNum, signalName)

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
		return nil, newHTTPError(http.StatusBadRequest, "Process ID is required")
	}

	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Workspace ID is required")
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
	}

	// Get process directory
	processDir := workspace.GetProcessDir(ws, processID)
	outputFile := filepath.Join(processDir, "output.log")

	// Read raw stdout bytes
	stdoutBytes, err := executor.ReadRawStdout(outputFile)
	if err != nil {
		return nil, newHTTPError(http.StatusInternalServerError, "Failed to read output")
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
// Allow all origins for now (TODO: make this configurable)
return true
},
}

// handleTerminal shows the interactive terminal page
func (s *Server) handleTerminal(ctx context.Context, r *http.Request) ([]byte, error) {
workspaceID := r.PathValue("id")
processID := r.PathValue("processID")

// Get workspace
ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
if err != nil {
return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
}

// Get process
proc, found := executor.GetProcess(s.stateDir, processID)
if !found {
return nil, newHTTPError(http.StatusNotFound, "Process not found")
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
return nil, newHTTPError(http.StatusBadRequest, "Failed to parse form")
}

command := r.FormValue("command")
if command == "" {
return nil, newHTTPError(http.StatusBadRequest, "Command is required")
}

// Get workspace
ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
if err != nil {
return nil, newHTTPError(http.StatusNotFound, "Workspace not found")
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
