package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/auth"
	"mobileshell/internal/executor"
	"mobileshell/internal/process"
	"mobileshell/pkg/httperror"
	"mobileshell/pkg/outputlog"
)

func TestTemplateRendering(t *testing.T) {
	// Create a temporary directory for state
	stateDir := t.TempDir()

	// Create server instance
	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Test case 1: Process with zero exit code
	proc1 := &process.Process{
		CommandId: "test1",
		Command:   "echo hello",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Completed: true,
		PID:       12345,
		ExitCode:  0,
	}

	// Test case 2: Process with non-zero exit code
	proc2 := &process.Process{
		CommandId: "test2",
		Command:   "false",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Completed: true,
		PID:       12346,
		ExitCode:  1,
	}

	// Test case 3: Process still running
	proc3 := &process.Process{
		CommandId: "test3",
		Command:   "sleep 100",
		StartTime: time.Now().UTC(),
		Completed: false,
		PID:       12347,
		ExitCode:  0,
	}

	// Test case 4: Process terminated by signal
	proc4 := &process.Process{
		CommandId: "test4",
		Command:   "sleep 100",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Completed: true,
		PID:       12348,
		ExitCode:  137, // 128 + 9 (SIGKILL)
		Signal:    "killed",
	}

	// Test case 5: Process terminated by SIGTERM
	proc5 := &process.Process{
		CommandId: "test5",
		Command:   "sleep 100",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Completed: true,
		PID:       12349,
		ExitCode:  143, // 128 + 15 (SIGTERM)
		Signal:    "terminated",
	}

	testCases := []struct {
		name      string
		processes []*process.Process
		wantError bool
	}{
		{
			name:      "Process with exit code 0",
			processes: []*process.Process{proc1},
			wantError: false,
		},
		{
			name:      "Process with exit code 1",
			processes: []*process.Process{proc2},
			wantError: false,
		},
		{
			name:      "Process with nil exit code",
			processes: []*process.Process{proc3},
			wantError: false,
		},
		{
			name:      "Process terminated by SIGKILL",
			processes: []*process.Process{proc4},
			wantError: false,
		},
		{
			name:      "Process terminated by SIGTERM",
			processes: []*process.Process{proc5},
			wantError: false,
		},
		{
			name:      "Multiple processes",
			processes: []*process.Process{proc1, proc2, proc3, proc4, proc5},
			wantError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := srv.tmpl.ExecuteTemplate(&buf, "hx-finished-processes-initial.html", map[string]interface{}{
				"FinishedProcesses": tc.processes,
				"HasMore":           false,
				"Offset":            10,
				"BasePath":          "",
				"WorkspaceID":       "test",
			})

			if tc.wantError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tc.wantError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Check that output was generated
			if !tc.wantError && buf.Len() == 0 {
				t.Error("Expected output but got empty buffer")
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		start    time.Time
		end      time.Time
		expected string
	}{
		{
			name:     "zero end time",
			start:    now,
			end:      time.Time{},
			expected: "",
		},
		{
			name:     "less than 1 second",
			start:    now,
			end:      now.Add(500 * time.Millisecond),
			expected: "",
		},
		{
			name:     "exactly 1 second",
			start:    now,
			end:      now.Add(1 * time.Second),
			expected: "1s",
		},
		{
			name:     "under 1 minute",
			start:    now,
			end:      now.Add(45 * time.Second),
			expected: "45s",
		},
		{
			name:     "exactly 1 minute",
			start:    now,
			end:      now.Add(60 * time.Second),
			expected: "1m",
		},
		{
			name:     "minutes and seconds",
			start:    now,
			end:      now.Add(125 * time.Second),
			expected: "2m 5s",
		},
		{
			name:     "exactly 1 hour",
			start:    now,
			end:      now.Add(3600 * time.Second),
			expected: "1h",
		},
		{
			name:     "hours and minutes",
			start:    now,
			end:      now.Add(3665 * time.Second),
			expected: "1h 1m",
		},
		{
			name:     "large duration",
			start:    now,
			end:      now.Add(7384 * time.Second),
			expected: "2h 3m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.start, tt.end)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestGetBasePath(t *testing.T) {
	stateDir := t.TempDir()

	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{
			name:     "no header",
			header:   "",
			expected: "",
		},
		{
			name:     "with header",
			header:   "/api/v1",
			expected: "/api/v1",
		},
		{
			name:     "with trailing slash",
			header:   "/api/v1/",
			expected: "/api/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("X-Forwarded-Prefix", tt.header)
			}

			result := srv.getBasePath(req)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestGetSessionToken(t *testing.T) {
	stateDir := t.TempDir()

	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	tests := []struct {
		name          string
		cookieValue   string
		expectedToken string
	}{
		{
			name:          "token in cookie",
			cookieValue:   "test-token-123",
			expectedToken: "test-token-123",
		},
		{
			name:          "no cookie",
			cookieValue:   "",
			expectedToken: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)

			if tt.cookieValue != "" {
				req.AddCookie(&http.Cookie{
					Name:  "session",
					Value: tt.cookieValue,
				})
			}

			token := srv.getSessionToken(req)
			if token != tt.expectedToken {
				t.Errorf("Expected token '%s', got '%s'", tt.expectedToken, token)
			}
		})
	}
}

func TestGetStateDir(t *testing.T) {
	tests := []struct {
		name     string
		stateDir string
		create   bool
		hasError bool
	}{
		{
			name:     "default state dir",
			stateDir: "",
			create:   true,
			hasError: false,
		},
		{
			name:     "custom state dir",
			stateDir: "/tmp/custom-state-dir-test",
			create:   true,
			hasError: false,
		},
		{
			name:     "non-existent dir without create",
			stateDir: "/tmp/non-existent-test-dir",
			create:   false,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up if using custom dir
			if tt.stateDir != "" && tt.create {
				defer func() {
					_ = os.RemoveAll(tt.stateDir)
				}()
			}

			result, err := GetStateDir(tt.stateDir, tt.create)
			if tt.hasError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.hasError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.hasError {
				if result == "" {
					t.Error("State dir should not be empty")
				}
			}
		})
	}
}

func TestAuthMiddleware(t *testing.T) {
	stateDir := t.TempDir()

	// Initialize auth
	err := auth.InitAuth(stateDir)
	if err != nil {
		t.Fatalf("Failed to init auth: %v", err)
	}

	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Add a test password and create session
	password := "a-very-long-password-that-meets-minimum-length-requirements"
	err = auth.AddPassword(stateDir, password)
	if err != nil {
		t.Fatalf("Failed to add password: %v", err)
	}

	validToken, success := auth.Authenticate(t.Context(), stateDir, password)
	if !success {
		t.Fatal("Failed to authenticate")
	}

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})

	tests := []struct {
		name           string
		token          string
		expectedStatus int
	}{
		{
			name:           "valid token",
			token:          validToken,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "invalid token",
			token:          "invalid-token",
			expectedStatus: http.StatusSeeOther, // 303 redirect to login
		},
		{
			name:           "no token",
			token:          "",
			expectedStatus: http.StatusSeeOther, // 303 redirect to login
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.token != "" {
				req.AddCookie(&http.Cookie{
					Name:  "session",
					Value: tt.token,
				})
			}

			rec := httptest.NewRecorder()
			handler := srv.authMiddleware(testHandler)
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			if tt.expectedStatus == http.StatusOK {
				body := rec.Body.String()
				if body != "success" {
					t.Errorf("Expected body 'success', got '%s'", body)
				}
			}
		})
	}
}

