package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceCreation(t *testing.T) {
	t.Parallel()
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

func TestListWorkspaces(t *testing.T) {
	t.Parallel()
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

func TestWorkspaceWithSpecialCharacters(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
			result := normalizePreCommand(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePreCommand() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPreCommandWithCRLFInWorkspace(t *testing.T) {
	t.Parallel()
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
