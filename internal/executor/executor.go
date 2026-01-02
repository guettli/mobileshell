package executor

import (
	"fmt"
	"net/http"
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
	Completed   bool      `json:"completed"`           // true if process has finished
	WorkspaceTS string    `json:"workspace_timestamp"`
	Hash        string    `json:"hash"`
	ExitCode    int       `json:"exit_code"`           // 0 if not exited yet or exited successfully
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
		Completed:   false,
		WorkspaceTS: workspaceTS,
		Hash:        hash,
	}

	return proc, nil
}

// convertWorkspaceProcess converts a workspace.Process to an executor.Process
func convertWorkspaceProcess(ws *workspace.Workspace, wp *workspace.Process) *Process {
	workspaceTS := filepath.Base(ws.Path)
	processDir := workspace.GetProcessDir(ws, wp.Hash)

	return &Process{
		ID:          wp.Hash,
		Command:     wp.Command,
		StartTime:   wp.StartTime,
		EndTime:     wp.EndTime,
		OutputFile:  filepath.Join(processDir, "output.log"),
		PID:         wp.PID,
		Completed:   wp.Completed,
		WorkspaceTS: workspaceTS,
		Hash:        wp.Hash,
		ExitCode:    wp.ExitCode,
		Signal:      wp.Signal,
	}
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

	for _, wp := range workspaceProcs {
		proc := convertWorkspaceProcess(ws, wp)
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

		proc := convertWorkspaceProcess(ws, wp)
		return proc, true
	}

	return nil, false
}

// ReadCombinedOutput reads and parses the combined output.log file
// Returns stdout, stderr, and stdin lines separately
func ReadCombinedOutput(filename string) (stdout string, stderr string, stdin string, err error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", "", "", err
	}

	lines := strings.Split(string(data), "\n")
	var stdoutLines, stderrLines, stdinLines []string

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse format: "stdout 2025-01-01T12:34:56.789Z: content"
		// or: "stderr 2025-01-01T12:34:56.789Z: content"
		// or: "stdin 2025-01-01T12:34:56.789Z: content"
		// or: "signal-sent 2025-01-01T12:34:56.789Z: 15 SIGTERM"
		// Content after ": " can be empty or incomplete (e.g., if process terminates without newline)
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
		} else if strings.HasPrefix(line, "stdin ") {
			// Extract content after ": "
			if idx := strings.Index(line[6:], ": "); idx != -1 {
				content := line[6+idx+2:]
				stdinLines = append(stdinLines, content)
			}
		} else if strings.HasPrefix(line, "signal-sent ") {
			// Extract signal info after ": " and show in stdin (for visibility)
			if idx := strings.Index(line[12:], ": "); idx != -1 {
				content := "Signal sent: " + line[12+idx+2:]
				stdinLines = append(stdinLines, content)
			}
		}
	}

	stdout = strings.Join(stdoutLines, "\n")
	stderr = strings.Join(stderrLines, "\n")
	stdin = strings.Join(stdinLines, "\n")

	return stdout, stderr, stdin, nil
}

// ReadRawStdout extracts raw stdout bytes from the combined output log file
// This function preserves binary data including newlines and null bytes
func ReadRawStdout(filename string) ([]byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var stdoutBytes []byte
	i := 0
	for i < len(data) {
		// Find the next newline
		lineEnd := i
		for lineEnd < len(data) && data[lineEnd] != '\n' {
			lineEnd++
		}

		line := data[i:lineEnd]

		// Check if this is a stdout line
		if len(line) > 7 && string(line[:7]) == "stdout " {
			// Find the ": " separator after the timestamp
			// Format: "stdout 2025-01-01T12:34:56.789Z: content"
			// The timestamp is fixed length: 24 chars (excluding "stdout ")
			// So ": " should be around position 7 + 24 = 31
			separatorIdx := -1
			for j := 7; j < len(line)-1; j++ {
				if line[j] == ':' && line[j+1] == ' ' {
					separatorIdx = j + 2 // Skip ": "
					break
				}
			}

			if separatorIdx != -1 {
				content := line[separatorIdx:]
				stdoutBytes = append(stdoutBytes, content...)
				// Add newline to separate lines (matching original output format)
				stdoutBytes = append(stdoutBytes, '\n')
			}
		}

		// Move to the next line
		i = lineEnd + 1
	}

	return stdoutBytes, nil
}

// DetectContentType detects the MIME type of stdout data
func DetectContentType(data []byte) string {
	// http.DetectContentType uses at most the first 512 bytes
	if len(data) > 512 {
		data = data[:512]
	}
	return http.DetectContentType(data)
}

// ListWorkspaces returns all workspaces
func ListWorkspaces(stateDir string) ([]*workspace.Workspace, error) {
	return workspace.ListWorkspaces(stateDir)
}