func TestHandleLogin(t *testing.T) {
	stateDir := t.TempDir()

	// Initialize auth
	err := auth.InitAuth(stateDir)
	if err != nil {
		t.Fatalf("Failed to init auth: %v", err)
	}

	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Add a test password
	password := "a-very-long-password-that-meets-minimum-length-requirements"
	err = auth.AddPassword(stateDir, password)
	if err != nil {
		t.Fatalf("Failed to add password: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		password       string
		expectedStatus int
	}{
		{
			name:           "GET request shows login page",
			method:         "GET",
			password:       "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "POST with valid password",
			method:         "POST",
			password:       password,
			expectedStatus: http.StatusSeeOther, // 303 redirect after login
		},
		{
			name:           "POST with invalid password",
			method:         "POST",
			password:       "invalid-password-that-is-long-enough",
			expectedStatus: http.StatusOK, // Shows login page with error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.method == "POST" {
				body := strings.NewReader("password=" + tt.password)
				req = httptest.NewRequest("POST", "/login", body)
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				req = httptest.NewRequest("GET", "/login", nil)
			}

			// Use the wrapHandler to test via HTTP
			handler := srv.wrapHandler(srv.handleLogin)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

func TestSetupRoutes(t *testing.T) {
	stateDir := t.TempDir()

	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// SetupRoutes should not panic and return a valid handler
	handler := srv.SetupRoutes()
	if handler == nil {
		t.Error("Handler should not be nil after SetupRoutes")
	}
}

func TestErrorPageRendering(t *testing.T) {
	stateDir := t.TempDir()

	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	tests := []struct {
		name       string
		statusCode int
		message    string
		wantTitle  string
	}{
		{
			name:       "404 Not Found",
			statusCode: http.StatusNotFound,
			message:    "Workspace not found",
			wantTitle:  "Not Found",
		},
		{
			name:       "400 Bad Request",
			statusCode: http.StatusBadRequest,
			message:    "Invalid request",
			wantTitle:  "Bad Request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a request to a non-existent workspace
			req := httptest.NewRequest("GET", "/workspaces/nonexistent/hx-finished-processes", nil)
			w := httptest.NewRecorder()

			// Create a handler that returns the HTTPError
			handler := srv.wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
				return nil, httperror.HTTPError{StatusCode: tt.statusCode, Message: tt.message}
			})

			handler(w, req)

			// Check status code
			if w.Code != tt.statusCode {
				t.Errorf("Expected status code %d, got %d", tt.statusCode, w.Code)
			}

			// Check that the response contains the error template elements
			body := w.Body.String()

			if !strings.Contains(body, tt.wantTitle) {
				t.Errorf("Response should contain title %q", tt.wantTitle)
			}

			if !strings.Contains(body, tt.message) {
				t.Errorf("Response should contain message %q", tt.message)
			}

			if !strings.Contains(body, "Go to Workspaces") {
				t.Error("Response should contain link to workspaces")
			}

			if !strings.Contains(body, "/workspaces") {
				t.Error("Response should contain link to /workspaces")
			}
		})
	}
}

