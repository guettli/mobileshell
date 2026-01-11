package workspace

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/process"
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

	expectedPreCommand := "#!/usr/bin/env bash\nsource .env"
	if ws.PreCommand != expectedPreCommand {
		t.Errorf("Expected pre-command '%s', got '%s'", expectedPreCommand, ws.PreCommand)
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
	command := "echo hello"
	hash := GenerateProcessHash(command, time.Now().UTC())
	processDir := GetProcessDir(ws, hash)
	if err := process.InitializeProcessDir(processDir, command); err != nil {
		t.Fatalf("Failed to initialize process directory: %v", err)
	}

	if hash == "" {
		t.Error("Process hash should not be empty")
	}

	// Verify process directory exists
	if _, err := os.Stat(processDir); os.IsNotExist(err) {
		t.Errorf("Process directory does not exist: %s", processDir)
	}

	// Verify output.log file exists
	outputFile := filepath.Join(processDir, "output.log")
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		t.Errorf("Output file does not exist: %s", outputFile)
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

	completedFile := filepath.Join(processDir, "completed")
	if _, err := os.Stat(completedFile); os.IsNotExist(err) {
		t.Errorf("Completed file does not exist: %s", completedFile)
	}

	// Get the process
	proc, err := GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}

	if proc.Command != "echo hello" {
		t.Errorf("Expected command 'echo hello', got '%s'", proc.Command)
	}

	if proc.Completed {
		t.Error("Expected process to not be completed")
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

	command := "sleep 1"
	hash := GenerateProcessHash(command, time.Now().UTC())
	processDir := GetProcessDir(ws, hash)
	if err := process.InitializeProcessDir(processDir, command); err != nil {
		t.Fatalf("Failed to initialize process directory: %v", err)
	}

	// Update PID
	err = process.UpdateProcessPIDInDir(processDir, 12345)
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

	if proc.Completed {
		t.Error("Expected process to not be completed yet")
	}

	// Update exit
	err = process.UpdateProcessExitInDir(processDir, 0, "")
	if err != nil {
		t.Fatalf("Failed to update exit: %v", err)
	}

	proc, err = GetProcess(ws, hash)
	if err != nil {
		t.Fatalf("Failed to get process: %v", err)
	}

	if proc.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", proc.ExitCode)
	}

	if !proc.Completed {
		t.Error("Expected process to be completed")
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
	cmd1 := "echo 1"
	h1 := GenerateProcessHash(cmd1, time.Now().UTC())
	if err := process.InitializeProcessDir(GetProcessDir(ws, h1), cmd1); err != nil {
		t.Fatalf("Failed to create process 1: %v", err)
	}

	time.Sleep(time.Millisecond) // Different timestamps

	cmd2 := "echo 2"
	h2 := GenerateProcessHash(cmd2, time.Now().UTC())
	if err := process.InitializeProcessDir(GetProcessDir(ws, h2), cmd2); err != nil {
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

func TestPreCommandLineEndingNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "CRLF line endings",
			input:    "export FOO=bar\r\necho $FOO",
			expected: "#!/usr/bin/env bash\nexport FOO=bar\necho $FOO",
		},
		{
			name:     "Mixed CRLF and LF",
			input:    "export FOO=bar\r\necho $FOO\nexport BAZ=qux",
			expected: "#!/usr/bin/env bash\nexport FOO=bar\necho $FOO\nexport BAZ=qux",
		},
		{
			name:     "Standalone CR characters",
			input:    "export FOO=bar\recho $FOO",
			expected: "#!/usr/bin/env bash\nexport FOO=barecho $FOO",
		},
		{
			name:     "CRLF with existing shebang",
			input:    "#!/usr/bin/env fish\r\nset FOO bar\r\necho $FOO",
			expected: "#!/usr/bin/env fish\nset FOO bar\necho $FOO",
		},
		{
			name:     "LF only (no change needed)",
			input:    "export FOO=bar\necho $FOO",
			expected: "#!/usr/bin/env bash\nexport FOO=bar\necho $FOO",
		},
		{
			name:     "Empty pre-command",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizePreCommand(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePreCommand() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPreCommandWithCRLFInWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace with pre-command containing CRLF
	preCommandWithCRLF := "export FOO=bar\r\nexport BAZ=qux\r\necho $FOO"
	ws, err := CreateWorkspace(tmpDir, "test-crlf", workDir, preCommandWithCRLF)
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Verify the pre-command was normalized (no \r characters)
	if strings.Contains(ws.PreCommand, "\r") {
		t.Errorf("PreCommand should not contain \\r characters, got: %q", ws.PreCommand)
	}

	expectedPreCommand := "#!/usr/bin/env bash\nexport FOO=bar\nexport BAZ=qux\necho $FOO"
	if ws.PreCommand != expectedPreCommand {
		t.Errorf("Expected pre-command %q, got %q", expectedPreCommand, ws.PreCommand)
	}

	// Verify the pre-command file on disk also doesn't have \r
	preCommandFile := filepath.Join(ws.Path, "pre-command")
	data, err := os.ReadFile(preCommandFile)
	if err != nil {
		t.Fatalf("Failed to read pre-command file: %v", err)
	}

	if bytes.Contains(data, []byte("\r")) {
		t.Errorf("Pre-command file should not contain \\r characters, got: %q", string(data))
	}
}

func TestPreCommandWithCRLFExecutes(t *testing.T) {
	tmpDir := t.TempDir()
	workDir := t.TempDir()

	if err := InitWorkspaces(tmpDir); err != nil {
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace with pre-command containing CRLF that sets an environment variable
	// This simulates a user copy-pasting a pre-command from Windows or a web form
	preCommandWithCRLF := "export TEST_VAR=success\r\necho \"Pre-command executed\""
	ws, err := CreateWorkspace(tmpDir, "test-exec", workDir, preCommandWithCRLF)
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Create a process to simulate what happens in nohup
	command := "echo $TEST_VAR"
	hash := GenerateProcessHash(command, time.Now().UTC())
	processDir := GetProcessDir(ws, hash)
	if err := process.InitializeProcessDir(processDir, command); err != nil {
		t.Fatalf("Failed to initialize process directory: %v", err)
	}

	// Write pre-command to a script file (simulating what nohup.go does)
	preScriptPath := filepath.Join(processDir, "pre-command.sh")
	if err := os.WriteFile(preScriptPath, []byte(ws.PreCommand), 0700); err != nil {
		t.Fatalf("Failed to write pre-command script: %v", err)
	}

	// Extract shell from shebang
	shell := ExtractShellFromShebang(ws.PreCommand)

	// Try to execute the pre-command script
	// This is the critical test - if CRLF wasn't normalized, this would fail with:
	// "$'\r': command not found"
	cmd := exec.Command(shell, "-c", fmt.Sprintf(". %s && echo \"Success: $TEST_VAR\"", preScriptPath))
	cmd.Dir = workDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to execute pre-command script (this would be the CRLF bug): %v\nOutput: %s", err, string(output))
	}

	// Verify the script executed successfully
	outputStr := string(output)
	if !strings.Contains(outputStr, "Success: success") {
		t.Errorf("Pre-command did not set environment variable correctly. Output: %s", outputStr)
	}

	// Verify no CRLF errors in output
	if strings.Contains(outputStr, "$'\\r'") || strings.Contains(outputStr, "command not found") {
		t.Errorf("Pre-command execution failed with CRLF error: %s", outputStr)
	}
}
