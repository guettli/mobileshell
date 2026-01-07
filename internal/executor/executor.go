package executor

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	file, err := os.Open(filename)
	if err != nil {
		return "", "", "", err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			slog.Error("Failed to close file in ReadCombinedOutput", "error", cerr)
		}
	}()

	var stdoutContent, stderrContent, stdinContent strings.Builder
	reader := bufio.NewReader(file)
	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(lineBytes) == 0 {
					break
				}
				// Process the last line if it doesn't end in newline
			} else {
				return "", "", "", err
			}
		}

		// Handle signal-sent lines first, as they don't follow the length-prefixed format
		if bytes.HasPrefix(lineBytes, []byte("signal-sent ")) {
			if idx := bytes.Index(lineBytes, []byte(": ")); idx != -1 {
				content := "Signal sent: " + string(lineBytes[idx+2:])
				// ReadBytes includes the delimiter, so content likely has \n at end
				// We want to ensure it ends with exactly one \n for display
				if !strings.HasSuffix(content, "\n") {
					content += "\n"
				}
				stdinContent.WriteString(content)
			}
			if err == io.EOF {
				break
			}
			continue
		}

		// Now process lines in the new format: stream time length: content
		// We expect at least 3 parts separated by spaces
		parts := bytes.SplitN(lineBytes, []byte(" "), 3)
		if len(parts) < 3 {
			if err == io.EOF {
				break
			}
			continue // Malformed line, skip
		}

		stream := parts[0]
		// parts[1] is timestamp
		rest := parts[2]

		colonIndex := bytes.Index(rest, []byte(": "))
		if colonIndex == -1 {
			if err == io.EOF {
				break
			}
			continue
		}

		lengthStr := string(rest[:colonIndex])
		length, parseErr := strconv.Atoi(lengthStr)
		if parseErr != nil {
			if err == io.EOF {
				break
			}
			continue
		}

		contentFromFile := rest[colonIndex+2:]

		// contentFromFile includes the log's newline if ReadBytes found one.
		// We rely on 'length' to determine what the original content was.
		// If len(contentFromFile) > length, the extra bytes are the log framing (usually \n or \r\n)
		// We truncate to 'length'.
		if len(contentFromFile) > length {
			contentFromFile = contentFromFile[:length]
		} else if len(contentFromFile) < length {
			// This theoretically shouldn't happen if the log was written correctly and not truncated
			// But if it does, we might want to append a newline if that was the intent,
			// or just accept we have partial data.
			// The original logic appended \n if len < length.
			contentFromFile = append(contentFromFile, '\n')
		}

		switch string(stream) {
		case "stdout":
			stdoutContent.Write(contentFromFile)
		case "stderr":
			stderrContent.Write(contentFromFile)
		case "stdin":
			stdinContent.Write(contentFromFile)
		}

		if err == io.EOF {
			break
		}
	}

	return stdoutContent.String(), stderrContent.String(), stdinContent.String(), nil
}

// ReadRawStdout extracts raw stdout bytes from the combined output log file
// This function preserves binary data including newlines and null bytes
func ReadRawStdout(filename string) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			slog.Error("Failed to close file in ReadRawStdout", "error", cerr)
		}
	}()

	var stdoutBytes []byte
	reader := bufio.NewReader(file)

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(lineBytes) == 0 {
					break
				}
			} else {
				return nil, err
			}
		}

		if !bytes.HasPrefix(lineBytes, []byte("stdout ")) {
			if err == io.EOF {
				break
			}
			continue
		}

		parts := bytes.SplitN(lineBytes, []byte(" "), 3)
		if len(parts) < 3 {
			if err == io.EOF {
				break
			}
			continue
		}
		rest := parts[2]

		colonIndex := bytes.Index(rest, []byte(": "))
		if colonIndex == -1 {
			if err == io.EOF {
				break
			}
			continue
		}

		lengthStr := string(rest[:colonIndex])
		length, parseErr := strconv.Atoi(lengthStr)
		if parseErr != nil {
			if err == io.EOF {
				break
			}
			continue
		}

		contentFromFile := rest[colonIndex+2:]

		// Exact same logic as ReadCombinedOutput
		if len(contentFromFile) > length {
			contentFromFile = contentFromFile[:length]
		} else if len(contentFromFile) < length {
			contentFromFile = append(contentFromFile, '\n')
		}

		stdoutBytes = append(stdoutBytes, contentFromFile...)

		if err == io.EOF {
			break
		}
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
