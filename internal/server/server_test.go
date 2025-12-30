package server

import (
	"bytes"
	"testing"
	"time"

	"mobileshell/internal/executor"
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
	exitCodeZero := 0
	proc1 := &executor.Process{
		ID:        "test1",
		Command:   "echo hello",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Status:    "completed",
		PID:       12345,
		ExitCode:  &exitCodeZero,
	}

	// Test case 2: Process with non-zero exit code
	exitCodeOne := 1
	proc2 := &executor.Process{
		ID:        "test2",
		Command:   "false",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Status:    "completed",
		PID:       12346,
		ExitCode:  &exitCodeOne,
	}

	// Test case 3: Process with nil exit code (still running)
	proc3 := &executor.Process{
		ID:        "test3",
		Command:   "sleep 100",
		StartTime: time.Now().UTC(),
		Status:    "running",
		PID:       12347,
		ExitCode:  nil,
	}

	// Test case 4: Process terminated by signal
	exitCodeSignal := 137 // 128 + 9 (SIGKILL)
	proc4 := &executor.Process{
		ID:        "test4",
		Command:   "sleep 100",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Status:    "completed",
		PID:       12348,
		ExitCode:  &exitCodeSignal,
		Signal:    "killed",
	}

	// Test case 5: Process terminated by SIGTERM
	exitCodeSigterm := 143 // 128 + 15 (SIGTERM)
	proc5 := &executor.Process{
		ID:        "test5",
		Command:   "sleep 100",
		StartTime: time.Now().UTC(),
		EndTime:   time.Now().UTC().Add(1 * time.Second),
		Status:    "completed",
		PID:       12349,
		ExitCode:  &exitCodeSigterm,
		Signal:    "terminated",
	}

	testCases := []struct {
		name      string
		processes []*executor.Process
		wantError bool
	}{
		{
			name:      "Process with exit code 0",
			processes: []*executor.Process{proc1},
			wantError: false,
		},
		{
			name:      "Process with exit code 1",
			processes: []*executor.Process{proc2},
			wantError: false,
		},
		{
			name:      "Process with nil exit code",
			processes: []*executor.Process{proc3},
			wantError: false,
		},
		{
			name:      "Process terminated by SIGKILL",
			processes: []*executor.Process{proc4},
			wantError: false,
		},
		{
			name:      "Process terminated by SIGTERM",
			processes: []*executor.Process{proc5},
			wantError: false,
		},
		{
			name:      "Multiple processes",
			processes: []*executor.Process{proc1, proc2, proc3, proc4, proc5},
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
