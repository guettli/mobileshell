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
	"syscall"
	"time"

	"github.com/creack/pty"
	"mobileshell/internal/workspace"
)

// OutputLine represents a single line of output from either stdout or stderr
type OutputLine struct {
	Stream    string    // "stdout", "stderr", or "stdin"
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
	binaryDetected := false

	// Start goroutine to write output lines to file
	go func() {
		defer close(writerDone)
		for line := range outputChan {
			// Check if this line contains binary data
			if !binaryDetected && (line.Stream == "stdout" || line.Stream == "stderr") {
				if isBinaryData(line.Line) {
					binaryDetected = true
					// Create binary-data marker file
					binaryMarkerFile := filepath.Join(processDir, "binary-data")
					_ = os.WriteFile(binaryMarkerFile, []byte("true"), 0600)
				}
			}

			// Format: "stdout 2025-01-01T12:34:56.789Z: line"
			timestamp := line.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
			formattedLine := fmt.Sprintf("%s %s: %s\n", line.Stream, timestamp, line.Line)
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

	// Start the command with a PTY
	// This provides a pseudo-terminal, making commands think they're running in a real terminal
	// This is essential for commands that check isatty() or need terminal capabilities
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start command with pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Set PTY size to a reasonable default (80x24 is standard terminal size)
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		slog.Warn("Failed to set PTY size, using default", "error", err)
	}

	// Start goroutine to read from PTY (combines stdout and stderr)
	// PTY combines both streams, so we label everything as stdout
	readersDone := make(chan struct{}, 1)
	go readLines(ptmx, "stdout", outputChan, readersDone)

	pid := cmd.Process.Pid

	// Start goroutine to read from named pipe and forward to PTY
	// Started IMMEDIATELY after process starts, before any file I/O
	// to minimize the window where a writer might try to connect before reader is ready
	stdinDone := make(chan struct{})
	namedPipePath := filepath.Join(processDir, "stdin.pipe")
	go readStdinPipe(namedPipePath, ptmx, outputChan, stdinDone)

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

	// Wait for reader to finish draining the PTY
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

// isBinaryData checks if a line contains binary data
// A line is considered binary if it contains null bytes or has a high proportion
// of non-printable characters (excluding common whitespace)
func isBinaryData(line string) bool {
	if len(line) == 0 {
		return false
	}

	nonPrintableCount := 0
	for _, r := range line {
		// Check for null bytes - definitive indicator of binary data
		if r == 0 {
			return true
		}
		// Count non-printable characters (excluding tab, newline, carriage return)
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			nonPrintableCount++
		} else if r > 126 && r < 160 {
			// Control characters in extended ASCII
			nonPrintableCount++
		}
	}

	// If more than 30% of characters are non-printable, consider it binary
	threshold := float64(len(line)) * 0.3
	return float64(nonPrintableCount) > threshold
}

// readLines reads lines from a reader and sends them to the output channel
// This function flushes partial lines (without newlines) after a timeout to support
// interactive programs that output prompts without trailing newlines (e.g., "Enter filename: ")
func readLines(reader io.Reader, stream string, outputChan chan<- OutputLine, done chan<- struct{}) {
	defer func() {
		done <- struct{}{}
	}()

	br := bufio.NewReader(reader)
	var buffer []byte
	flushTimeout := 100 * time.Millisecond

	for {
		// Read one byte at a time (bufio.Reader handles internal buffering)
		b, err := br.ReadByte()
		if err != nil {
			// EOF or error - flush any remaining buffer
			if len(buffer) > 0 {
				outputChan <- OutputLine{
					Stream:    stream,
					Timestamp: time.Now().UTC(),
					Line:      string(buffer),
				}
			}
			break
		}
		buffer = append(buffer, b)

		// Check if we should flush
		shouldFlush := false
		if len(buffer) > 0 && buffer[len(buffer)-1] == '\n' {
			shouldFlush = true
		} else if len(buffer) > 0 && br.Buffered() == 0 {
			// No more data available immediately, wait a bit for more
			time.Sleep(flushTimeout)
			// If still no data, flush what we have
			if br.Buffered() == 0 {
				shouldFlush = true
			}
		}

		if shouldFlush {
			// Remove trailing newline if present
			line := string(buffer)
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}

			outputChan <- OutputLine{
				Stream:    stream,
				Timestamp: time.Now().UTC(),
				Line:      line,
			}
			buffer = buffer[:0] // Reset buffer
		}
	}
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
