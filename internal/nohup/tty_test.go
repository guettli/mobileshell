package nohup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/workspace"
)

// TestTTYSupport verifies that commands can detect they're running in a TTY
// This is critical for commands like vim, less, top, etc. that check isatty()
func TestTTYSupport(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize workspace storage
	if err := workspace.InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Test 1: Verify that stdin is a TTY using the `test -t 0` command
	// This command returns exit code 0 if stdin is a TTY, 1 otherwise
	hash, err := workspace.CreateProcess(ws, "test -t 0 && echo 'stdin is a tty' || echo 'stdin is NOT a tty'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command
	err = Run(tmpDir, workspaceTS, hash)
	if err != nil {
		t.Fatalf("Failed to run nohup: %v", err)
	}

	// Read output
	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	output := string(outputData)
	t.Logf("Output: %s", output)

	// With PTY support, stdin should be detected as a TTY
	if !strings.Contains(output, "stdin is a tty") {
		t.Errorf("Expected 'stdin is a tty' in output, got: %s", output)
		t.Error("This indicates PTY support is not working correctly")
	}
}

// TestTTYEcho verifies that PTY echo is working (terminals normally echo input)
func TestTTYEcho(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize workspace storage
	if err := workspace.InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a cat process that will echo input
	hash, err := workspace.CreateProcess(ws, "cat")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)
	processDir := workspace.GetProcessDir(ws, hash)
	pipePath := filepath.Join(processDir, "stdin.pipe")

	// Start nohup in background
	done := make(chan error)
	go func() {
		done <- Run(tmpDir, workspaceTS, hash)
	}()

	// Wait for process to start by polling for PID file
	for i := 0; i < 20; i++ {
		proc, err := workspace.GetProcess(ws, hash)
		if err == nil && proc.PID > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Send input
	file, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
	if err != nil {
		t.Logf("Failed to open pipe for writing: %v", err)
	} else {
		defer func() { _ = file.Close() }()
		if _, err := file.WriteString("test input\n"); err != nil {
			t.Logf("Failed to write to pipe: %v", err)
		}
	}

	// Wait for output with polling
	outputFile := filepath.Join(processDir, "output.log")
	var outputData []byte
	for i := 0; i < 10; i++ {
		data, err := os.ReadFile(outputFile)
		if err == nil && len(data) > 0 {
			outputData = data
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Kill the cat process
	proc, err := workspace.GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}
	if proc.PID > 0 {
		p, err := os.FindProcess(proc.PID)
		if err == nil {
			_ = p.Kill()
		}
	}

	// Wait for nohup to finish
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Nohup finished with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Nohup did not finish in time")
	}

	// Verify output contains the echoed input
	if outputData == nil {
		outputData, _ = os.ReadFile(outputFile)
	}

	output := string(outputData)
	t.Logf("Output: %s", output)

	// With PTY, we should see the input echoed back
	if !strings.Contains(output, "test input") {
		t.Errorf("Expected 'test input' in output, got '%s'", output)
	}
}

// TestColorOutput verifies that ANSI color codes work with PTY
// Many commands detect TTY and only output colors when connected to a terminal
func TestColorOutput(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize workspace storage
	if err := workspace.InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Use printf to output ANSI color codes
	// Many tools like ls --color=auto check isatty() and only output colors with a TTY
	hash, err := workspace.CreateProcess(ws, "printf '\\033[31mRED TEXT\\033[0m\\n'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command
	err = Run(tmpDir, workspaceTS, hash)
	if err != nil {
		t.Fatalf("Failed to run nohup: %v", err)
	}

	// Read output
	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	output := string(outputData)
	t.Logf("Output: %s", output)

	// Verify ANSI escape codes are present (they should pass through with PTY)
	if !strings.Contains(output, "\033[31m") || !strings.Contains(output, "RED TEXT") {
		t.Errorf("Expected ANSI color codes in output, got: %s", output)
	}
}
