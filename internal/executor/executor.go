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

	var stdoutParts, stderrParts, stdinParts []string
	i := 0

	for i < len(data) {
		// Check for new format: "> stream ..."
		if i+2 < len(data) && data[i] == '>' && data[i+1] == ' ' {
			// New format: "> stream timestamp length: content\n"
			// Find the ": " separator
			separatorIdx := -1
			for j := i + 2; j < len(data)-1; j++ {
				if data[j] == ':' && data[j+1] == ' ' {
					separatorIdx = j + 2
					break
				}
			}

			if separatorIdx != -1 {
				// Extract the stream type from between "> " and the first space after it
				streamStart := i + 2
				streamEnd := streamStart
				for streamEnd < len(data) && data[streamEnd] != ' ' {
					streamEnd++
				}
				stream := string(data[streamStart:streamEnd])

				// Extract length from the format
				// Find the space before the colon to get the length field
				lengthStart := -1
				for j := separatorIdx - 3; j >= i+2; j-- {
					if data[j] == ' ' {
						lengthStart = j + 1
						break
					}
				}

				if lengthStart != -1 {
					lengthStr := string(data[lengthStart : separatorIdx-2])
					var length int
					if _, scanErr := fmt.Sscanf(lengthStr, "%d", &length); scanErr == nil {
						// Read exactly 'length' bytes of content
						if separatorIdx+length <= len(data) {
							content := string(data[separatorIdx : separatorIdx+length])

							switch stream {
							case "stdout":
								stdoutParts = append(stdoutParts, content)
							case "stderr":
								stderrParts = append(stderrParts, content)
							case "stdin":
								stdinParts = append(stdinParts, content)
							}

							// Move past content and the line separator '\n'
							i = separatorIdx + length + 1
							continue
						}
					}
				}
			}

			// If parsing failed, skip to next line
			for i < len(data) && data[i] != '\n' {
				i++
			}
			i++ // Skip the newline
		} else if i+12 <= len(data) && string(data[i:i+12]) == "signal-sent " {
			// Signal-sent format: "signal-sent timestamp: signal info\n"
			// Find the ": " separator
			separatorIdx := -1
			for j := i + 12; j < len(data)-1; j++ {
				if data[j] == ':' && data[j+1] == ' ' {
					separatorIdx = j + 2
					break
				}
				if data[j] == '\n' {
					break
				}
			}

			if separatorIdx != -1 {
				// Find the end of the line
				lineEnd := separatorIdx
				for lineEnd < len(data) && data[lineEnd] != '\n' {
					lineEnd++
				}
				content := "Signal sent: " + string(data[separatorIdx:lineEnd])
				stdinParts = append(stdinParts, content)
				stdinParts = append(stdinParts, "\n")
				i = lineEnd + 1
				continue
			}

			// If parsing failed, skip to next line
			for i < len(data) && data[i] != '\n' {
				i++
			}
			i++
		} else {
			// Skip to next line if not a recognized format
			for i < len(data) && data[i] != '\n' {
				i++
			}
			i++
		}
	}

	// Concatenate parts as-is (they already include newlines where appropriate)
	stdout = strings.Join(stdoutParts, "")
	stderr = strings.Join(stderrParts, "")
	stdin = strings.Join(stdinParts, "")

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
		// Check for new format: "> stdout ..."
		if i+9 <= len(data) && string(data[i:i+9]) == "> stdout " {
			// New format: "> stdout timestamp length: content\n"
			// Find the ": " separator
			separatorIdx := -1
			for j := i + 9; j < len(data)-1; j++ {
				if data[j] == ':' && data[j+1] == ' ' {
					separatorIdx = j + 2
					break
				}
			}

			if separatorIdx != -1 {
				// Extract length from the format
				// Find the space before the colon to get the length field
				lengthStart := -1
				for j := separatorIdx - 3; j >= i+9; j-- {
					if data[j] == ' ' {
						lengthStart = j + 1
						break
					}
				}

				if lengthStart != -1 {
					lengthStr := string(data[lengthStart : separatorIdx-2])
					var length int
					if _, scanErr := fmt.Sscanf(lengthStr, "%d", &length); scanErr == nil {
						// Read exactly 'length' bytes of content
						if separatorIdx+length <= len(data) {
							content := data[separatorIdx : separatorIdx+length]
							stdoutBytes = append(stdoutBytes, content...)

							// Move past content and the line separator '\n'
							i = separatorIdx + length + 1
							continue
						}
					}
				}
			}
		}

		// Skip to next line if parsing failed or not a stdout line
		nextLine := i
		for nextLine < len(data) && data[nextLine] != '\n' {
			nextLine++
		}
		i = nextLine + 1
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
