package nohup

import (
	"fmt"
	"net"
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
	t.Parallel()
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
	t.Parallel()
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
			assert.Failf(collect, "Expected output to contain 'stdout' and 'hello'", "got: %s", output)
		}
	}, time.Second, 10*time.Millisecond)
}

func TestNohupRunWithFailingCommand(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestNohupSignalViaUnixSocket(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Initialize workspace storage
	err := workspace.InitWorkspaces(tmpDir)
	require.NoError(t, err)

	// Create workspace
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	require.NoError(t, err)

	// Create a long-running process that handles SIGTERM gracefully
	// The trap will write to a file since PTY output might not be captured reliably during signal handling
	signalReceivedFile := filepath.Join(tmpDir, "signal-received")
	script := fmt.Sprintf(`#!/bin/bash
set -e
trap 'echo "SIGTERM" > %s; exit 143' TERM
echo "Process started"
# Use a loop that checks frequently instead of one long sleep
for i in {1..300}; do
  sleep 0.1
done
echo "Process completed naturally"
`, signalReceivedFile)
	scriptPath := filepath.Join(tmpDir, "test-signal.sh")
	err = os.WriteFile(scriptPath, []byte(script), 0o755)
	require.NoError(t, err)

	// Execute the process
	proc, err := executor.Execute(ws, scriptPath)
	require.NoError(t, err)

	// Wait for process to start and write PID
	var pidData []byte
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		pidFile := filepath.Join(proc.ProcessDir, "pid")
		pidData, err = os.ReadFile(pidFile)
		assert.NoError(collect, err)
		assert.NotEmpty(collect, pidData)
	}, 3*time.Second, 10*time.Millisecond)

	// Wait for "Process started" message in output
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		outputFile := filepath.Join(proc.ProcessDir, "output.log")
		stdoutBytes, _, _, err := outputlog.ReadThreeStreams(outputFile, "stdout", "stderr", "stdin")
		assert.NoError(collect, err)
		assert.Contains(collect, string(stdoutBytes), "Process started")
	}, 3*time.Second, 10*time.Millisecond)

	// Give the process time to enter the sleep loop and set up signal handlers
	time.Sleep(500 * time.Millisecond)

	// Connect to the Unix domain socket
	// Socket path is in /tmp with format: /tmp/ms-<commandId>.sock
	socketPath := filepath.Join("/tmp", "ms-"+proc.CommandId+".sock")

	// Wait for socket to be available
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		_, err := os.Stat(socketPath)
		assert.NoError(collect, err)
	}, 3*time.Second, 10*time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err)

	// Create an OutputLog writer to send the signal
	writer := outputlog.NewOutputLogWriter(conn, nil)
	signalWriter := writer.StreamWriter("signal")

	// Send SIGTERM signal
	_, err = signalWriter.Write([]byte("TERM"))
	require.NoError(t, err)

	// Close the writer to flush
	writer.Close()
	_ = conn.Close()

	// Give the signal time to be processed
	time.Sleep(200 * time.Millisecond)

	// Wait for the process to complete
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		completedFile := filepath.Join(proc.ProcessDir, "completed")
		completedData, err := os.ReadFile(completedFile)
		assert.NoError(collect, err)
		assert.Equal(collect, "true", strings.TrimSpace(string(completedData)))
	}, 5*time.Second, 100*time.Millisecond)

	// Verify the process output
	outputFile := filepath.Join(proc.ProcessDir, "output.log")
	stdoutBytes, _, _, err := outputlog.ReadThreeStreams(outputFile, "stdout", "stderr", "stdin")
	require.NoError(t, err)
	stdout := string(stdoutBytes)

	// Should contain the startup message
	require.Contains(t, stdout, "Process started")
	// Should NOT contain the natural completion message (process was killed by signal)
	require.NotContains(t, stdout, "Process completed naturally")

	// Verify the signal file was written (indicating process was terminated by signal)
	signalFile := filepath.Join(proc.ProcessDir, "signal")
	signalData, err := os.ReadFile(signalFile)
	require.NoError(t, err)
	// The signal should be SIGTERM
	require.Contains(t, string(signalData), "terminated")

	// Verify exit status - process was killed by signal, so non-zero exit
	exitStatusFile := filepath.Join(proc.ProcessDir, "exit-status")
	exitStatusData, err := os.ReadFile(exitStatusFile)
	require.NoError(t, err)
	exitCode, err := strconv.Atoi(strings.TrimSpace(string(exitStatusData)))
	require.NoError(t, err)
	// Exit code should be non-zero (process was terminated by signal)
	require.NotEqual(t, 0, exitCode)
}

