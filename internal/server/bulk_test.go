package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mobileshell/internal/auth"
	"mobileshell/internal/executor"
	"mobileshell/internal/workspace"
)

func TestSearchAndBulkActions(t *testing.T) {
	// Create a temporary directory for state
	stateDir := t.TempDir()

	// Initialize auth
	if err := auth.InitAuth(stateDir); err != nil {
		t.Fatalf("Failed to init auth: %v", err)
	}

	// Initialize executor
	if err := executor.InitExecutor(stateDir); err != nil {
		t.Fatalf("Failed to initialize executor: %v", err)
	}

	// Create server instance
	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Create a workspace
	ws, err := executor.CreateWorkspace(stateDir, "test-ws", stateDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create processes with different commands
	proc1Hash, _ := workspace.CreateProcess(ws, "python script.py")
	proc2Hash, _ := workspace.CreateProcess(ws, "go run main.go")
	proc3Hash, _ := workspace.CreateProcess(ws, "grep pattern file.txt")

	// Helper to mark process as completed
	completeProcess := func(hash string) {
		processDir := workspace.GetProcessDir(ws, hash)
		os.WriteFile(filepath.Join(processDir, "completed"), []byte("true"), 0600)
		os.WriteFile(filepath.Join(processDir, "exit-status"), []byte("0"), 0600)
	}

	completeProcess(proc1Hash)
	completeProcess(proc2Hash)
	completeProcess(proc3Hash)

	// Authenticate
	password := "a-very-long-password-that-meets-minimum-length-requirements"
	auth.AddPassword(stateDir, password)
	token, _ := auth.Authenticate(context.Background(), stateDir, password)
	cookie := &http.Cookie{Name: "session", Value: token}

	// Test 1: Search Filtering
	t.Run("Search Filtering", func(t *testing.T) {
		tests := []struct {
			query    string
			expected int // number of processes expected
		}{
			{"python", 1},
			{"go", 1},
			{"script", 1},
			{"nonexistent", 0},
			{"", 3},
		}

		for _, tt := range tests {
			req := httptest.NewRequest("GET", "/workspaces/"+ws.ID+"/hx-finished-processes?search="+tt.query, nil)
			req.AddCookie(cookie)
			w := httptest.NewRecorder()
			
			// We need to use the router to handle path parameters properly
			handler := srv.SetupRoutes()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Search '%s' failed with status %d", tt.query, w.Code)
			}

			// Count occurrences of "process-card" in HTML to verify count
			count := strings.Count(w.Body.String(), "process-card")
			if count != tt.expected {
				t.Errorf("Search '%s': expected %d processes, got %d", tt.query, tt.expected, count)
			}
		}
	})

	// Test 2: Bulk Signal
	t.Run("Bulk Signal", func(t *testing.T) {
		// Create real sleep processes to have valid PIDs
		cmd1 := exec.Command("sleep", "10")
		if err := cmd1.Start(); err != nil {
			t.Fatalf("Failed to start sleep process: %v", err)
		}
		defer cmd1.Process.Kill()

		cmd2 := exec.Command("sleep", "10")
		if err := cmd2.Start(); err != nil {
			t.Fatalf("Failed to start sleep process: %v", err)
		}
		defer cmd2.Process.Kill()

		workspace.UpdateProcessPID(ws, proc1Hash, cmd1.Process.Pid)
		workspace.UpdateProcessPID(ws, proc2Hash, cmd2.Process.Pid)

		form := url.Values{}
		form.Add("signal", "15") // SIGTERM
		form.Add("process_ids", proc1Hash)
		form.Add("process_ids", proc2Hash)

		req := httptest.NewRequest("POST", "/workspaces/"+ws.ID+"/hx-bulk-signal", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		w := httptest.NewRecorder()

		handler := srv.SetupRoutes()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Bulk signal failed with status %d", w.Code)
		}

		if !strings.Contains(w.Body.String(), "Signal terminated sent to 2 processes") {
			t.Errorf("Response should contain success message for 2 processes, got: %s", w.Body.String())
		}
	})
}
