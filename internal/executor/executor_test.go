package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/outputlog"
	"mobileshell/internal/process"
	"mobileshell/internal/workspace"

	"github.com/stretchr/testify/require"
)

func TestInitExecutor(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitExecutor(tmpDir)
	if err != nil {
		t.Fatalf("InitExecutor failed: %v", err)
	}

	// Verify workspaces directory was created
	workspacesDir := filepath.Join(tmpDir, "workspaces")
	info, err := os.Stat(workspacesDir)
	if err != nil {
		t.Fatalf("Workspaces directory not created: %v", err)
	}

	if !info.IsDir() {
		t.Fatal("Workspaces path is not a directory")
	}
}

func TestCreateWorkspace(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitExecutor(tmpDir)
	if err != nil {
		t.Fatalf("InitExecutor failed: %v", err)
	}

	workDir := t.TempDir()

	ws, err := CreateWorkspace(tmpDir, "test-workspace", workDir, "")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if ws == nil {
		t.Fatal("Workspace should not be nil")
	}

	if ws.Name != "test-workspace" {
		t.Errorf("Expected name 'test-workspace', got '%s'", ws.Name)
	}

	if ws.Directory != workDir {
		t.Errorf("Expected directory '%s', got '%s'", workDir, ws.Directory)
	}
}

func TestGetWorkspaceByID(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitExecutor(tmpDir)
	if err != nil {
		t.Fatalf("InitExecutor failed: %v", err)
	}

	workDir := t.TempDir()

	// Create a workspace
	ws, err := CreateWorkspace(tmpDir, "test-workspace", workDir, "")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Get the workspace ID from the path
	wsID := filepath.Base(ws.Path)

	// Retrieve workspace by ID
	retrievedWS, err := GetWorkspaceByID(tmpDir, wsID)
	if err != nil {
		t.Fatalf("GetWorkspaceByID failed: %v", err)
	}

	if retrievedWS.Name != ws.Name {
		t.Errorf("Expected name '%s', got '%s'", ws.Name, retrievedWS.Name)
	}

	// Test with non-existent ID
	_, err = GetWorkspaceByID(tmpDir, "non-existent-id")
	if err == nil {
		t.Error("GetWorkspaceByID should fail for non-existent ID")
	}
}

func TestExecute(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitExecutor(tmpDir)
	if err != nil {
		t.Fatalf("InitExecutor failed: %v", err)
	}

	workDir := t.TempDir()

	// Create a workspace
	ws, err := CreateWorkspace(tmpDir, "test-workspace", workDir, "")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Execute a simple command
	proc, err := Execute(ws, "echo 'test'")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if proc == nil {
		t.Fatal("Process should not be nil")
	}

	if proc.Command != "echo 'test'" {
		t.Errorf("Expected command 'echo 'test'', got '%s'", proc.Command)
	}

	if proc.Completed {
		t.Error("Expected process to not be completed")
	}
}

func TestListWorkspaceProcesses(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitExecutor(tmpDir)
	if err != nil {
		t.Fatalf("InitExecutor failed: %v", err)
	}

	workDir := t.TempDir()

	// Create a workspace
	ws, err := CreateWorkspace(tmpDir, "test-workspace", workDir, "")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Initially, workspace should have no processes
	procs, err := workspace.ListProcesses(ws)
	if err != nil {
		t.Fatalf("ListWorkspaceProcesses failed: %v", err)
	}
	if len(procs) != 0 {
		t.Errorf("Expected 0 processes, got %d", len(procs))
	}

	// Execute a command
	proc, err := Execute(ws, "echo 'test'")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Give the system a moment to create the process
	time.Sleep(10 * time.Millisecond)

	// List workspace processes
	procs, err = workspace.ListProcesses(ws)
	if err != nil {
		t.Fatalf("ListWorkspaceProcesses failed: %v", err)
	}
	if len(procs) < 1 {
		t.Errorf("Expected at least 1 process, got %d", len(procs))
	}

	// Verify process details
	found := false
	for _, p := range procs {
		if p.CommandId == proc.CommandId {
			found = true
			if p.Command != proc.Command {
				t.Errorf("Expected command '%s', got '%s'", proc.Command, p.Command)
			}
		}
	}
	if !found {
		t.Error("Created process not found in workspace process list")
	}
}

