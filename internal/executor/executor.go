package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mobileshell/internal/workspace"
)

type Process struct {
	ID          string    `json:"id"`
	Command     string    `json:"command"`
	StartTime   time.Time `json:"start_time"`
	OutputFile  string    `json:"output_file"`
	PID         int       `json:"pid"`
	Status      string    `json:"status"` // "running", "completed", or "pending"
	WorkspaceTS string    `json:"workspace_timestamp"`
	Hash        string    `json:"hash"`
	ExitCode    *int      `json:"exit_code,omitempty"`
	Signal      string    `json:"signal,omitempty"`
	EndTime     time.Time `json:"end_time,omitempty"`
}

func InitExecutor(stateDir string) error {
	// Initialize workspace storage
	return workspace.InitWorkspaces(stateDir)
}

// CreateWorkspace creates a new workspace
func CreateWorkspace(stateDir, name, directory, preCommand string) (*workspace.Workspace, error) {
	return workspace.CreateWorkspace(stateDir, name, directory, preCommand)
}

// GetWorkspaceByID retrieves a workspace by its ID
func GetWorkspaceByID(stateDir, id string) (*workspace.Workspace, error) {
	return workspace.GetWorkspaceByID(stateDir, id)
}

// Execute spawns a new process in the given workspace
func Execute(stateDir string, ws *workspace.Workspace, command string) (*Process, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is nil")
	}

	// Get the path to the current executable
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Create process in workspace
	hash, err := workspace.CreateProcess(ws, command)
	if err != nil {
		return nil, fmt.Errorf("failed to create process: %w", err)
	}

	// Get workspace timestamp from path
	workspaceTS := filepath.Base(ws.Path)

	// Spawn the process using `mobileshell nohup` in the background
	cmd := exec.Command(execPath, "nohup", "--state-dir", stateDir, workspaceTS, hash)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to spawn nohup process: %w", err)
	}

	// Don't wait for the nohup process - let it run independently
	go func() {
		_ = cmd.Wait()
	}()

	// Get process directory for file paths
	processDir := workspace.GetProcessDir(ws, hash)

	proc := &Process{
		ID:          hash,
		Command:     command,
		StartTime:   time.Now().UTC(),
		OutputFile:  filepath.Join(processDir, "output.log"),
		Status:      "pending",
		WorkspaceTS: workspaceTS,
		Hash:        hash,
	}

	return proc, nil
}

// ListProcesses returns all processes from all workspaces
func ListProcesses(stateDir string) []*Process {
	var allProcesses []*Process

	// Get all workspaces
	workspaces, err := workspace.ListWorkspaces(stateDir)
	if err != nil {
		return allProcesses
	}

	for _, ws := range workspaces {
		workspaceProcs, err := workspace.ListProcesses(ws)
		if err != nil {
			continue
		}

		workspaceTS := filepath.Base(ws.Path)

		for _, wp := range workspaceProcs {
			processDir := workspace.GetProcessDir(ws, wp.Hash)

			proc := &Process{
				ID:          wp.Hash,
				Command:     wp.Command,
				StartTime:   wp.StartTime,
				EndTime:     wp.EndTime,
				OutputFile:  filepath.Join(processDir, "output.log"),
				PID:         wp.PID,
				Status:      wp.Status,
				WorkspaceTS: workspaceTS,
				Hash:        wp.Hash,
				ExitCode:    wp.ExitCode,
				Signal:      wp.Signal,
			}

			allProcesses = append(allProcesses, proc)
		}
	}

	return allProcesses
}

// ListWorkspaceProcesses returns processes from a specific workspace
func ListWorkspaceProcesses(ws *workspace.Workspace) ([]*Process, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is nil")
	}

	var processes []*Process

	workspaceProcs, err := workspace.ListProcesses(ws)
	if err != nil {
		return nil, err
	}

	workspaceTS := filepath.Base(ws.Path)

	for _, wp := range workspaceProcs {
		processDir := workspace.GetProcessDir(ws, wp.Hash)

		proc := &Process{
			ID:          wp.Hash,
			Command:     wp.Command,
			StartTime:   wp.StartTime,
			EndTime:     wp.EndTime,
			OutputFile:  filepath.Join(processDir, "output.log"),
			PID:         wp.PID,
			Status:      wp.Status,
			WorkspaceTS: workspaceTS,
			Hash:        wp.Hash,
			ExitCode:    wp.ExitCode,
			Signal:      wp.Signal,
		}

		processes = append(processes, proc)
	}

	return processes, nil
}

// GetProcess retrieves a process by ID (hash)
func GetProcess(stateDir, id string) (*Process, bool) {
	// Search through all workspaces for the process
	workspaces, err := workspace.ListWorkspaces(stateDir)
	if err != nil {
		return nil, false
	}

	for _, ws := range workspaces {
		wp, err := workspace.GetProcess(ws, id)
		if err != nil {
			continue
		}

		workspaceTS := filepath.Base(ws.Path)
		processDir := workspace.GetProcessDir(ws, wp.Hash)

		proc := &Process{
			ID:          wp.Hash,
			Command:     wp.Command,
			StartTime:   wp.StartTime,
			EndTime:     wp.EndTime,
			OutputFile:  filepath.Join(processDir, "output.log"),
			PID:         wp.PID,
			Status:      wp.Status,
			WorkspaceTS: workspaceTS,
			Hash:        wp.Hash,
			ExitCode:    wp.ExitCode,
			Signal:      wp.Signal,
		}

		return proc, true
	}

	return nil, false
}

// ReadOutput reads output from a file
func ReadOutput(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadCombinedOutput reads and parses the combined output.log file
// Returns stdout lines and stderr lines separately
func ReadCombinedOutput(filename string) (stdout string, stderr string, err error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", "", err
	}

	lines := strings.Split(string(data), "\n")
	var stdoutLines, stderrLines []string

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse format: "stdout 2025-01-01T12:34:56.789Z: content"
		// or: "stderr 2025-01-01T12:34:56.789Z: content"
		if len(line) > 37 { // Minimum length for prefix
			if strings.HasPrefix(line, "stdout ") {
				// Extract content after ": "
				if idx := strings.Index(line[7:], ": "); idx != -1 {
					content := line[7+idx+2:]
					stdoutLines = append(stdoutLines, content)
				}
			} else if strings.HasPrefix(line, "stderr ") {
				// Extract content after ": "
				if idx := strings.Index(line[7:], ": "); idx != -1 {
					content := line[7+idx+2:]
					stderrLines = append(stderrLines, content)
				}
			}
		}
	}

	stdout = strings.Join(stdoutLines, "\n")
	stderr = strings.Join(stderrLines, "\n")
	return stdout, stderr, nil
}

// ListWorkspaces returns all workspaces
func ListWorkspaces(stateDir string) ([]*workspace.Workspace, error) {
	return workspace.ListWorkspaces(stateDir)
}
