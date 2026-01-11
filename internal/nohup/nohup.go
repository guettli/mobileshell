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

	"github.com/creack/pty"
	"mobileshell/internal/claude"
	"mobileshell/internal/outputlog"
	"mobileshell/internal/workspace"
	"mobileshell/pkg/outputtype"
)

// Run executes a command in nohup mode within a workspace
// This function is called by the `mobileshell nohup` command
func Run(stateDir, workspaceID, processHash string) error {
	// Get the workspace
	ws, err := workspace.GetWorkspace(stateDir, workspaceID)
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
	outputChan := make(chan outputlog.OutputLine, 100)
	writerDone := make(chan struct{})

	// Capture nohup subprocess's own stdout and stderr
	// Save original stdout/stderr
	origStdout := os.Stdout
	origStderr := os.Stderr

	// Create pipes for capturing nohup's own output
	nohupStdoutReader, nohupStdoutWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create nohup stdout pipe: %w", err)
	}
	nohupStderrReader, nohupStderrWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create nohup stderr pipe: %w", err)
	}

	// Redirect os.Stdout and os.Stderr to our pipes
	os.Stdout = nohupStdoutWriter
	os.Stderr = nohupStderrWriter

	// Also redirect slog to use the new stderr
	slog.SetDefault(slog.New(slog.NewTextHandler(nohupStderrWriter, nil)))

	// Start goroutines to read from nohup's own stdout/stderr
	nohupReadersDone := make(chan struct{}, 2)
	go readLines(nohupStdoutReader, "nohup-stdout", outputChan, nohupReadersDone)
	go readLines(nohupStderrReader, "nohup-stderr", outputChan, nohupReadersDone)

	// Defer cleanup: restore original stdout/stderr and close pipes
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
		_ = nohupStdoutWriter.Close()
		_ = nohupStderrWriter.Close()
		// Wait for nohup readers to finish draining
		<-nohupReadersDone
		<-nohupReadersDone
	}()

	// Create output type detector
	typeDetector := outputtype.NewDetector()

	// Start goroutine to write output lines to file
	go func() {
		defer close(writerDone)
		for line := range outputChan {
			// Write the line to output.log first
			formattedLine := outputlog.FormatOutputLine(line)
			_, _ = outFile.WriteString(formattedLine)
			// No need to sync since file was opened with O_SYNC

			// Only analyze stdout for type detection
			if line.Stream == "stdout" && !typeDetector.IsDetected() {
				if typeDetector.AnalyzeLine(line.Line) {
					// Type detected! Write output-type file immediately
					detectedType, detectionReason := typeDetector.GetDetectedType()
					outputTypeFile := filepath.Join(processDir, "output-type")
					outputTypeContent := fmt.Sprintf("%s,%s", detectedType, detectionReason)
					if err := os.WriteFile(outputTypeFile, []byte(outputTypeContent), 0600); err != nil {
						slog.Warn("Failed to write output-type file", "error", err)
					}
				}
			}
		}
	}()

	// Build the full command with pre-command if specified
	var fullCommand string
	var shellCmd []string
	if ws.PreCommand != "" {
		// Write pre-command to a temporary script file
		preScriptPath := filepath.Join(processDir, "pre-command.sh")
		if err := os.WriteFile(preScriptPath, []byte(ws.PreCommand), 0700); err != nil {
			return fmt.Errorf("failed to write pre-command script: %w", err)
		}

		// Extract shell from shebang (if present) to determine which shell to use
		shell := workspace.ExtractShellFromShebang(ws.PreCommand)

		// Source the pre-command script (to preserve environment) and then run user command
		fullCommand = fmt.Sprintf(". %s && %s", preScriptPath, proc.Command)
		shellCmd = []string{shell, "-c", fullCommand}
	} else {
		fullCommand = proc.Command
		shellCmd = []string{"sh", "-c", fullCommand}
	}

	// Create the command
	cmd := exec.Command(shellCmd[0], shellCmd[1:]...)
	cmd.Dir = ws.Directory

	// Check if this is a Claude command - if so, use pipes instead of PTY
	// to prevent Claude from detecting a terminal and showing its TUI interface
	isClaudeCommand := strings.Contains(proc.Command, "claude")

	var stderrPipe, stdoutPipe io.ReadCloser
	var stdinPipe io.WriteCloser
	var ptmx, tty *os.File

	if isClaudeCommand {
		// Use regular pipes for Claude to prevent TUI activation
		stderrPipe, err = cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("failed to create stderr pipe: %w", err)
		}

		stdoutPipe, err = cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdin pipe: %w", err)
		}

		// Start the command in a new session (detach from parent)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid: true,
		}
	} else {
		// Set up stderr pipe separately (bypasses PTY)
		stderrPipe, err = cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("failed to create stderr pipe: %w", err)
		}

		// Open a PTY manually so we can control which streams use it
		// We want stdout to use the PTY (for terminal capabilities)
		// But stderr should use the pipe (for separate capture)
		ptmx, tty, err = pty.Open()
		if err != nil {
			return fmt.Errorf("failed to open pty: %w", err)
		}
		defer func() { _ = ptmx.Close() }()
		defer func() { _ = tty.Close() }()

		// Assign PTY to stdin and stdout only (stderr uses the pipe)
		cmd.Stdin = tty
		cmd.Stdout = tty
		// cmd.Stderr is already set to stderrPipe above

		// Start the command in a new session (detach from parent)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid:  true,
			Setctty: true,
		}
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Handle PTY vs pipe setup based on command type
	var stdoutReader io.Reader
	var stdinWriter io.WriteCloser

	if isClaudeCommand {
		// For Claude commands using pipes
		stdoutReader = stdoutPipe
		stdinWriter = stdinPipe
	} else {
		// For regular commands using PTY
		// Close tty in parent process (child has its own copy)
		_ = tty.Close()

		// Set PTY size to a reasonable default (80x24 is standard terminal size)
		if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
			slog.Warn("Failed to set PTY size, using default", "error", err)
		}

		stdoutReader = ptmx
		stdinWriter = ptmx
	}

	// Start goroutine to read from stdout (either PTY or pipe)
	readersDone := make(chan struct{}, 2)
	go readLines(stdoutReader, "stdout", outputChan, readersDone)

	// Start goroutine to read from stderr pipe
	go readLines(stderrPipe, "stderr", outputChan, readersDone)

	pid := cmd.Process.Pid

	// Start goroutine to read from named pipe and forward to stdin
	// Started IMMEDIATELY after process starts, before any file I/O
	// to minimize the window where a writer might try to connect before reader is ready
	stdinDone := make(chan struct{})
	namedPipePath := filepath.Join(processDir, "stdin.pipe")
	go readStdinPipe(namedPipePath, stdinWriter, outputChan, stdinDone, isClaudeCommand)

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

	// Wait for both readers to finish draining (stdout from PTY, stderr from pipe)
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

	// Post-process Claude stream-json output if applicable
	if err := postProcessClaudeOutput(processDir, proc.Command); err != nil {
		slog.Warn("Failed to post-process Claude output", "error", err)
		// Continue anyway - this is not a fatal error
	}

	// Write exit status to file
	exitStatusFile := filepath.Join(processDir, "exit-status")
	if err := os.WriteFile(exitStatusFile, []byte(strconv.Itoa(exitCode)), 0600); err != nil {
		return fmt.Errorf("failed to write exit-status file: %w", err)
	}

	// Note: output-type file is written by the writer goroutine as soon as detection occurs

	// Update process metadata with exit information
	if err := workspace.UpdateProcessExit(ws, processHash, exitCode, signalName); err != nil {
		return fmt.Errorf("failed to update process exit: %w", err)
	}

	return nil
}