func TestGetProcess(t *testing.T) {
	stateDir := t.TempDir()

	err := InitExecutor(stateDir)
	if err != nil {
		t.Fatalf("InitExecutor failed: %v", err)
	}

	workDir := t.TempDir()

	// Create a workspace
	ws, err := CreateWorkspace(stateDir, "test-workspace", workDir, "")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// Execute a command
	proc, err := Execute(ws, "echo 'test'")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Get the process by ID
	retrievedProc, err := process.LoadProcessFromDir(proc.ProcessDir)
	require.NoError(t, err)

	if retrievedProc.CommandId != proc.CommandId {
		t.Errorf("Expected hash '%s', got '%s'", proc.CommandId, retrievedProc.CommandId)
	}

	if retrievedProc.Command != proc.Command {
		t.Errorf("Expected command '%s', got '%s'", proc.Command, retrievedProc.Command)
	}
}

func TestListWorkspaces(t *testing.T) {
	tmpDir := t.TempDir()

	err := InitExecutor(tmpDir)
	if err != nil {
		t.Fatalf("InitExecutor failed: %v", err)
	}

	// Initially, there should be no workspaces
	workspaces, err := workspace.ListWorkspaces(tmpDir)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}
	if len(workspaces) != 0 {
		t.Errorf("Expected 0 workspaces, got %d", len(workspaces))
	}

	workDir := t.TempDir()

	// Create some workspaces
	ws1, err := CreateWorkspace(tmpDir, "workspace1", workDir, "")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	ws2, err := CreateWorkspace(tmpDir, "workspace2", workDir, "")
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// List workspaces
	workspaces, err = workspace.ListWorkspaces(tmpDir)
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}

	if len(workspaces) != 2 {
		t.Errorf("Expected 2 workspaces, got %d", len(workspaces))
	}

	// Verify workspace names
	names := make(map[string]bool)
	for _, ws := range workspaces {
		names[ws.Name] = true
	}

	if !names[ws1.Name] {
		t.Errorf("Workspace '%s' not found", ws1.Name)
	}
	if !names[ws2.Name] {
		t.Errorf("Workspace '%s' not found", ws2.Name)
	}
}

func TestReadCombinedOutput(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test combined output file with new format
	var testContent strings.Builder
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC),
		Line:      "line 1\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stderr",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 1, 0, time.UTC),
		Line:      "error message\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 2, 0, time.UTC),
		Line:      "line 2\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdin",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 3, 0, time.UTC),
		Line:      "input text\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "signal-sent",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 4, 0, time.UTC),
		Line:      "15 SIGTERM\n",
	}))
	testFile := filepath.Join(tmpDir, "combined-output.txt")
	err := os.WriteFile(testFile, []byte(testContent.String()), 0o600)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Read the combined output
	stdout, stderr, stdin, err := outputlog.ReadCombinedOutput(testFile)
	if err != nil {
		t.Fatalf("ReadCombinedOutput failed: %v", err)
	}

	// Verify stdout
	if !strings.Contains(stdout, "line 1") {
		t.Errorf("stdout should contain 'line 1', got: %s", stdout)
	}
	if !strings.Contains(stdout, "line 2") {
		t.Errorf("stdout should contain 'line 2', got: %s", stdout)
	}

	// Verify stderr
	if !strings.Contains(stderr, "error message") {
		t.Errorf("stderr should contain 'error message', got: %s", stderr)
	}

	// Verify stdin
	if !strings.Contains(stdin, "input text") {
		t.Errorf("stdin should contain 'input text', got: %s", stdin)
	}
	if !strings.Contains(stdin, "Signal sent: 15 SIGTERM") {
		t.Errorf("stdin should contain signal info, got: %s", stdin)
	}

	// Test with non-existent file
	_, _, _, err = outputlog.ReadCombinedOutput(filepath.Join(tmpDir, "non-existent.txt"))
	if err == nil {
		t.Error("ReadCombinedOutput should fail for non-existent file")
	}

	// Test with malformed content
	malformedContent := "not properly formatted line\n"
	malformedFile := filepath.Join(tmpDir, "malformed.txt")
	err = os.WriteFile(malformedFile, []byte(malformedContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create malformed test file: %v", err)
	}

	stdout, stderr, stdin, err = outputlog.ReadCombinedOutput(malformedFile)
	if err != nil {
		t.Fatalf("ReadCombinedOutput should handle malformed content: %v", err)
	}

	// Should return empty strings for malformed content
	if stdout != "" || stderr != "" || stdin != "" {
		t.Error("Malformed content should result in empty outputs")
	}
}