func TestBinaryDownload(t *testing.T) {
	// Create a temporary directory for state
	stateDir := t.TempDir()

	// Initialize auth
	err := auth.InitAuth(stateDir)
	if err != nil {
		t.Fatalf("Failed to init auth: %v", err)
	}

	// Initialize executor
	err = executor.InitExecutor(stateDir)
	if err != nil {
		t.Fatalf("Failed to initialize executor: %v", err)
	}

	// Create a workspace
	ws, err := executor.CreateWorkspace(stateDir, "test-ws", stateDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a test process with binary data (all bytes from 0 to 255)
	binaryData := make([]byte, 256)
	for i := 0; i < 256; i++ {
		binaryData[i] = byte(i)
	}

	// Create a fake process by directly setting up the process directory structure
	// This avoids issues with running actual commands in the test environment
	proc, err := executor.Execute(ws, "test binary command")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}
	processDir := proc.ProcessDir
	// Create output.log file with binary data in stdout format
	// Use the same formatting function as nohup to ensure consistency
	var outputLog bytes.Buffer
	timestamp := time.Now().UTC()

	// Write each byte on its own line to simulate line-by-line output
	// This is how the actual process would output binary data
	for _, b := range binaryData {
		chunk := outputlog.Chunk{
			Stream:    "stdout",
			Timestamp: timestamp,
			Line:      []byte{b},
		}
		outputLog.Write(outputlog.FormatChunk(chunk))
	}

	outputFile := filepath.Join(processDir, "output.log")
	if err := os.WriteFile(outputFile, outputLog.Bytes(), 0o600); err != nil {
		t.Fatalf("Failed to write output.log: %v", err)
	}

	// Create binary-data marker file (simulating what nohup would do)
	binaryMarkerFile := filepath.Join(processDir, "binary-data")
	if err := os.WriteFile(binaryMarkerFile, []byte("true"), 0o600); err != nil {
		t.Fatalf("Failed to write binary-data marker: %v", err)
	}

	// Create server instance
	srv, err := New(stateDir)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Add a test password and create session
	password := "a-very-long-password-that-meets-minimum-length-requirements"
	err = auth.AddPassword(stateDir, password)
	if err != nil {
		t.Fatalf("Failed to add password: %v", err)
	}

	token, success := auth.Authenticate(context.Background(), stateDir, password)
	if !success {
		t.Fatal("Failed to authenticate")
	}

	// Create request
	req := httptest.NewRequest("GET", "/workspaces/"+ws.ID+"/processes/"+proc.CommandId+"/download", nil)
	req.AddCookie(&http.Cookie{
		Name:  "session",
		Value: token,
	})

	// Create response recorder
	w := httptest.NewRecorder()

	// Get the handler and serve the request
	handler := srv.SetupRoutes()
	handler.ServeHTTP(w, req)

	// Check status code
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Check Content-Type header is set
	contentType := w.Header().Get("Content-Type")
	if contentType == "" {
		t.Error("Content-Type header should be set")
	}

	// Check Content-Disposition header
	contentDisposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(contentDisposition, "attachment") {
		t.Errorf("Content-Disposition should contain 'attachment', got %q", contentDisposition)
	}

	// Check that the downloaded data contains all bytes 0-255
	downloadedData := w.Body.Bytes()

	if len(downloadedData) == 0 {
		t.Fatal("Downloaded data should not be empty")
	}

	// Verify that all bytes 0-255 are present in the downloaded data
	// Count occurrences of each byte value
	byteCounts := make(map[byte]int)
	for _, b := range downloadedData {
		byteCounts[b]++
	}

	// Check that we have all 256 unique byte values
	for i := 0; i < 256; i++ {
		if byteCounts[byte(i)] == 0 {
			t.Errorf("Missing byte value %d in downloaded data", i)
		}
	}

	// All 256 bytes should be present (byte 10 which is '\n' will appear multiple times)
	if len(byteCounts) != 256 {
		t.Errorf("Expected 256 unique byte values, got %d", len(byteCounts))
	}

	t.Logf("Successfully downloaded %d bytes with all 256 unique byte values (0-255) preserved", len(downloadedData))

	// Test that the process page shows binary data message instead of output
	processPageReq := httptest.NewRequest("GET", "/workspaces/"+ws.ID+"/processes/"+proc.CommandId, nil)
	processPageReq.AddCookie(&http.Cookie{
		Name:  "session",
		Value: token,
	})

	processPageW := httptest.NewRecorder()
	handler.ServeHTTP(processPageW, processPageReq)

	if processPageW.Code != http.StatusOK {
		t.Errorf("Expected status code %d for process page, got %d", http.StatusOK, processPageW.Code)
	}

	processPageBody := processPageW.Body.String()
	if !strings.Contains(processPageBody, "Binary data detected") {
		t.Error("Process page should contain 'Binary data detected' message")
	}
	if !strings.Contains(processPageBody, "Download Output") {
		t.Error("Process page should contain 'Download Output' button")
	}

	t.Logf("Successfully verified binary data message is shown on process page")
}

