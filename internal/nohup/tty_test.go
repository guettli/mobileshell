package nohup

import (
	"testing"
	"time"

	"mobileshell/internal/executor"
	"mobileshell/internal/workspace"
	"mobileshell/pkg/outputlog"

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

	// Wait for output file to be created and contain the expected output
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		stdout, _, _, _, _, err := outputlog.ReadCombinedOutputWithNohup(proc.OutputFile)
		assert.NoError(collect, err)
		// With PTY support, stdin should be detected as a TTY
		assert.Contains(collect, stdout, "stdin is a tty", "This indicates PTY support is not working correctly")
	}, 2*time.Second, 50*time.Millisecond)
}

// TestTTYEcho is disabled - stdin.pipe is outdated, Unix domain sockets should be used instead
// TODO: Re-implement this test using Unix domain sockets when that functionality is available

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
		stdout, _, _, _, _, err := outputlog.ReadCombinedOutputWithNohup(proc.OutputFile)
		assert.NoError(collect, err)
		assert.Contains(collect, stdout, "\033[31m")
	}, time.Second, 50*time.Millisecond)
}