func TestNewlinePreservation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test content with new format that preserves newlines
	// Format: "> stream timestamp length: content" + separator \n if content doesn't end with \n
	// where content may include a trailing newline (counted in length)
	var testContent strings.Builder
	// Line 1: content is "foo\n" (4 bytes) - has newline, no extra separator
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC),
		Line:      "foo\n",
	}))
	// Line 2: content is "bar\n" (4 bytes) - has newline, no extra separator
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 1, 0, time.UTC),
		Line:      "bar\n",
	}))
	// Line 3: content is "baz\n" (4 bytes) - has newline, no extra separator
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 2, 0, time.UTC),
		Line:      "baz\n",
	}))
	// Line 4: content is "prompt> " (8 bytes) - NO newline, add separator
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 3, 0, time.UTC),
		Line:      "prompt> ",
	}))

	testFile := filepath.Join(tmpDir, "newline-test.txt")
	err := os.WriteFile(testFile, []byte(testContent.String()), 0o600)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Read using ReadCombinedOutput
	stdout, _, _, err := outputlog.ReadCombinedOutput(testFile)
	if err != nil {
		t.Fatalf("ReadCombinedOutput failed: %v", err)
	}

	// Expected output: "foo\nbar\nbaz\nprompt> "
	// - First line: "foo\n" (has newline)
	// - Second line: "bar\n" (has newline)
	// - Third line: "baz\n" (has newline)
	// - Fourth line: "prompt> " (no newline)
	expected := "foo\nbar\nbaz\nprompt> "
	if stdout != expected {
		t.Errorf("Expected stdout:\n%q\nGot:\n%q", expected, stdout)
	}

	// Verify exact byte content
	if len(stdout) != len(expected) {
		t.Errorf("Expected length %d, got %d", len(expected), len(stdout))
	}

	// Verify no trailing newline after "prompt> "
	if strings.HasSuffix(stdout, "prompt> \n") {
		t.Error("Should not have trailing newline after 'prompt> '")
	}

	// Test with ReadRawStdout for binary data preservation
	rawBytes, err := outputlog.ReadRawStdout(testFile)
	if err != nil {
		t.Fatalf("ReadRawStdout failed: %v", err)
	}

	// Should be exactly the same as the string
	if string(rawBytes) != expected {
		t.Errorf("ReadRawStdout expected:\n%q\nGot:\n%q", expected, string(rawBytes))
	}
}

func TestReadCombinedOutputNewFormat(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test content using the new format
	var testContent strings.Builder
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC),
		Line:      "line 1\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stderr",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 1, 0, time.UTC),
		Line:      "error message\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdout",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 2, 0, time.UTC),
		Line:      "line 2\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "stdin",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 3, 0, time.UTC),
		Line:      "input text\n",
	}))
	testContent.WriteString(outputlog.FormatOutputLine(outputlog.OutputLine{
		Stream:    "signal-sent",
		Timestamp: time.Date(2025, 12, 31, 12, 0, 4, 0, time.UTC),
		Line:      "15 SIGTERM\n",
	}))

	testFile := filepath.Join(tmpDir, "combined-output-new.txt")
	err := os.WriteFile(testFile, []byte(testContent.String()), 0o600)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Read the combined output
	stdout, stderr, stdin, err := outputlog.ReadCombinedOutput(testFile)
	if err != nil {
		t.Fatalf("ReadCombinedOutput failed: %v", err)
	}

	// Verify stdout
	if !strings.Contains(stdout, "line 1") {
		t.Errorf("stdout should contain 'line 1', got: %s", stdout)
	}
	if !strings.Contains(stdout, "line 2") {
		t.Errorf("stdout should contain 'line 2', got: %s", stdout)
	}

	// Verify stderr
	if !strings.Contains(stderr, "error message") {
		t.Errorf("stderr should contain 'error message', got: %s", stderr)
	}

	// Verify stdin
	if !strings.Contains(stdin, "input text") {
		t.Errorf("stdin should contain 'input text', got: %s", stdin)
	}
	if !strings.Contains(stdin, "Signal sent: 15 SIGTERM") {
		t.Errorf("stdin should contain signal info, got: %s", stdin)
	}
}