func TestNohupSignalViaUnixSocketNumeric(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	// Initialize workspace storage
	err := workspace.InitWorkspaces(tmpDir)
	require.NoError(t, err)

	// Create workspace
	ws, err := workspace.CreateWorkspace(tmpDir, "test", tmpDir, "")
	require.NoError(t, err)

	// Create a long-running process - SIGKILL cannot be trapped
	script := `#!/bin/bash
echo "Process started"
for i in {1..300}; do
  sleep 0.1
done
echo "Process completed naturally"
`
	scriptPath := filepath.Join(tmpDir, "test-signal-numeric.sh")
	err = os.WriteFile(scriptPath, []byte(script), 0o755)
	require.NoError(t, err)

	// Execute the process
	proc, err := executor.Execute(ws, scriptPath)
	require.NoError(t, err)

	// Wait for process to start
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		pidFile := filepath.Join(proc.ProcessDir, "pid")
		_, err = os.ReadFile(pidFile)
		assert.NoError(collect, err)
	}, 3*time.Second, 10*time.Millisecond)

	// Wait for "Process started" message
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		outputFile := filepath.Join(proc.ProcessDir, "output.log")
		stdoutBytes, _, _, err := outputlog.ReadThreeStreams(outputFile, "stdout", "stderr", "stdin")
		assert.NoError(collect, err)
		assert.Contains(collect, string(stdoutBytes), "Process started")
	}, 3*time.Second, 10*time.Millisecond)

	// Give the process time to set up
	time.Sleep(500 * time.Millisecond)

	// Connect to the Unix domain socket
	socketPath := filepath.Join("/tmp", "ms-"+proc.CommandId+".sock")

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err)

	// Create an OutputLog writer to send the signal
	writer := outputlog.NewOutputLogWriter(conn, nil)
	signalWriter := writer.StreamWriter("signal")

	// Send SIGKILL signal using numeric value (9) - more forceful than SIGINT
	_, err = signalWriter.Write([]byte("9"))
	require.NoError(t, err)

	// Close the writer to flush
	writer.Close()
	_ = conn.Close()

	// Give the signal time to be processed
	time.Sleep(200 * time.Millisecond)

	// Wait for the process to complete
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		completedFile := filepath.Join(proc.ProcessDir, "completed")
		completedData, err := os.ReadFile(completedFile)
		assert.NoError(collect, err)
		assert.Equal(collect, "true", strings.TrimSpace(string(completedData)))
	}, 5*time.Second, 100*time.Millisecond)

	// Verify the signal file was written
	signalFile := filepath.Join(proc.ProcessDir, "signal")
	signalData, err := os.ReadFile(signalFile)
	require.NoError(t, err)
	// The signal should be SIGKILL (killed)
	require.Contains(t, string(signalData), "killed")

	// Verify exit status is non-zero
	exitStatusFile := filepath.Join(proc.ProcessDir, "exit-status")
	exitStatusData, err := os.ReadFile(exitStatusFile)
	require.NoError(t, err)
	exitCode, err := strconv.Atoi(strings.TrimSpace(string(exitStatusData)))
	require.NoError(t, err)
	require.NotEqual(t, 0, exitCode)
}
