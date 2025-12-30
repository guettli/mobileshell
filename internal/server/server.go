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
	"net/http"
	"os"
	"path/filepath"
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
	auth     *auth.Auth
	executor *executor.Executor
	tmpl     *template.Template
}

func New(auth *auth.Auth, executor *executor.Executor) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		auth:     auth,
		executor: executor,
		tmpl:     tmpl,
	}

	return s, nil
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
				if cre.hxRedirect != "" {
					w.Header().Set("HX-Redirect", cre.hxRedirect)
					w.WriteHeader(http.StatusOK)
					return
				}
				if cre.redirect != "" {
					http.Redirect(w, r, cre.redirect, cre.statusCode)
					return
				}
				return
			}
			if cte, ok := err.(*contentTypeError); ok {
				w.Header().Set("Content-Type", cte.contentType)
				_, _ = w.Write(cte.data)
				return
			}
			// Check if it's an httpError with status code
			if he, ok := err.(*httpError); ok {
				http.Error(w, he.message, he.statusCode)
				return
			}
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
	hxRedirect string
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

func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("/", s.wrapHandler(s.handleIndex))
	mux.HandleFunc("/login", s.wrapHandler(s.handleLogin))
	mux.HandleFunc("/logout", s.wrapHandler(s.handleLogout))
	mux.HandleFunc("/dashboard", s.authMiddleware(s.wrapHandler(s.handleDashboard)))
	mux.HandleFunc("/execute", s.authMiddleware(s.wrapHandler(s.handleExecute)))
	mux.HandleFunc("/processes", s.authMiddleware(s.wrapHandler(s.handleProcesses)))
	mux.HandleFunc("/output/", s.authMiddleware(s.wrapHandler(s.handleOutput)))

	return mux
}

func (s *Server) handleIndex(ctx context.Context, r *http.Request) ([]byte, error) {
	token := s.getSessionToken(r)
	if token != "" {
		valid, err := s.auth.ValidateSession(token)
		if err != nil {
			return nil, fmt.Errorf("failed to validate session: %w", err)
		}
		if valid {
			basePath := s.getBasePath(r)
			// Return redirect as a special marker (we'll handle this in wrapHandler)
			return nil, &redirectError{url: basePath + "/dashboard", statusCode: http.StatusSeeOther}
		}
	}

	var buf bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&buf, "login.html", map[string]interface{}{
		"BasePath": s.getBasePath(r),
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) handleLogin(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, newHTTPError(http.StatusMethodNotAllowed, "Method not allowed")
	}

	password := r.FormValue("password")
	token, ok := s.auth.Authenticate(ctx, password)
	basePath := s.getBasePath(r)

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

	// Return cookie and HX-Redirect header
	return nil, &cookieRedirectError{
		cookie: &http.Cookie{
			Name:     "session",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			MaxAge:   86400, // 24 hours
		},
		hxRedirect: basePath + "/dashboard",
	}
}

func (s *Server) handleLogout(ctx context.Context, r *http.Request) ([]byte, error) {
	basePath := s.getBasePath(r)
	redirectPath := basePath + "/"
	if redirectPath == "/" {
		redirectPath = "/"
	}

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

func (s *Server) handleDashboard(ctx context.Context, r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	err := s.tmpl.ExecuteTemplate(&buf, "dashboard.html", map[string]interface{}{
		"BasePath": s.getBasePath(r),
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) handleExecute(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, newHTTPError(http.StatusMethodNotAllowed, "Method not allowed")
	}

	command := r.FormValue("command")
	if command == "" {
		return nil, newHTTPError(http.StatusBadRequest, "Command is required")
	}

	proc, err := s.executor.Execute(command)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(proc)
	if err != nil {
		return nil, err
	}

	return data, &contentTypeError{
		contentType: "application/json",
		data:        data,
	}
}

func (s *Server) handleProcesses(ctx context.Context, r *http.Request) ([]byte, error) {
	processes := s.executor.ListProcesses()

	if r.Header.Get("HX-Request") == "true" {
		var buf bytes.Buffer
		err := s.tmpl.ExecuteTemplate(&buf, "processes.html", map[string]interface{}{
			"Processes": processes,
			"BasePath":  s.getBasePath(r),
		})
		if err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	data, err := json.Marshal(processes)
	if err != nil {
		return nil, err
	}

	return data, &contentTypeError{
		contentType: "application/json",
		data:        data,
	}
}

func (s *Server) handleOutput(ctx context.Context, r *http.Request) ([]byte, error) {
	id := r.URL.Path[len("/output/"):]

	proc, ok := s.executor.GetProcess(id)
	if !ok {
		return nil, newHTTPError(http.StatusNotFound, "Process not found")
	}

	outputType := r.URL.Query().Get("type")
	var content string
	var err error

	if outputType == "stderr" {
		content, err = s.executor.ReadOutput(proc.StderrFile)
	} else {
		content, err = s.executor.ReadOutput(proc.StdoutFile)
	}

	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&buf, "output.html", map[string]interface{}{
		"Process":  proc,
		"Content":  content,
		"Type":     outputType,
		"BasePath": s.getBasePath(r),
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := s.getSessionToken(r)
		valid := false
		if token != "" {
			var err error
			valid, err = s.auth.ValidateSession(token)
			if err != nil {
				slog.Error("Failed to validate session", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
		}
		if !valid {
			basePath := s.getBasePath(r)
			redirectPath := basePath + "/"
			if redirectPath == "/" {
				redirectPath = "/"
			}
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
			s.auth.CleanExpiredSessions()
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
				if err := os.MkdirAll(stateDir, 0700); err != nil {
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

	outputDir := filepath.Join(stateDir, "outputs")

	authSvc, err := auth.New(stateDir)
	if err != nil {
		return fmt.Errorf("failed to create auth service: %w", err)
	}

	exec, err := executor.New(outputDir)
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	srv, err := New(authSvc, exec)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	addr := fmt.Sprintf("localhost:%s", port)
	return srv.Start(addr)
}
