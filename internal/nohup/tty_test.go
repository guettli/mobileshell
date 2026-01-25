package nohup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mobileshell/internal/executor"
	"mobileshell/internal/workspace"
	"mobileshell/pkg/outputlog"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestColorOutput verifies that ANSI color codes work with PTY
// Many commands detect TTY and only output colors when connected to a terminal
func TestColorOutput(t *testing.T) {
	t.Parallel()
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
	// Use $'...' syntax to enable escape sequences in bash
	proc, err := executor.Execute(ws, "printf $'\\033[31mRED TEXT\\033[0m\\n'")
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		stdoutBytes, err := outputlog.ReadOneStream(proc.OutputFile, "stdout")
		stdout := string(stdoutBytes)
		assert.NoError(collect, err)
		assert.Contains(collect, stdout, "\033[31m")
	}, testTimeout, 100*time.Millisecond)

	// Wait for the process to complete to avoid cleanup issues
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		completedFile := filepath.Join(proc.ProcessDir, "completed")
		data, err := os.ReadFile(completedFile)
		if err != nil {
			assert.Fail(collect, "completed file not found yet")
			return
		}
		assert.Equal(collect, "true", string(data))
	}, testTimeout, 100*time.Millisecond)
}
