package nohup

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"mobileshell/internal/workspace"
)

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

	// Verify stdout contains expected output
	stdoutFile := filepath.Join(processDir, "stdout")
	stdoutData, err := os.ReadFile(stdoutFile)
	if err != nil {
		t.Fatalf("Failed to read stdout: %v", err)
	}
	if string(stdoutData) != "Hello, World!\n" {
		t.Errorf("Expected stdout 'Hello, World!\\n', got '%s'", string(stdoutData))
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

	// Verify stdout contains the environment variable value
	processDir := workspace.GetProcessDir(ws, hash)
	stdoutFile := filepath.Join(processDir, "stdout")
	stdoutData, err := os.ReadFile(stdoutFile)
	if err != nil {
		t.Fatalf("Failed to read stdout: %v", err)
	}
	if string(stdoutData) != "hello\n" {
		t.Errorf("Expected stdout 'hello\\n', got '%s'", string(stdoutData))
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

	// Verify stdout contains the file content
	processDir := workspace.GetProcessDir(ws, hash)
	stdoutFile := filepath.Join(processDir, "stdout")
	stdoutData, err := os.ReadFile(stdoutFile)
	if err != nil {
		t.Fatalf("Failed to read stdout: %v", err)
	}
	if string(stdoutData) != "test content" {
		t.Errorf("Expected stdout 'test content', got '%s'", string(stdoutData))
	}
}
