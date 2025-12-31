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

	if proc.Status != "completed" {
		t.Errorf("Expected status 'completed', got '%s'", proc.Status)
	}

	if proc.ExitCode == nil || *proc.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %v", proc.ExitCode)
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

	if proc.Status != "completed" {
		t.Errorf("Expected status 'completed', got '%s'", proc.Status)
	}

	if proc.ExitCode == nil || *proc.ExitCode != 42 {
		t.Errorf("Expected exit code 42, got %v", proc.ExitCode)
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
	hash, err := workspace.CreateProcess(ws, "echo 'stdout message' && echo 'stderr message' >&2")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	workspaceTS := filepath.Base(ws.Path)

	// Run the nohup command
	err = Run(tmpDir, workspaceTS, hash, []string{})
	if err != nil {
		t.Fatalf("Failed to run nohup: %v", err)
	}

	// Verify output.log contains both stdout and stderr with proper prefixes
	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	output := string(outputData)
	if !contains(output, "stdout") || !contains(output, "stdout message") {
		t.Errorf("Expected output to contain 'stdout' and 'stdout message', got '%s'", output)
	}
	if !contains(output, "stderr") || !contains(output, "stderr message") {
		t.Errorf("Expected output to contain 'stderr' and 'stderr message', got '%s'", output)
	}
}