func TestServerLogCapture(t *testing.T) {
	// Create a temporary directory for state
	stateDir := t.TempDir()

	// Set up server logging
	logFile, err := setupServerLog(stateDir)
	if err != nil {
		t.Fatalf("Failed to setup server log: %v", err)
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			t.Errorf("Failed to close log file: %v", err)
		}
	}()

	// Write to log package
	log.Println("Test log message")

	// Write to slog package
	slog.Info("Test slog message")

	// Write directly to stdout and stderr
	_, _ = fmt.Fprintln(os.Stdout, "Direct stdout message")
	_, _ = fmt.Fprintln(os.Stderr, "Direct stderr message")

	// Give goroutines time to copy data
	time.Sleep(100 * time.Millisecond)

	// Read server.log
	logPath := filepath.Join(stateDir, "server.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read server.log: %v", err)
	}

	contentStr := string(content)
	t.Logf("server.log content:\n%s", contentStr)

	// Verify all messages are captured
	if !strings.Contains(contentStr, "Test log message") {
		t.Error("server.log should contain 'Test log message' from log package")
	}
	if !strings.Contains(contentStr, "Test slog message") {
		t.Error("server.log should contain 'Test slog message' from slog package")
	}
	if !strings.Contains(contentStr, "Direct stdout message") {
		t.Error("server.log should contain 'Direct stdout message' from stdout")
	}
	if !strings.Contains(contentStr, "Direct stderr message") {
		t.Error("server.log should contain 'Direct stderr message' from stderr")
	}

	if len(content) == 0 {
		t.Error("server.log should not be empty")
	}
}