// sendOutputLine sends an output line to the channel with a timeout to prevent blocking
// Returns true if sent successfully, false if timed out or channel is closed
func sendOutputLine(outputChan chan<- outputlog.OutputLine, line outputlog.OutputLine, stream string) bool {
	defer func() {
		if r := recover(); r != nil {
			// Channel was closed while we were trying to send
			slog.Debug("Output channel closed during send", "stream", stream)
		}
	}()

	select {
	case outputChan <- line:
		return true
	case <-time.After(5 * time.Second):
		// Channel is full and writer can't keep up - log warning and drop the line
		slog.Warn("Output channel write timed out, dropping line", "stream", stream)
		return false
	}
}

// readLines reads lines from a reader and sends them to the output channel
// This function flushes partial lines (without newlines) after a timeout to support
// interactive programs that output prompts without trailing newlines (e.g., "Enter filename: ")
func readLines(reader io.Reader, stream string, outputChan chan<- outputlog.OutputLine, done chan<- struct{}) {
	defer func() {
		select {
		case done <- struct{}{}:
		case <-time.After(1 * time.Second):
			slog.Warn("Failed to send done signal", "stream", stream)
		}
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
				sendOutputLine(outputChan, outputlog.OutputLine{
					Stream:    stream,
					Timestamp: time.Now().UTC(),
					Line:      string(buffer),
				}, stream)
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
			timer := time.NewTimer(flushTimeout)
			<-timer.C
			// Timeout expired, check if we should flush
			if br.Buffered() == 0 {
				shouldFlush = true
			}
		}

		if shouldFlush {
			// Keep the line as-is, including newline if present
			// The length field in the output format will indicate if newline is included
			line := string(buffer)

			sendOutputLine(outputChan, outputlog.OutputLine{
				Stream:    stream,
				Timestamp: time.Now().UTC(),
				Line:      line,
			}, stream)
			buffer = buffer[:0] // Reset buffer
		}
	}
}

