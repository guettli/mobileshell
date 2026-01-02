package nohup

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mobileshell/internal/workspace"
)

// OutputLine represents a single line of output from either stdout or stderr
type OutputLine struct {
	Stream    string    // "STDOUT" or "STDERR"
	Timestamp time.Time // UTC timestamp
	Line      string    // The actual line content
}

// Run executes a command in nohup mode within a workspace
// This function is called by the `mobileshell nohup` command
func Run(stateDir, workspaceTimestamp, processHash string, commandArgs []string) error {
	// Get the workspace
	ws, err := workspace.GetWorkspace(stateDir, workspaceTimestamp)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	// Get the process
	proc, err := workspace.GetProcess(ws, processHash)
	if err != nil {
		return fmt.Errorf("failed to get process: %w", err)
	}

	processDir := workspace.GetProcessDir(ws, processHash)

	// Open combined output file
	outputFile := filepath.Join(processDir, "output.log")
	outFile, err := os.OpenFile(outputFile, os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open output.log file: %w", err)
	}
	defer func() { _ = outFile.Close() }()

	// Create channel for output lines
	outputChan := make(chan OutputLine, 100)
	writerDone := make(chan struct{})

	// Start goroutine to write output lines to file
	go func() {
		defer close(writerDone)
		for line := range outputChan {
			// Format: "stdout 2025-01-01T12:34:56.789Z: line"
			timestamp := line.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
			stream := strings.ToLower(line.Stream)
			formattedLine := fmt.Sprintf("%s %s: %s\n", stream, timestamp, line.Line)
			_, _ = outFile.WriteString(formattedLine)
			// No need to sync since file was opened with O_SYNC
		}
	}()

	// Build the full command with pre-command if specified
	var fullCommand string
	if ws.PreCommand != "" {
		fullCommand = ws.PreCommand + " && " + proc.Command
	} else {
		fullCommand = proc.Command
	}

	// Create the command
	cmd := exec.Command("sh", "-c", fullCommand)
	cmd.Dir = ws.Directory

	// Create stdin pipe for the command
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Create pipes for stdout and stderr
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Start goroutines to read stdout and stderr line-by-line BEFORE starting the process
	// This ensures we don't miss any output from fast-running commands
	readersDone := make(chan struct{}, 2)
	go readLines(stdoutPipe, "STDOUT", outputChan, readersDone)
	go readLines(stderrPipe, "STDERR", outputChan, readersDone)

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	pid := cmd.Process.Pid

	// Start goroutine to read from named pipe and forward to process stdin
	// Started IMMEDIATELY after process starts, before any file I/O
	// to minimize the window where a writer might try to connect before reader is ready
	stdinDone := make(chan struct{})
	namedPipePath := filepath.Join(processDir, "stdin.pipe")
	go readStdinPipe(namedPipePath, stdinPipe, outputChan, stdinDone)

	// Write PID to file
	pidFile := filepath.Join(processDir, "pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0600); err != nil {
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	// Update process metadata with PID
	if err := workspace.UpdateProcessPID(ws, processHash, pid); err != nil {
		return fmt.Errorf("failed to update process PID: %w", err)
	}

	// Wait for the process to complete
	err = cmd.Wait()

	// Wait for readers to finish draining the pipes
	<-readersDone
	<-readersDone

	// Close output channel so writer can finish
	close(outputChan)

	// Get exit code and signal
	exitCode := 0
	signalName := ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			// Check if process was terminated by a signal
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					signalName = status.Signal().String()
				}
			}
		} else {
			exitCode = 1
		}
	}

	// Wait for writer to finish
	<-writerDone

	// Write exit status to file
	exitStatusFile := filepath.Join(processDir, "exit-status")
	if err := os.WriteFile(exitStatusFile, []byte(strconv.Itoa(exitCode)), 0600); err != nil {
		return fmt.Errorf("failed to write exit-status file: %w", err)
	}

	// Update process metadata with exit information
	if err := workspace.UpdateProcessExit(ws, processHash, exitCode, signalName); err != nil {
		return fmt.Errorf("failed to update process exit: %w", err)
	}

	return nil
}

// readLines reads lines from a reader and sends them to the output channel
func readLines(reader io.Reader, stream string, outputChan chan<- OutputLine, done chan<- struct{}) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		outputChan <- OutputLine{
			Stream:    stream,
			Timestamp: time.Now().UTC(),
			Line:      line,
		}
	}
	// Signal done only after all lines have been sent
	done <- struct{}{}
}

// readStdinPipe reads from a named pipe and forwards data to process stdin and output.log
// This runs in the background and exits when the process ends or stdin write fails
// It continuously reopens the pipe to handle multiple writers (each HTTP request opens/closes)
func readStdinPipe(pipePath string, stdinWriter io.WriteCloser, outputChan chan<- OutputLine, done chan<- struct{}) {
	defer func() {
		close(done)
		_ = stdinWriter.Close()
	}()

	// Keep reading from the pipe until the process exits
	for {
		// Open the named pipe for reading in blocking mode
		// This will block until a writer opens the pipe
		file, err := os.OpenFile(pipePath, os.O_RDONLY, 0)
		if err != nil {
			slog.Error("Failed to open stdin pipe for reading", "error", err, "path", pipePath)
			return
		}

		// Read lines from the pipe until this writer closes it
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()

			// Write to process stdin
			_, err := stdinWriter.Write([]byte(line + "\n"))
			if err != nil {
				// Process stdin closed, stop reading
				_ = file.Close()
				return
			}

			// Also log to output.log
			outputChan <- OutputLine{
				Stream:    "stdin",
				Timestamp: time.Now().UTC(),
				Line:      line,
			}
		}

		// Close this instance of the pipe
		_ = file.Close()

		// The scanner exited because the writer closed the pipe (EOF)
		// Loop back to reopen the pipe and wait for the next writer
	}
}
