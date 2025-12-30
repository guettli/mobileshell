package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceCreation(t *testing.T) {
	// Create temporary state directory
	tmpDir := t.TempDir()

	// Create temporary workspace directory that exists
	workDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create a workspace
	ws, err := CreateWorkspace(tmpDir, "test-workspace", workDir, "source .env")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	if ws.ID != "test-workspace" {
		t.Errorf("Expected workspace ID 'test-workspace', got '%s'", ws.ID)
	}

	if ws.Name != "test-workspace" {
		t.Errorf("Expected workspace name 'test-workspace', got '%s'", ws.Name)
	}

	if ws.Directory != workDir {
		t.Errorf("Expected directory '%s', got '%s'", workDir, ws.Directory)
	}

	if ws.PreCommand != "source .env" {
		t.Errorf("Expected pre-command 'source .env', got '%s'", ws.PreCommand)
	}

	// Verify workspace directory exists
	if _, err := os.Stat(ws.Path); os.IsNotExist(err) {
		t.Errorf("Workspace directory does not exist: %s", ws.Path)
	}

	// Verify processes directory exists
	processesDir := filepath.Join(ws.Path, "processes")
	if _, err := os.Stat(processesDir); os.IsNotExist(err) {
		t.Errorf("Processes directory does not exist: %s", processesDir)
	}

	// Verify individual metadata files exist
	idFile := filepath.Join(ws.Path, "id")
	if _, err := os.Stat(idFile); os.IsNotExist(err) {
		t.Errorf("ID file does not exist: %s", idFile)
	}

	nameFile := filepath.Join(ws.Path, "name")
	if _, err := os.Stat(nameFile); os.IsNotExist(err) {
		t.Errorf("Name file does not exist: %s", nameFile)
	}

	directoryFile := filepath.Join(ws.Path, "directory")
	if _, err := os.Stat(directoryFile); os.IsNotExist(err) {
		t.Errorf("Directory file does not exist: %s", directoryFile)
	}

	createdAtFile := filepath.Join(ws.Path, "created-at")
	if _, err := os.Stat(createdAtFile); os.IsNotExist(err) {
		t.Errorf("Created-at file does not exist: %s", createdAtFile)
	}
}

func TestProcessCreation(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	ws, err := CreateWorkspace(tmpDir, "test", workDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a process
	hash, err := CreateProcess(ws, "echo hello")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	if hash == "" {
		t.Error("Process hash should not be empty")
	}

	// Verify process directory exists
	processDir := GetProcessDir(ws, hash)
	if _, err := os.Stat(processDir); os.IsNotExist(err) {
		t.Errorf("Process directory does not exist: %s", processDir)
	}

	// Verify stdout file exists
	stdoutFile := filepath.Join(processDir, "stdout")
	if _, err := os.Stat(stdoutFile); os.IsNotExist(err) {
		t.Errorf("Stdout file does not exist: %s", stdoutFile)
	}

	// Verify stderr file exists
	stderrFile := filepath.Join(processDir, "stderr")
	if _, err := os.Stat(stderrFile); os.IsNotExist(err) {
		t.Errorf("Stderr file does not exist: %s", stderrFile)
	}

	// Verify individual metadata files exist
	cmdFile := filepath.Join(processDir, "cmd")
	if _, err := os.Stat(cmdFile); os.IsNotExist(err) {
		t.Errorf("Command file does not exist: %s", cmdFile)
	}

	starttimeFile := filepath.Join(processDir, "starttime")
	if _, err := os.Stat(starttimeFile); os.IsNotExist(err) {
		t.Errorf("Starttime file does not exist: %s", starttimeFile)
	}

	statusFile := filepath.Join(processDir, "status")
	if _, err := os.Stat(statusFile); os.IsNotExist(err) {
		t.Errorf("Status file does not exist: %s", statusFile)
	}

	// Get the process
	proc, err := GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}

	if proc.Command != "echo hello" {
		t.Errorf("Expected command 'echo hello', got '%s'", proc.Command)
	}

	if proc.Status != "pending" {
		t.Errorf("Expected status 'pending', got '%s'", proc.Status)
	}
}

