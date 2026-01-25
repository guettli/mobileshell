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
	"sync"
	"sync/atomic"
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
	var processHolder *os.Process // Store process reference for signal delivery
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
		// processHolder will be set after cmd.Start()
		go acceptSocketConnections(socketListener, outputLogWriter.Channel(), &processHolder)
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

	// Set process reference for signal delivery via Unix socket
	if inputUnixDomainSocket != "" {
		processHolder = cmd.Process
	}

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
	var detectedWritten atomic.Int32

	// WaitGroup to ensure stdout/stderr goroutines finish before closing writer
	var streamWg sync.WaitGroup

	// Copy stdout from PTY to output log with type detection
	stdoutWriter := outputLogWriter.StreamWriter("stdout")
	streamWg.Add(1)
	go func() {
		defer streamWg.Done()
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
							detectedWritten.Store(1)
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
	streamWg.Add(1)
	go func() {
		defer streamWg.Done()
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

	// Wait for stdout/stderr goroutines to finish before closing the writer
	streamWg.Wait()
	outputLogWriter.Close()

	// Write output type detection results if not already written
	if detector.IsDetected() && detectedWritten.Load() == 0 {
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
func acceptSocketConnections(listener net.Listener, outputChan chan<- outputlog.Chunk, processHolder **os.Process) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Listener closed, exit gracefully
			slog.Info("Socket listener closed", "error", err)
			return
		}

		// Handle each connection in a separate goroutine
		go handleSocketConnection(conn, outputChan, processHolder)
	}
}

// handleSocketConnection processes a single connection to the Unix domain socket
func handleSocketConnection(conn net.Conn, outputChan chan<- outputlog.Chunk, processHolder **os.Process) {
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

		// Handle signal stream - send signal to process
		if chunk.Stream == "signal" {
			if processHolder != nil && *processHolder != nil {
				signalName := string(chunk.Line)
				signalName = strings.TrimSpace(signalName)
				slog.Info("Received signal request via Unix socket", "signal", signalName)

				// Parse signal name to syscall.Signal
				sig, err := parseSignal(signalName)
				if err != nil {
					slog.Error("Failed to parse signal", "error", err, "signal", signalName)
					continue
				}

				// Send signal to process
				if err := (*processHolder).Signal(sig); err != nil {
					slog.Error("Failed to send signal to process", "error", err, "signal", signalName)
				} else {
					slog.Info("Signal sent to process successfully", "signal", signalName, "pid", (*processHolder).Pid)
				}
			} else {
				slog.Warn("Cannot send signal: process not started yet")
			}
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

// parseSignal converts a signal name string to syscall.Signal
func parseSignal(signalName string) (syscall.Signal, error) {
	signalName = strings.TrimSpace(signalName)

	// Try parsing as a number first
	if num, err := strconv.Atoi(signalName); err == nil {
		if num < 0 || num > 64 {
			return 0, fmt.Errorf("signal number out of range: %d", num)
		}
		return syscall.Signal(num), nil
	}

	signalName = strings.ToUpper(signalName)

	// Remove "SIG" prefix if present
	signalName = strings.TrimPrefix(signalName, "SIG")

	// Map signal names to numbers
	switch signalName {
	case "EXIT":
		return syscall.Signal(0), nil
	case "HUP":
		return syscall.SIGHUP, nil
	case "INT":
		return syscall.SIGINT, nil
	case "QUIT":
		return syscall.SIGQUIT, nil
	case "ILL":
		return syscall.SIGILL, nil
	case "TRAP":
		return syscall.SIGTRAP, nil
	case "ABRT":
		return syscall.SIGABRT, nil
	case "BUS":
		return syscall.SIGBUS, nil
	case "FPE":
		return syscall.SIGFPE, nil
	case "KILL":
		return syscall.SIGKILL, nil
	case "USR1":
		return syscall.SIGUSR1, nil
	case "SEGV":
		return syscall.SIGSEGV, nil
	case "USR2":
		return syscall.SIGUSR2, nil
	case "PIPE":
		return syscall.SIGPIPE, nil
	case "ALRM":
		return syscall.SIGALRM, nil
	case "TERM":
		return syscall.SIGTERM, nil
	case "STKFLT":
		return syscall.SIGSTKFLT, nil
	case "CHLD":
		return syscall.SIGCHLD, nil
	case "CONT":
		return syscall.SIGCONT, nil
	case "STOP":
		return syscall.SIGSTOP, nil
	case "TSTP":
		return syscall.SIGTSTP, nil
	case "TTIN":
		return syscall.SIGTTIN, nil
	case "TTOU":
		return syscall.SIGTTOU, nil
	case "URG":
		return syscall.SIGURG, nil
	case "XCPU":
		return syscall.SIGXCPU, nil
	case "XFSZ":
		return syscall.SIGXFSZ, nil
	case "VTALRM":
		return syscall.SIGVTALRM, nil
	case "PROF":
		return syscall.SIGPROF, nil
	case "WINCH":
		return syscall.SIGWINCH, nil
	case "POLL", "IO":
		return syscall.SIGIO, nil
	case "PWR":
		return syscall.SIGPWR, nil
	case "SYS":
		return syscall.SIGSYS, nil
	default:
		return 0, fmt.Errorf("unknown signal: %s", signalName)
	}
}
