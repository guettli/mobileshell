package nohup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mobileshell/pkg/outputlog"
	"mobileshell/pkg/outputtype"

	"github.com/creack/pty"
)

// Run executes a command in nohup mode within a workspace This function is called by the
// `mobileshell nohup` subcommand. During a http request executor.Execute() gets called, which calls
// nohup (and Run()).
func Run(commandSlice []string, inputUnixDomainSocket string) error {
	if len(commandSlice) < 1 {
		return fmt.Errorf("not enough arguments")
	}
	command := filepath.Clean(commandSlice[0])
	processDir := filepath.Dir(command)
	if processDir == "." {
		return fmt.Errorf("failed to run as nohup. A containing directory is needed: %q", command)
	}

	// Write command file
	if err := os.WriteFile(filepath.Join(processDir, "cmd"),
		[]byte(strings.Join(commandSlice, "\b")), 0o600); err != nil {
		return fmt.Errorf("failed to write cmd file: %w", err)
	}

	// Write completed file
	if err := os.WriteFile(filepath.Join(processDir, "completed"), []byte("false"), 0o600); err != nil {
		return fmt.Errorf("failed to write completed file: %w", err)
	}

	// Open combined output file
	outputFile := filepath.Join(processDir, "output.log")
	_, err := os.Stat(outputFile)
	if err == nil {
		return fmt.Errorf("%q does already exist. This is not supported", outputFile)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat of output.log: %q: %w", outputFile, err)
	}

	outFile, err := os.OpenFile(outputFile, os.O_WRONLY|os.O_APPEND|os.O_SYNC|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open output.log file: %w", err)
	}
	defer func() { _ = outFile.Close() }()

	// Create channel for output chunks
	outputChan := make(chan outputlog.Chunk, 100)
	writerDone := make(chan struct{})

	// Create output type detector
	typeDetector := outputtype.NewDetector()

	// Handle input from Unix domain socket if provided
	if inputUnixDomainSocket != "" {
		// Open Unix domain socket and read via OutputLog format
		go readFromUnixSocket(inputUnixDomainSocket, outputChan)
	}

	// Start goroutine to write output chunks to file
	go func() {
		defer close(writerDone)
		for chunk := range outputChan {
			// Write the chunk to output.log first
			formattedChunk := outputlog.FormatChunk(chunk)
			_, _ = outFile.Write(formattedChunk)
			// No need to sync since file was opened with O_SYNC

			// Only analyze stdout for type detection
			if chunk.Stream == "stdout" && !typeDetector.IsDetected() {
				if typeDetector.AnalyzeLine(string(chunk.Line)) {
					// Type detected! Write output-type file immediately
					detectedType, detectionReason := typeDetector.GetDetectedType()
					outputTypeFile := filepath.Join(processDir, "output-type")
					outputTypeContent := fmt.Sprintf("%s,%s", detectedType, detectionReason)
					if err := os.WriteFile(outputTypeFile, []byte(outputTypeContent), 0o600); err != nil {
						slog.Warn("Failed to write output-type file", "error", err)
					}
				}
			}
		}
	}()

	// Create the command
	cmd := exec.Command(commandSlice[0], commandSlice[1:]...)

	var stderrPipe io.ReadCloser
	var ptmx, tty *os.File
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

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Handle PTY vs pipe setup based on command type
	var stdoutReader io.Reader

	// For regular commands using PTY
	// Close tty in parent process (child has its own copy)
	_ = tty.Close()

	// Set PTY size to a reasonable default (80x24 is standard terminal size)
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		slog.Warn("Failed to set PTY size, using default", "error", err)
	}

	stdoutReader = ptmx

	// Start goroutine to read from stdout (either PTY or pipe)
	readersDone := make(chan struct{}, 2)
	go readLines(stdoutReader, "stdout", outputChan, readersDone)

	// Start goroutine to read from stderr pipe
	go readLines(stderrPipe, "stderr", outputChan, readersDone)

	pid := cmd.Process.Pid

	// Write PID to file
	pidFile := filepath.Join(processDir, "pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	// Update status file
	if err := os.WriteFile(filepath.Join(processDir, "status"), []byte("running"), 0o600); err != nil {
		return fmt.Errorf("failed to write status file: %w", err)
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

	// Write exit status to file
	exitStatusFile := filepath.Join(processDir, "exit-status")
	if err := os.WriteFile(exitStatusFile, []byte(strconv.Itoa(exitCode)), 0o600); err != nil {
		return fmt.Errorf("failed to write exit-status file: %w", err)
	}

	// Write signal file if process was terminated by signal
	if signalName != "" {
		if err := os.WriteFile(filepath.Join(processDir, "signal"), []byte(signalName), 0o600); err != nil {
			return fmt.Errorf("failed to write signal file: %w", err)
		}
	}

	// Write endtime file
	endTime := time.Now().UTC().Format(outputlog.TimeFormatRFC3339NanoUTC)
	if err := os.WriteFile(filepath.Join(processDir, "endtime"), []byte(endTime), 0o600); err != nil {
		return fmt.Errorf("failed to write endtime file: %w", err)
	}

	// Update completed file
	if err := os.WriteFile(filepath.Join(processDir, "completed"), []byte("true"), 0o600); err != nil {
		return fmt.Errorf("failed to write completed file: %w", err)
	}

	return nil
}

// sendOutputChunk sends an output chunk to the channel with a timeout to prevent blocking
// Returns true if sent successfully, false if timed out or channel is closed
func sendOutputChunk(outputChan chan<- outputlog.Chunk, chunk outputlog.Chunk, stream string) bool {
	defer func() {
		if r := recover(); r != nil {
			// Channel was closed while we were trying to send
			slog.Debug("Output channel closed during send", "stream", stream)
		}
	}()

	select {
	case outputChan <- chunk:
		return true
	case <-time.After(5 * time.Second):
		// Channel is full and writer can't keep up - log warning and drop the chunk
		slog.Warn("Output channel write timed out, dropping chunk", "stream", stream)
		return false
	}
}

// readLines reads lines from a reader and sends them to the output channel
// This function flushes partial lines (without newlines) after a timeout to support
// interactive programs that output prompts without trailing newlines (e.g., "Enter filename: ")
func readLines(reader io.Reader, stream string, outputChan chan<- outputlog.Chunk, done chan<- struct{}) {
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
				sendOutputChunk(outputChan, outputlog.Chunk{
					Stream:    stream,
					Timestamp: time.Now().UTC(),
					Line:      append([]byte(nil), buffer...),
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
			sendOutputChunk(outputChan, outputlog.Chunk{
				Stream:    stream,
				Timestamp: time.Now().UTC(),
				Line:      append([]byte(nil), buffer...),
			}, stream)
			buffer = buffer[:0] // Reset buffer
		}
	}
}

// readFromUnixSocket connects to a Unix domain socket and reads OutputLog formatted data
// It forwards all chunks received from the socket to the outputChan
func readFromUnixSocket(socketPath string, outputChan chan<- outputlog.Chunk) {
	// Connect to the Unix domain socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		slog.Error("Failed to connect to Unix domain socket", "error", err, "path", socketPath)
		return
	}
	defer func() { _ = conn.Close() }()

	slog.Info("Connected to Unix domain socket", "path", socketPath)

	// Create OutputLog reader from the connection
	reader, err := outputlog.NewOutputLogReader(conn)
	if err != nil {
		slog.Error("Failed to create OutputLog reader", "error", err)
		return
	}

	// Read chunks from the channel and forward to outputChan
	for chunk := range reader.Channel() {
		if chunk.Error != nil {
			slog.Error("Error reading chunk from Unix socket", "error", chunk.Error)
			continue
		}

		// Send chunk to output channel (non-blocking)
		select {
		case outputChan <- chunk:
			// Successfully sent
		case <-time.After(5 * time.Second):
			slog.Warn("Output channel write timed out, dropping chunk from Unix socket", "stream", chunk.Stream)
		}
	}

	slog.Info("Unix domain socket connection closed", "path", socketPath)
}