func TestProcessUpdate(t *testing.T) {
	tmpDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	ws, err := CreateWorkspace(tmpDir, "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	hash, err := CreateProcess(ws, "sleep 1")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	// Update PID
	err = UpdateProcessPID(ws, hash, 12345)
	if err != nil {
		t.Fatalf("Failed to update PID: %v", err)
	}

	proc, err := GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}

	if proc.PID != 12345 {
		t.Errorf("Expected PID 12345, got %d", proc.PID)
	}

	if proc.Status != "running" {
		t.Errorf("Expected status 'running', got '%s'", proc.Status)
	}

	// Update exit
	err = UpdateProcessExit(ws, hash, 0, "")
	if err != nil {
		t.Fatalf("Failed to update exit: %v", err)
	}

	proc, err = GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}

	if proc.ExitCode == nil || *proc.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %v", proc.ExitCode)
	}

	if proc.Status != "completed" {
		t.Errorf("Expected status 'completed', got '%s'", proc.Status)
	}

	if proc.EndTime.IsZero() {
		t.Error("End time should be set")
	}
}

func TestListWorkspaces(t *testing.T) {
	tmpDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create multiple workspaces
	_, err := CreateWorkspace(tmpDir, "ws1", t.TempDir(), "")
	if err != nil {
		t.Fatalf("Failed to create workspace 1: %v", err)
	}

	time.Sleep(10 * time.Millisecond) // Ensure different timestamps

	_, err = CreateWorkspace(tmpDir, "ws2", t.TempDir(), "source .bashrc")
	if err != nil {
		t.Fatalf("Failed to create workspace 2: %v", err)
	}

	workspaces, err := ListWorkspaces(tmpDir)
	if err != nil {
		t.Fatalf("Failed to list workspaces: %v", err)
	}

	if len(workspaces) != 2 {
		t.Errorf("Expected 2 workspaces, got %d", len(workspaces))
	}
}

func TestListProcesses(t *testing.T) {
	tmpDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	ws, err := CreateWorkspace(tmpDir, "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create multiple processes
	_, err = CreateProcess(ws, "echo 1")
	if err != nil {
		t.Fatalf("Failed to create process 1: %v", err)
	}

	_, err = CreateProcess(ws, "echo 2")
	if err != nil {
		t.Fatalf("Failed to create process 2: %v", err)
	}

	processes, err := ListProcesses(ws)
	if err != nil {
		t.Fatalf("Failed to list processes: %v", err)
	}

	if len(processes) != 2 {
		t.Errorf("Expected 2 processes, got %d", len(processes))
	}
}

func TestWorkspaceWithSpecialCharacters(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Test workspace name with only special characters should fail
	_, err := CreateWorkspace(tmpDir, "üüü", workDir, "")
	if err == nil {
		t.Error("Expected error when creating workspace with only special characters")
	}
	if err != nil && err.Error() != "workspace name must contain at least one valid character (a-z, 0-9)" {
		t.Errorf("Expected specific error message, got: %v", err)
	}

	// Test workspace name with mixed special and valid characters should work
	ws, err := CreateWorkspace(tmpDir, "test-üüü-workspace", workDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace with mixed characters: %v", err)
	}
	if ws.ID != "test-workspace" {
		t.Errorf("Expected workspace ID 'test-workspace', got '%s'", ws.ID)
	}

	// Test workspace name with only symbols should fail
	_, err = CreateWorkspace(tmpDir, "!!!", workDir, "")
	if err == nil {
		t.Error("Expected error when creating workspace with only symbols")
	}

	// Test workspace name with spaces and special characters should fail
	_, err = CreateWorkspace(tmpDir, "   ", workDir, "")
	if err == nil {
		t.Error("Expected error when creating workspace with only spaces")
	}
}
