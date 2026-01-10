package nohup

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/workspace"
)

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestNohupRun(t *testing.T) {
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

	// Create a process
	hash, err := workspace.CreateProcess(ws, "echo 'Hello, World!'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command
	err = Run(tmpDir, workspaceTS, hash, []string{})
	if err != nil {
		t.Fatalf("Failed to run nohup: %v", err)
	}

	// Verify PID file was created
	processDir := workspace.GetProcessDir(ws, hash)
	pidFile := filepath.Join(processDir, "pid")
	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Errorf("PID file does not exist: %s", pidFile)
	}

	// Verify exit-status file was created
	exitStatusFile := filepath.Join(processDir, "exit-status")
	if _, err := os.Stat(exitStatusFile); os.IsNotExist(err) {
		t.Errorf("Exit status file does not exist: %s", exitStatusFile)
	}

	// Read exit status
	exitStatusData, err := os.ReadFile(exitStatusFile)
	if err != nil {
		t.Fatalf("Failed to read exit status: %v", err)
	}
	exitCode, err := strconv.Atoi(string(exitStatusData))
	if err != nil {
		t.Fatalf("Failed to parse exit status: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}

	// Verify output.log contains expected output with STDOUT prefix
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	// Output should contain "stdout" prefix and timestamp in ISO8601 format
	output := string(outputData)
	if !contains(output, "stdout") || !contains(output, "Hello, World!") {
		t.Errorf("Expected output to contain 'stdout' and 'Hello, World!', got '%s'", output)
	}

	// Verify process metadata was updated
	proc, err := workspace.GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}

	if !proc.Completed {
		t.Error("Expected process to be completed")
	}

	if proc.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", proc.ExitCode)
	}

	if proc.PID == 0 {
		t.Error("PID should be set")
	}

	if proc.EndTime.IsZero() {
		t.Error("End time should be set")
	}
}

func TestNohupRunWithPreCommand(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize workspace storage
	if err := workspace.InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace with pre-command
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "export TEST_VAR=hello")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a process that uses the environment variable
	hash, err := workspace.CreateProcess(ws, "echo $TEST_VAR")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command
	err = Run(tmpDir, workspaceTS, hash, []string{})
	if err != nil {
		t.Fatalf("Failed to run nohup: %v", err)
	}

	// Verify output.log contains the environment variable value
	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	output := string(outputData)
	if !contains(output, "stdout") || !contains(output, "hello") {
		t.Errorf("Expected output to contain 'stdout' and 'hello', got '%s'", output)
	}
}

func TestNohupRunWithFailingCommand(t *testing.T) {
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

	// Create a process that will fail
	hash, err := workspace.CreateProcess(ws, "exit 42")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command
	err = Run(tmpDir, workspaceTS, hash, []string{})
	if err != nil {
		t.Fatalf("Failed to run nohup: %v", err)
	}

	// Verify exit status
	processDir := workspace.GetProcessDir(ws, hash)
	exitStatusFile := filepath.Join(processDir, "exit-status")
	exitStatusData, err := os.ReadFile(exitStatusFile)
	if err != nil {
		t.Fatalf("Failed to read exit status: %v", err)
	}
	exitCode, err := strconv.Atoi(string(exitStatusData))
	if err != nil {
		t.Fatalf("Failed to parse exit status: %v", err)
	}
	if exitCode != 42 {
		t.Errorf("Expected exit code 42, got %d", exitCode)
	}

	// Verify process metadata
	proc, err := workspace.GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}

	if !proc.Completed {
		t.Error("Expected process to be completed")
	}

	if proc.ExitCode != 42 {
		t.Errorf("Expected exit code 42, got %d", proc.ExitCode)
	}
}

func TestNohupRunWithWorkingDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file in tmpDir
	testFile := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(testFile, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Initialize workspace storage
	if err := workspace.InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace with specific directory
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a process that reads the file
	hash, err := workspace.CreateProcess(ws, "cat test.txt")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command
	err = Run(tmpDir, workspaceTS, hash, []string{})
	if err != nil {
		t.Fatalf("Failed to run nohup: %v", err)
	}

	// Give it a moment to complete
	time.Sleep(100 * time.Millisecond)

	// Verify output.log contains the file content
	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	output := string(outputData)
	if !contains(output, "stdout") || !contains(output, "test content") {
		t.Errorf("Expected output to contain 'stdout' and 'test content', got '%s'", output)
	}
}

func TestNohupRunWithStderrOutput(t *testing.T) {
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

	// Create a process that writes to both stdout and stderr
	// Use sh -c with explicit sleep to ensure outputs are flushed separately and timing is more predictable
	// The sleep gives time for readers to be ready and outputs to be captured
	hash, err := workspace.CreateProcess(ws, "sh -c 'echo stdout message; sleep 0.1; echo stderr message >&2; sleep 0.1'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command in background to avoid blocking
	done := make(chan error, 1)
	go func() {
		done <- Run(tmpDir, workspaceTS, hash, []string{})
	}()

	// Poll for expected output with timeout
	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")

	timeout := time.After(5 * time.Second) // Increased timeout for CI
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var output string
	foundStdout := false
	foundStderr := false

	for {
		select {
		case err := <-done:
			// Process completed, do one final check
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			// Give a moment for final output to be written
			time.Sleep(50 * time.Millisecond)

			outputData, readErr := os.ReadFile(outputFile)
			if readErr != nil {
				t.Fatalf("Failed to read output.log after completion: %v", readErr)
			}
			output = string(outputData)

			hasStdout := contains(output, "stdout") && contains(output, "stdout message")
			hasStderr := contains(output, "stderr") && contains(output, "stderr message")

			if hasStdout && hasStderr {
				return // Success
			}

			t.Fatalf("Process completed but missing output. Has stdout: %v, Has stderr: %v. Output: '%s'", hasStdout, hasStderr, output)

		case <-timeout:
			t.Fatalf("Timeout waiting for output. Has stdout: %v, Has stderr: %v. Got: '%s'", foundStdout, foundStderr, output)

		case <-ticker.C:
			outputData, err := os.ReadFile(outputFile)
			if err != nil {
				continue // File might not exist yet
			}
			output = string(outputData)

			// Check if we have all expected output
			hasStdout := contains(output, "stdout") && contains(output, "stdout message")
			hasStderr := contains(output, "stderr") && contains(output, "stderr message")

			// Track what we've found for better error messages
			if hasStdout {
				foundStdout = true
			}
			if hasStderr {
				foundStderr = true
			}

			if hasStdout && hasStderr {
				// Success! Found all expected output
				// Wait for Run to complete
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("Run returned error: %v", err)
					}
				case <-time.After(1 * time.Second):
					t.Fatal("Run did not complete after output was found")
				}
				return
			}
		}
	}
}

func TestNohupRunWithStdin(t *testing.T) {
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

	// Create a cat process
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
		done <- Run(tmpDir, workspaceTS, hash, []string{})
	}()

	// Wait for process to start
	time.Sleep(500 * time.Millisecond)

	// Send first input
	func() {
		file, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
		if err != nil {
			t.Logf("Failed to open pipe for writing: %v", err)
			return
		}
		defer func() { _ = file.Close() }()
		_, _ = file.WriteString("foo1\n")
	}()

	// Wait a bit for output
	time.Sleep(200 * time.Millisecond)

	// Send second input
	func() {
		file, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
		if err != nil {
			t.Logf("Failed to open pipe for writing: %v", err)
			return
		}
		defer func() { _ = file.Close() }()
		_, _ = file.WriteString("foo2\n")
	}()

	// Wait a bit more
	time.Sleep(200 * time.Millisecond)

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

	// Verify output.log contains both inputs
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	output := string(outputData)
	t.Logf("Output: %s", output)
	if !contains(output, "foo1") {
		t.Errorf("Expected output to contain 'foo1', got '%s'", output)
	}
	if !contains(output, "foo2") {
		t.Errorf("Expected output to contain 'foo2', got '%s'", output)
	}
}
