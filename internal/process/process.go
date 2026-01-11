package process

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"mobileshell/internal/outputlog"
)

// InitializeProcessDir initializes the process directory with metadata files
func InitializeProcessDir(processDir string, command string) error {
	// Create process directory
	if err := os.MkdirAll(processDir, 0700); err != nil {
		return fmt.Errorf("failed to create process directory: %w", err)
	}

	// Write command file
	if err := os.WriteFile(filepath.Join(processDir, "cmd"), []byte(command), 0600); err != nil {
		return fmt.Errorf("failed to write cmd file: %w", err)
	}

	// Write starttime file
	startTime := time.Now().UTC()
	if err := os.WriteFile(filepath.Join(processDir, "starttime"), []byte(startTime.Format(time.RFC3339Nano)), 0600); err != nil {
		return fmt.Errorf("failed to write starttime file: %w", err)
	}

	// Write completed file
	if err := os.WriteFile(filepath.Join(processDir, "completed"), []byte("false"), 0600); err != nil {
		return fmt.Errorf("failed to write completed file: %w", err)
	}

	// Create named pipe for stdin
	stdinPipe := filepath.Join(processDir, "stdin.pipe")
	if _, err := os.Stat(stdinPipe); os.IsNotExist(err) {
		if err := syscall.Mkfifo(stdinPipe, 0600); err != nil {
			return fmt.Errorf("failed to create stdin pipe: %w", err)
		}
	}

	// Create empty output.log file if it doesn't exist
	outputLog := filepath.Join(processDir, "output.log")
	if _, err := os.Stat(outputLog); os.IsNotExist(err) {
		if err := os.WriteFile(outputLog, []byte{}, 0600); err != nil {
			return fmt.Errorf("failed to create output.log file: %w", err)
		}
	}

	return nil
}

// UpdateProcessPIDInDir updates the PID of a running process in the given directory
func UpdateProcessPIDInDir(processDir string, pid int) error {
	// Write PID file
	if err := os.WriteFile(filepath.Join(processDir, "pid"), []byte(strconv.Itoa(pid)), 0600); err != nil {
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	// Update status file
	if err := os.WriteFile(filepath.Join(processDir, "status"), []byte("running"), 0600); err != nil {
		return fmt.Errorf("failed to write status file: %w", err)
	}

	return nil
}

// UpdateProcessExitInDir updates a process state in the given directory when it exits
func UpdateProcessExitInDir(processDir string, exitCode int, signal string) error {
	// Write exit-status file
	if err := os.WriteFile(filepath.Join(processDir, "exit-status"), []byte(strconv.Itoa(exitCode)), 0600); err != nil {
		return fmt.Errorf("failed to write exit-status file: %w", err)
	}

	// Write signal file if process was terminated by signal
	if signal != "" {
		if err := os.WriteFile(filepath.Join(processDir, "signal"), []byte(signal), 0600); err != nil {
			return fmt.Errorf("failed to write signal file: %w", err)
		}
	}

	// Detect and write content type
	outputFile := filepath.Join(processDir, "output.log")
	if data, err := outputlog.ReadRawStdout(outputFile); err == nil && len(data) > 0 {
		// Use http.DetectContentType directly
		limit := 512
		if len(data) > limit {
			data = data[:limit]
		}
		contentType := http.DetectContentType(data)
		if err := os.WriteFile(filepath.Join(processDir, "content-type"), []byte(contentType), 0600); err != nil {
			return fmt.Errorf("failed to write content-type file: %w", err)
		}
	}

	// Write endtime file
	endTime := time.Now().UTC().Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(processDir, "endtime"), []byte(endTime), 0600); err != nil {
		return fmt.Errorf("failed to write endtime file: %w", err)
	}

	// Update completed file
	if err := os.WriteFile(filepath.Join(processDir, "completed"), []byte("true"), 0600); err != nil {
		return fmt.Errorf("failed to write completed file: %w", err)
	}

	return nil
}