// readStdinPipe reads from a named pipe and forwards data to process stdin and output.log
// This runs in the background and exits when the process ends or stdin write fails
// It continuously reopens the pipe to handle multiple writers (each HTTP request opens/closes)
// useNonBlocking enables non-blocking open for commands that need immediate stdin connection
func readStdinPipe(pipePath string, stdinWriter io.WriteCloser, outputChan chan<- outputlog.OutputLine, done chan<- struct{}, useNonBlocking bool) {
	defer func() {
		close(done)
		_ = stdinWriter.Close()
	}()

	// Keep reading from the pipe until the process exits
	for {
		var file *os.File
		var err error

		if useNonBlocking {
			// Open the named pipe for reading in non-blocking mode first
			// This prevents blocking the process startup when no writers are present
			// Used for Claude commands which need stdin connected immediately
			file, err = os.OpenFile(pipePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				slog.Error("Failed to open stdin pipe for reading", "error", err, "path", pipePath)
				return
			}

			// Switch back to blocking mode for actual reading
			// This allows us to block on read() calls but not on open()
			if err := syscall.SetNonblock(int(file.Fd()), false); err != nil {
				slog.Warn("Failed to set stdin pipe to blocking mode", "error", err)
				_ = file.Close()
				return
			}
		} else {
			// Open in blocking mode (traditional behavior)
			// This will block until a writer opens the pipe
			file, err = os.OpenFile(pipePath, os.O_RDONLY, 0)
			if err != nil {
				slog.Error("Failed to open stdin pipe for reading", "error", err, "path", pipePath)
				return
			}
		}

		// Read lines from the pipe until this writer closes it
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()

			// Write to process stdin with timeout to avoid blocking
			stdinData := []byte(line + "\n")
			writeDone := make(chan error, 1)
			go func() {
				_, err := stdinWriter.Write(stdinData)
				writeDone <- err
			}()

			select {
			case err := <-writeDone:
				if err != nil {
					// Process stdin closed, stop reading
					_ = file.Close()
					return
				}
			case <-time.After(5 * time.Second):
				// Write timed out
				slog.Warn("Stdin write timed out", "line", line)
				_ = file.Close()
				return
			}

			// Also log to output.log (non-blocking)
			sendOutputLine(outputChan, outputlog.OutputLine{
				Stream:    "stdin",
				Timestamp: time.Now().UTC(),
				Line:      line,
			}, "stdin")
		}

		// Close this instance of the pipe
		_ = file.Close()

		// The scanner exited because the writer closed the pipe (EOF)
		// Loop back to reopen the pipe and wait for the next writer
	}
}

// postProcessClaudeOutput processes Claude CLI stream-json output
// It extracts the markdown text from the JSON stream and updates the output file
func postProcessClaudeOutput(processDir, command string) error {
	// Only process Claude commands
	if !strings.Contains(command, "claude") {
		return nil
	}

	outputFile := filepath.Join(processDir, "output.log")

	// Read the combined output
	stdout, _, _, err := outputlog.ReadCombinedOutput(outputFile)
	if err != nil {
		return fmt.Errorf("failed to read output: %w", err)
	}

	// Parse the stream-json and extract markdown text
	markdownText := claude.ParseStreamJSON(stdout)

	if markdownText == "" {
		return nil // No text extracted
	}

	// Write the processed markdown output back
	// We need to reconstruct the output.log format with just stdout containing markdown
	outFile, err := os.OpenFile(outputFile, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open output file for writing: %w", err)
	}
	defer func() { _ = outFile.Close() }()

	// Write markdown as stdout lines
	for _, line := range strings.Split(markdownText, "\n") {
		outputLine := outputlog.OutputLine{
			Stream:    "stdout",
			Timestamp: time.Now().UTC(),
			Line:      line + "\n",
		}
		formattedLine := outputlog.FormatOutputLine(outputLine)
		if _, err := outFile.WriteString(formattedLine); err != nil {
			return fmt.Errorf("failed to write output line: %w", err)
		}
	}

	// Force output-type to markdown
	outputTypeFile := filepath.Join(processDir, "output-type")
	outputTypeContent := fmt.Sprintf("%s,%s", outputtype.OutputTypeMarkdown, "Claude CLI output processed")
	if err := os.WriteFile(outputTypeFile, []byte(outputTypeContent), 0600); err != nil {
		return fmt.Errorf("failed to write output-type file: %w", err)
	}

	return nil
}
