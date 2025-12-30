package nohup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"mobileshell/internal/workspace"
)

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

	// Open stdout and stderr files
	stdoutFile := filepath.Join(processDir, "stdout")
	stderrFile := filepath.Join(processDir, "stderr")

	stdout, err := os.OpenFile(stdoutFile, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open stdout file: %w", err)
	}
	defer func() { _ = stdout.Close() }()

	stderr, err := os.OpenFile(stderrFile, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open stderr file: %w", err)
	}
	defer func() { _ = stderr.Close() }()

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
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = nil

	// Detach from parent process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	pid := cmd.Process.Pid

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
	if err := os.WriteFile(exitStatusFile, []byte(strconv.Itoa(exitCode)), 0600); err != nil {
		return fmt.Errorf("failed to write exit-status file: %w", err)
	}

	// Update process metadata with exit information
	if err := workspace.UpdateProcessExit(ws, processHash, exitCode, signalName); err != nil {
		return fmt.Errorf("failed to update process exit: %w", err)
	}

	return nil
}
