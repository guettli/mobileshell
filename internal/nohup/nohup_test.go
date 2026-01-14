package nohup

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/executor"
	"mobileshell/internal/process"
	"mobileshell/internal/workspace"

	"github.com/stretchr/testify/require"
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
	proc, err := executor.Execute(ws, "echo 'Hello, World!'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	// Verify PID file was created
	processDir := workspace.GetProcessDir(ws, proc.CommandId)
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
	proc, err = process.LoadProcessFromDir(proc.ProcessDir)
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
	proc, err := executor.Execute(ws, "echo $TEST_VAR")
	require.NoError(t, err)

	// Verify output.log contains the environment variable value
	outputFile := filepath.Join(proc.ProcessDir, "output.log")
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
	proc, err := executor.Execute(ws, "exit 42")
	require.NoError(t, err)
	exitStatusFile := filepath.Join(proc.ProcessDir, "exit-status")
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
	proc, err = process.LoadProcessFromDir(proc.ProcessDir)
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
	err := os.WriteFile(testFile, []byte("test content"), 0o644)
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
	proc, err := executor.Execute(ws, "cat test.txt")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	// Give it a moment to complete
	time.Sleep(100 * time.Millisecond)

	// Verify output.log contains the file content
	outputFile := filepath.Join(proc.ProcessDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output.log: %v", err)
	}

	output := string(outputData)
	if !contains(output, "stdout") || !contains(output, "test content") {
		t.Errorf("Expected output to contain 'stdout' and 'test content', got '%s'", output)
	}
}
