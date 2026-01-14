package nohup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/executor"
	"mobileshell/internal/outputlog"
	"mobileshell/internal/process"
	"mobileshell/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	proc, err := executor.Execute(ws, "test -t 0 && echo 'stdin is a tty' || echo 'stdin is NOT a tty'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	outputFile := filepath.Join(proc.ProcessDir, "output.log")
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
	proc, err := executor.Execute(ws, "cat")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	pipePath := filepath.Join(proc.ProcessDir, "stdin.pipe")

	// Wait for process to start by polling for PID file
	for i := 0; i < 20; i++ {
		proc, err := process.LoadProcessFromDir(proc.ProcessDir)
		if err == nil && proc.PID > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Send input
	file, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()
	_, err = file.WriteString("test input\n")
	require.NoError(t, err)

	// Kill the cat process
	proc, err = process.LoadProcessFromDir(proc.ProcessDir)
	require.NoError(t, err)
	p, err := os.FindProcess(proc.PID)
	require.NoError(t, err)
	_ = p.Kill()
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		stdout, _, _, err := outputlog.ReadCombinedOutput(proc.OutputFile)
		require.NoError(t, err)
		require.Contains(t, stdout, "test input")
	}, time.Second, 50*time.Millisecond)
}

// TestColorOutput verifies that ANSI color codes work with PTY
// Many commands detect TTY and only output colors when connected to a terminal
func TestColorOutput(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize workspace storage
	if err := workspace.InitWorkspaces(tmpDir); err != nil { // TODO: remove all if err to require.NoError()
		t.Fatalf("Failed to initialize workspaces: %v", err)
	}

	// Create workspace
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	// Use printf to output ANSI color codes
	// Many tools like ls --color=auto check isatty() and only output colors with a TTY
	proc, err := executor.Execute(ws, "printf '\\033[31mRED TEXT\\033[0m\\n'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		stdout, _, _, err := outputlog.ReadCombinedOutput(proc.OutputFile)
		require.NoError(t, err)
		require.Contains(t, stdout, "\033[31m")
	}, time.Second, 50*time.Millisecond)
}
