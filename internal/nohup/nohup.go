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
	"syscall"
	"time"

	"mobileshell/pkg/outputlog"
	"mobileshell/pkg/outputtype"

	"github.com/creack/pty"
)

// Run executes a command in nohup mode within a workspace This function is called by the
// `mobileshell nohup` subcommand. During a http request executor.Execute() gets called, which calls
// nohup (and Run()).
func Run(commandSlice []string, inputUnixDomainSocket string, workingDirectory string) error {
	slog.Info("nohup.Run called", "commandSlice", commandSlice, "socketPath", inputUnixDomainSocket)
	if len(commandSlice) < 1 {
		return fmt.Errorf("not enough arguments")
	}
	command := filepath.Clean(commandSlice[0])
	processDir := filepath.Dir(command)
	if processDir == "." {
		return fmt.Errorf("failed to run as nohup. A containing directory is needed: %q", command)
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

	// Create the command
	cmd := exec.Command(commandSlice[0], commandSlice[1:]...)
	if workingDirectory != "" {
		cmd.Dir = workingDirectory
	}

	var ptmx, tty *os.File

	// Open a PTY for all commands
	ptmx, tty, err = pty.Open()
	if err != nil {
		return fmt.Errorf("failed to open pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Set PTY size to a reasonable default
	_ = pty.Setsize(ptmx, &pty.Winsize{
		Rows: 24,
		Cols: 80,
	})

	onChunk := func(chunk *outputlog.Chunk) {
		slog.Info("recevied chunk",
			"stream", chunk.Stream,
			"time", chunk.Timestamp,
			"line", string(chunk.Line[:min(10, len(chunk.Line))]),
		)
		if chunk.Stream == "stdin" {
			_, err := ptmx.Write(chunk.Line)
			if err != nil {
				slog.Error("ptmx.Write(chunk.Line)", "error", err.Error())
			}
		}
	}
	outputLogWriter := outputlog.NewOutputLogWriter(outFile, onChunk)

	// Handle input from Unix domain socket if provided
	var socketListener net.Listener
	if inputUnixDomainSocket != "" {
		// Create and listen on Unix domain socket for stdin input
		// Remove existing socket file if it exists
		_ = os.Remove(inputUnixDomainSocket)

		var err error
		socketListener, err = net.Listen("unix", inputUnixDomainSocket)
		if err != nil {
			return fmt.Errorf("failed to create Unix domain socket listener: %w", err)
		}

		// Accept connections and read stdin data from the socket
		go acceptSocketConnections(socketListener, outputLogWriter.Channel())
	} else {
		// Read input from stdin. Do not read outputlog format. Read from stdin, emit Chunks from
		// stream "stdin".
		stdinReaderToChannel := outputLogWriter.StreamWriter("stdin")
		go func() {
			_, err := io.Copy(stdinReaderToChannel, os.Stdin)
			if err != nil {
				slog.Error("io.Copy(stdinReaderToChannel, os.Stdin)", "error", err)
			}
			slog.Info("os.Stdin was closed")
		}()
	}

	var stderrPipe io.ReadCloser

	// Set up stderr pipe separately (bypasses PTY)
	stderrPipe, err = cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Assign PTY to stdin and stdout
	cmd.Stdin = tty
	cmd.Stdout = tty
	// cmd.Stderr uses the pipe

	// Start the command in a new session (detach from parent)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Close tty in parent process (child process has it)
	_ = tty.Close()

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

	// Create output type detector
	detector := outputtype.NewDetector()
	detectedWritten := false

	// Copy stdout from PTY to output log with type detection
	stdoutWriter := outputLogWriter.StreamWriter("stdout")
	go func() {
		// Use a buffered reader to scan lines
		reader := bufio.NewReader(ptmx)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				// Analyze line for output type detection
				if !detector.IsDetected() {
					if detector.AnalyzeLine(line) {
						// Type detected - write immediately
						outputType, reason := detector.GetDetectedType()
						outputTypeFile := filepath.Join(processDir, "output-type")
						outputTypeContent := fmt.Sprintf("%s,%s", outputType, reason)
						if writeErr := os.WriteFile(outputTypeFile, []byte(outputTypeContent), 0o600); writeErr != nil {
							slog.Warn("Failed to write output-type file", "error", writeErr)
						} else {
							detectedWritten = true
						}
					}
				}
				// Write to output log
				if _, writeErr := stdoutWriter.Write([]byte(line)); writeErr != nil {
					slog.Error("Failed to write stdout", "error", writeErr)
				}
			}
			if err != nil {
				if err != io.EOF {
					slog.Error("Error reading stdout", "error", err)
				}
				break
			}
		}
	}()

	// Copy stderr from pipe to output log
	stderrWriter := outputLogWriter.StreamWriter("stderr")
	go func() {
		_, err := io.Copy(stderrWriter, stderrPipe)
		if err != nil {
			slog.Error("io.Copy(stderrWriter, stderrPipe)", "error", err)
		}
	}()

	// Wait for the process to complete
	err = cmd.Wait()

	// Clean up Unix domain socket if it was created
	if socketListener != nil {
		_ = socketListener.Close()
		if inputUnixDomainSocket != "" {
			_ = os.Remove(inputUnixDomainSocket)
		}
	}

	outputLogWriter.Close()

	// Write output type detection results if not already written
	if detector.IsDetected() && !detectedWritten {
		outputType, reason := detector.GetDetectedType()
		outputTypeFile := filepath.Join(processDir, "output-type")
		outputTypeContent := fmt.Sprintf("%s,%s", outputType, reason)
		if err := os.WriteFile(outputTypeFile, []byte(outputTypeContent), 0o600); err != nil {
			slog.Warn("Failed to write output-type file", "error", err)
		}
	}

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

// acceptSocketConnections listens for connections on a Unix domain socket and processes stdin input
// It reads OutputLog formatted data and logs all chunks (stdin is NOT forwarded to the command)
func acceptSocketConnections(listener net.Listener, outputChan chan<- outputlog.Chunk) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Listener closed, exit gracefully
			slog.Info("Socket listener closed", "error", err)
			return
		}

		// Handle each connection in a separate goroutine
		go handleSocketConnection(conn, outputChan)
	}
}

// handleSocketConnection processes a single connection to the Unix domain socket
func handleSocketConnection(conn net.Conn, outputChan chan<- outputlog.Chunk) {
	defer func() { _ = conn.Close() }()

	slog.Info("Client connected to Unix domain socket")

	// Create OutputLog reader from the connection
	reader, err := outputlog.NewOutputLogReader(conn)
	if err != nil {
		slog.Error("Failed to create OutputLog reader", "error", err)
		return
	}

	// Read chunks from the channel and log them (stdin is NOT forwarded to command)
	for chunk := range reader.Channel() {
		if chunk.Error != nil {
			slog.Error("Error reading chunk from Unix socket", "error", chunk.Error)
			continue
		}

		// Send chunk to output channel for logging (non-blocking)
		select {
		case outputChan <- chunk:
			// Successfully sent to log
		case <-time.After(5 * time.Second):
			slog.Warn("Output channel write timed out, dropping chunk from Unix socket", "stream", chunk.Stream)
		}
	}

	slog.Info("Unix domain socket connection closed")
}
