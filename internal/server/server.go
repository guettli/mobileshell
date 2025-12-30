package server

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mobileshell/internal/auth"
	"mobileshell/internal/executor"
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
		"deref":          derefInt,
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

// derefInt safely dereferences an *int, returning 0 if nil
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
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
			// Check if it's an httpError with status code
			if he, ok := err.(*httpError); ok {
				slog.Error("HTTP handler error",
					"method", r.Method,
					"path", r.URL.Path,
					"status", he.statusCode,
					"error", he.message)
				http.Error(w, he.message, he.statusCode)
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
	mux.HandleFunc("/workspaces/{id}/hx-execute", s.authMiddleware(s.wrapHandler(s.hxHandleExecute)))
	mux.HandleFunc("/workspaces/{id}/hx-running-processes", s.authMiddleware(s.wrapHandler(s.hxHandleRunningProcesses)))
	mux.HandleFunc("/workspaces/{id}/hx-finished-processes", s.authMiddleware(s.wrapHandler(s.hxHandleFinishedProcesses)))
	mux.HandleFunc("/workspaces/{id}/processes/{processID}/hx-output", s.authMiddleware(s.wrapHandler(s.hxHandleOutput)))

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
		return nil, fmt.Errorf("workspace not found: %w", err)
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
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	proc, err := executor.Execute(s.stateDir, ws, command)
	if err != nil {
		return nil, err
	}

	// Render the new process as HTML using the running processes template
	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "hx-running-processes.html", map[string]interface{}{
		"RunningProcesses": []*executor.Process{proc},
		"BasePath":         s.getBasePath(r),
		"WorkspaceID":      workspaceID,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) hxHandleRunningProcesses(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get workspace ID from path parameter
	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Workspace ID is required")
	}

	// Get the workspace
	ws, err := executor.GetWorkspaceByID(s.stateDir, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	allProcesses, err := executor.ListWorkspaceProcesses(ws)
	if err != nil {
		return nil, err
	}

	// Filter for running processes only
	var runningProcesses []*executor.Process
	for _, p := range allProcesses {
		if p.Status != "completed" {
			runningProcesses = append(runningProcesses, p)
		}
	}

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "hx-running-processes.html", map[string]interface{}{
		"RunningProcesses": runningProcesses,
		"BasePath":         s.getBasePath(r),
		"WorkspaceID":      workspaceID,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	allProcesses, err := executor.ListWorkspaceProcesses(ws)
	if err != nil {
		return nil, err
	}

	// Filter for finished processes only
	var finishedProcesses []*executor.Process
	for _, p := range allProcesses {
		if p.Status == "completed" {
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

func (s *Server) hxHandleOutput(ctx context.Context, r *http.Request) ([]byte, error) {
	// Get process ID from path parameter
	processID := r.PathValue("processID")

	proc, ok := executor.GetProcess(s.stateDir, processID)
	if !ok {
		return nil, newHTTPError(http.StatusNotFound, "Process not found")
	}

	outputType := r.URL.Query().Get("type")
	expand := r.URL.Query().Get("expand") == "true"

	var buf bytes.Buffer

	if outputType == "combined" {
		// Read both stdout and stderr
		stdout, errOut := executor.ReadOutput(proc.StdoutFile)
		if errOut != nil {
			stdout = ""
		}
		stderr, errErr := executor.ReadOutput(proc.StderrFile)
		if errErr != nil {
			stderr = ""
		}

		// Calculate total size and lines
		totalSize := len(stdout) + len(stderr)
		stdoutLines := strings.Split(stdout, "\n")
		stderrLines := strings.Split(stderr, "\n")
		totalLines := len(stdoutLines) + len(stderrLines)
		if stdout != "" && stdoutLines[len(stdoutLines)-1] == "" {
			totalLines--
		}
		if stderr != "" && stderrLines[len(stderrLines)-1] == "" {
			totalLines--
		}

		// Decide whether to show automatically
		autoShow := totalSize < 1000 && totalLines <= 5

		// Prepare preview (first 5 lines combined, up to 1000 bytes)
		var previewStdout, previewStderr string
		needsExpand := false

		if !autoShow && !expand {
			// Show preview
			previewStdout, previewStderr, needsExpand = truncateOutput(stdout, stderr, 5, 1000)
		} else if expand {
			// Show full output
			previewStdout = stdout
			previewStderr = stderr
		} else {
			// Auto-show (small enough)
			previewStdout = stdout
			previewStderr = stderr
		}

		err := s.tmpl.ExecuteTemplate(&buf, "hx-output.html", map[string]interface{}{
			"Process":      proc,
			"Stdout":       previewStdout,
			"Stderr":       previewStderr,
			"Type":         "combined",
			"NeedsExpand":  needsExpand,
			"Expanded":     expand,
			"BasePath":     s.getBasePath(r),
			"WorkspaceID":  filepath.Base(filepath.Dir(filepath.Dir(proc.StdoutFile))),
		})
		if err != nil {
			return nil, err
		}
	} else {
		// Legacy single output type
		var content string
		var err error

		if outputType == "stderr" {
			content, err = executor.ReadOutput(proc.StderrFile)
		} else {
			content, err = executor.ReadOutput(proc.StdoutFile)
		}

		if err != nil {
			return nil, err
		}

		err = s.tmpl.ExecuteTemplate(&buf, "hx-output.html", map[string]interface{}{
			"Process":  proc,
			"Content":  content,
			"Type":     outputType,
			"BasePath": s.getBasePath(r),
		})
		if err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

// truncateOutput returns the first maxLines lines (up to maxBytes total) from stdout and stderr
// Returns whether truncation occurred (needsExpand)
func truncateOutput(stdout, stderr string, maxLines, maxBytes int) (string, string, bool) {
	stdoutLines := strings.Split(stdout, "\n")
	stderrLines := strings.Split(stderr, "\n")

	// Remove trailing empty line if present
	if len(stdoutLines) > 0 && stdoutLines[len(stdoutLines)-1] == "" {
		stdoutLines = stdoutLines[:len(stdoutLines)-1]
	}
	if len(stderrLines) > 0 && stderrLines[len(stderrLines)-1] == "" {
		stderrLines = stderrLines[:len(stderrLines)-1]
	}

	totalLines := len(stdoutLines) + len(stderrLines)
	needsExpand := totalLines > maxLines || len(stdout)+len(stderr) > maxBytes

	if !needsExpand {
		return stdout, stderr, false
	}

	// Truncate to first maxLines
	var resultStdout, resultStderr string
	linesLeft := maxLines
	bytesLeft := maxBytes

	// First, try to include stdout lines
	for i := 0; i < len(stdoutLines) && linesLeft > 0 && bytesLeft > 0; i++ {
		line := stdoutLines[i]
		if i > 0 {
			line = "\n" + line
		}
		if len(line) <= bytesLeft {
			resultStdout += line
			linesLeft--
			bytesLeft -= len(line)
		} else {
			break
		}
	}

	// Then include stderr lines
	for i := 0; i < len(stderrLines) && linesLeft > 0 && bytesLeft > 0; i++ {
		line := stderrLines[i]
		if i > 0 {
			line = "\n" + line
		}
		if len(line) <= bytesLeft {
			resultStderr += line
			linesLeft--
			bytesLeft -= len(line)
		} else {
			break
		}
	}

	return resultStdout, resultStderr, true
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := s.getSessionToken(r)
		valid := false
		if token != "" {
			var err error
			valid, err = auth.ValidateSession(s.stateDir, token)
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
