package nohup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mobileshell/internal/executor"
	"mobileshell/internal/process"
	"mobileshell/internal/workspace"
	"mobileshell/pkg/outputlog"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestNohupRun(t *testing.T) {
	stateDir := t.TempDir()

	// Initialize workspace storage
	err := workspace.InitWorkspaces(stateDir)
	require.NoError(t, err)

	workDir := t.TempDir()

	// Create workspace
	ws, err := workspace.CreateWorkspace(stateDir, "test", workDir, "")
	require.NoError(t, err)

	// Create a process
	proc, err := executor.Execute(ws, "echo 'Hello, World!'")
	require.NoError(t, err)

	// Verify PID file was created
	processDir := proc.ProcessDir
	var pidFile string
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		pidFile = filepath.Join(processDir, "pid")
		_, err = os.Stat(pidFile)
		assert.NoError(collect, err)
	}, 3*time.Second, 10*time.Millisecond)

	// Verify exit-status file was created
	var exitStatusFile string
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		exitStatusFile = filepath.Join(processDir, "exit-status")
		_, err = os.Stat(exitStatusFile)
		assert.NoError(collect, err)
	}, time.Second, 10*time.Millisecond)

	// Read exit status
	exitStatusData, err := os.ReadFile(exitStatusFile)
	require.NoError(t, err)
	exitCode, err := strconv.Atoi(string(exitStatusData))
	require.NoError(t, err)
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}

	// Verify output.log contains expected output with STDOUT prefix
	outputFile := filepath.Join(processDir, "output.log")
	outputData, err := os.ReadFile(outputFile)
	require.NoError(t, err)

	// Output should contain "stdout" prefix and timestamp in ISO8601 format
	output := string(outputData)
	if !contains(output, "stdout") || !contains(output, "Hello, World!") {
		t.Errorf("Expected output to contain 'stdout' and 'Hello, World!', got '%s'", output)
	}

	// Verify process metadata was updated
	proc, err = process.LoadProcessFromDir(proc.ProcessDir)
	require.NoError(t, err)

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
	err := workspace.InitWorkspaces(tmpDir)
	require.NoError(t, err)

	// Create workspace with pre-command
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "export TEST_VAR=hello")
	require.NoError(t, err)

	// Create a process that uses the environment variable
	proc, err := executor.Execute(ws, "echo $TEST_VAR")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		// Verify output.log contains the environment variable value
		outputFile := filepath.Join(proc.ProcessDir, "output.log")
		outputData, err := os.ReadFile(outputFile)
		assert.NoError(collect, err)

		output := string(outputData)
		if !contains(output, "stdout") || !contains(output, "hello") {
			assert.Fail(collect, "Expected output to contain 'stdout' and 'hello', got '%s'", output)
		}
	}, time.Second, 10*time.Millisecond)
}

func TestNohupRunWithFailingCommand(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize workspace storage
	err := workspace.InitWorkspaces(tmpDir)
	require.NoError(t, err)

	// Create workspace
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	require.NoError(t, err)

	// Create a process that will fail
	proc, err := executor.Execute(ws, "exit 42")
	require.NoError(t, err)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		exitStatusFile := filepath.Join(proc.ProcessDir, "exit-status")
		exitStatusData, err := os.ReadFile(exitStatusFile)
		assert.NoError(collect, err)
		exitCode, err := strconv.Atoi(string(exitStatusData))
		assert.NoError(collect, err)
		if exitCode != 42 {
			assert.Fail(collect, "Expected exit code 42, got %d", exitCode)
		}
	}, time.Second, 10*time.Millisecond)

	// Verify process metadata
	proc, err = process.LoadProcessFromDir(proc.ProcessDir)
	require.NoError(t, err)

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
	require.NoError(t, err)

	// Initialize workspace storage
	err = workspace.InitWorkspaces(tmpDir)
	require.NoError(t, err)

	// Create workspace with specific directory
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	require.NoError(t, err)

	// Create a process that reads the file
	proc, err := executor.Execute(ws, fmt.Sprintf("cat %s", testFile))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		// Verify output.log contains the file content
		outputFile := filepath.Join(proc.ProcessDir, "output.log")
		stdoutBytes, stderrBytes, stdinBytes, err := outputlog.ReadThreeStreams(outputFile, "stdout", "stderr", "stdin")
		stdout := string(stdoutBytes)
		stderr := string(stderrBytes)
		stdin := string(stdinBytes)
		assert.NoError(collect, err)
		assert.NoError(collect, err)
		assert.Equal(collect, "test content", stdout)
		assert.Equal(collect, "", stderr)
		assert.Equal(collect, "", stdin)
	}, time.Second, 10*time.Millisecond)
}

func TestNohupRunWithStdinViaGoRun(t *testing.T) {
	tmpDir := t.TempDir()

	// Create process directory
	processDir := filepath.Join(tmpDir, "process")
	err := os.MkdirAll(processDir, 0o755)
	require.NoError(t, err)

	// Create bash script inside the process directory that reads from stdin
	scriptPath := filepath.Join(processDir, "script.sh")
	scriptContent := `#!/bin/bash
echo "reading from stdin"
read -r foo
echo "you entered: $foo"
`
	err = os.WriteFile(scriptPath, []byte(scriptContent), 0o755)
	require.NoError(t, err)

	// Prepare the nohup command - nohup expects the script path as the first argument
	cmd := exec.Command("go", "run", "../../cmd/mobileshell", "nohup", scriptPath)

	// Capture stdout and stderr for debugging
	var cmdOutput strings.Builder
	cmd.Stdout = &cmdOutput
	cmd.Stderr = &cmdOutput

	// Create stdin pipe
	stdinPipe, err := cmd.StdinPipe()
	require.NoError(t, err)

	// Start the command
	err = cmd.Start()
	require.NoError(t, err)

	// Write to stdin
	_, err = stdinPipe.Write([]byte("hello word\n"))
	require.NoError(t, err)

	// Close stdin to signal end of input
	err = stdinPipe.Close()
	require.NoError(t, err)

	// Wait for command to complete with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Logf("Command output:\n%s", cmdOutput.String())
		}
		require.NoError(t, err)
	case <-time.After(4 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("Test timed out after 4 seconds. Output:\n%s", cmdOutput.String())
	}

	// Verify output.log exists and contains expected content
	outputFile := filepath.Join(processDir, "output.log")
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		stdoutBytes, stderrBytes, stdinBytes, err := outputlog.ReadThreeStreams(outputFile, "stdout", "stderr", "stdin")
		assert.NoError(collect, err)

		stdout := string(stdoutBytes)
		stderr := string(stderrBytes)
		stdin := string(stdinBytes)

		assert.Contains(collect, stdout, "reading from stdin")
		assert.Contains(collect, stdout, "you entered: hello word")
		assert.Equal(collect, "", stderr)
		assert.Equal(collect, "hello word\n", stdin)
	}, time.Second, 10*time.Millisecond)
}
