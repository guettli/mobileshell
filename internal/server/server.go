package server

import (
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
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

func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/dashboard", s.authMiddleware(s.handleDashboard))
	mux.HandleFunc("/execute", s.authMiddleware(s.handleExecute))
	mux.HandleFunc("/processes", s.authMiddleware(s.handleProcesses))
	mux.HandleFunc("/output/", s.authMiddleware(s.handleOutput))

	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	token := s.getSessionToken(r)
	if token != "" && s.auth.ValidateSession(token) {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	_ = s.tmpl.ExecuteTemplate(w, "login.html", nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	password := r.FormValue("password")
	token, ok := s.auth.Authenticate(password)

	if !ok {
		_ = s.tmpl.ExecuteTemplate(w, "login.html", map[string]string{"error": "Invalid password"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   86400, // 24 hours
	})

	w.Header().Set("HX-Redirect", "/dashboard")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	_ = s.tmpl.ExecuteTemplate(w, "dashboard.html", nil)
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	command := r.FormValue("command")
	if command == "" {
		http.Error(w, "Command is required", http.StatusBadRequest)
		return
	}

	proc, err := s.executor.Execute(command)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(proc)
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	processes := s.executor.ListProcesses()

	if r.Header.Get("HX-Request") == "true" {
		_ = s.tmpl.ExecuteTemplate(w, "processes.html", map[string]interface{}{
			"Processes": processes,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(processes)
}

func (s *Server) handleOutput(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/output/"):]

	proc, ok := s.executor.GetProcess(id)
	if !ok {
		http.Error(w, "Process not found", http.StatusNotFound)
		return
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = s.tmpl.ExecuteTemplate(w, "output.html", map[string]interface{}{
		"Process": proc,
		"Content": content,
		"Type":    outputType,
	})
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := s.getSessionToken(r)
		if token == "" || !s.auth.ValidateSession(token) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
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

func (s *Server) Start(addr string) error {
	// Clean expired sessions periodically
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		for range ticker.C {
			s.auth.CleanExpiredSessions()
		}
	}()

	log.Printf("Starting server on %s", addr)
	return http.ListenAndServe(addr, s.SetupRoutes())
}
