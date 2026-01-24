package executor

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mobileshell/internal/process"
	"mobileshell/internal/workspace"
	"mobileshell/pkg/outputlog"
)

func InitExecutor(stateDir string) error {
	// Initialize workspace storage
	return workspace.InitWorkspaces(stateDir)
}

// findProjectRoot finds the project root by looking for go.mod file
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk up the directory tree looking for go.mod
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding go.mod
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}

// CreateWorkspace creates a new workspace
func CreateWorkspace(stateDir, name, directory, preCommand string) (*workspace.Workspace, error) {
	return workspace.CreateWorkspace(stateDir, name, directory, preCommand)
}

// GetWorkspaceByID retrieves a workspace by its ID
func GetWorkspaceByID(stateDir, id string) (*workspace.Workspace, error) {
	return workspace.GetWorkspaceByID(stateDir, id)
}

// Execute spawns a new process in the given workspace. It uses exec.Command() to call the nohup
// subcommand. It does not wait for completion.
func Execute(ws *workspace.Workspace, command string) (*process.Process, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is nil")
	}

	// Get the path to the current executable
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Generate hash for the process
	commandId := time.Now().UTC().Format(outputlog.TimeFormatRFC3339NanoUTC)

	processDir := filepath.Join(ws.Path, "processes", commandId)
	if err := os.MkdirAll(processDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create process directory: %w", err)
	}

	proc := &process.Process{
		CommandId:  commandId,
		Command:    command,
		Completed:  false,
		ProcessDir: processDir,
		OutputFile: filepath.Join(processDir, "output.log"),
	}

	cmdPath := filepath.Join(processDir, "cmd")
	err = os.WriteFile(cmdPath, []byte(command), 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to write %q: %w", cmdPath, err)
	}

	// Write starttime file
	startTime := time.Now().UTC().Format(outputlog.TimeFormatRFC3339NanoUTC)
	if err := os.WriteFile(filepath.Join(processDir, "starttime"), []byte(startTime), 0o600); err != nil {
		return nil, fmt.Errorf("failed to write starttime file: %w", err)
	}

	// Create script
	var nohupCommand string
	if ws.PreCommand == "" {
		nohupCommand = "#!/usr/bin/env bash"
	} else {
		nohupCommand = ws.PreCommand
	}

	nohupCommandPath := filepath.Join(processDir, "nohup-command")
	if err := os.WriteFile(nohupCommandPath,
		[]byte(nohupCommand+"\n"+command), 0o700); err != nil {
		return nil, fmt.Errorf("failed to write nohup-command file: %w", err)
	}

	// Spawn the process using `mobileshell nohup` in the background
	// In test mode, use `go run` to execute the mobileshell command

	// Use a shorter socket path to avoid Unix socket path length limit (108 chars)
	// Store the socket in /tmp with a unique name based on the process timestamp
	socketPath := filepath.Join("/tmp", "ms-"+commandId+".sock")

	args := []string{
		"nohup",
		"--input-unix-domain-socket", socketPath,
		"--working-directory", ws.Directory,
		nohupCommandPath,
	}
	if filepath.Ext(execPath) == ".test" {
		// Use ./cmd/mobileshell for go run (works from project root)
		cmd := []string{"run", "./cmd/mobileshell"}
		cmd = append(cmd, args...)
		goCmd := exec.Command("go", cmd...)
		// Set working directory to project root (where go.mod is)
		projectRoot, err := findProjectRoot()
		if err != nil {
			return nil, fmt.Errorf("failed to find project root: %w", err)
		}
		goCmd.Dir = projectRoot
		proc.ExecCmd = goCmd
	} else {
		proc.ExecCmd = exec.Command(execPath, args...)
	}

	// Redirect stdout and stderr to nohup.log
	nohupLogPath := filepath.Join(processDir, "nohup.log")
	nohupLogFile, err := os.OpenFile(nohupLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to create nohup.log: %w", err)
	}
	proc.ExecCmd.Stdout = nohupLogFile
	proc.ExecCmd.Stderr = nohupLogFile
	proc.ExecCmd.Stdin = nil

	// Log the command being executed
	cmdStr := proc.ExecCmd.String()
	slog.Info("Starting nohup process", "command", cmdStr, "dir", proc.ExecCmd.Dir)

	if err := proc.ExecCmd.Start(); err != nil {
		_ = nohupLogFile.Close()
		return nil, fmt.Errorf("failed to spawn nohup process: %w", err)
	}

	return proc, nil
}

// DetectContentType detects the MIME type of stdout data
func DetectContentType(data []byte) string {
	// http.DetectContentType uses at most the first 512 bytes
	if len(data) > 512 {
		data = data[:512]
	}
	return http.DetectContentType(data)
}
