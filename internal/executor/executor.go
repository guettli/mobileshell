package executor

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mobileshell/internal/outputlog"
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
	ContentType string    `json:"content_type,omitempty"` // MIME type of stdout output
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

	// Generate hash for the process
	hash := workspace.GenerateProcessHash(command, time.Now().UTC())

	// Get workspace timestamp from path
	workspaceTS := filepath.Base(ws.Path)

	// Get process directory for file paths
	processDir := workspace.GetProcessDir(ws, hash)
	outputFile := filepath.Join(processDir, "output.log")

	// Spawn the process using `mobileshell nohup` in the background
	cmd := exec.Command(execPath, "nohup", "--work-dir", ws.Directory, "--pre-command", ws.PreCommand, processDir, command)

	// Capture stdout and stderr from the nohup subprocess
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe for nohup: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe for nohup: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to spawn nohup process: %w", err)
	}

	// Wait for process initialization (files to be created by nohup)
	// We check for 'starttime' file which is created early
	starttimeFile := filepath.Join(processDir, "starttime")
	initialized := false
	for i := 0; i < 40; i++ { // Wait up to 2 seconds (50ms * 40)
		if _, err := os.Stat(starttimeFile); err == nil {
			initialized = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !initialized {
		// Log warning but continue - the process might just be slow or failed immediately
		// The background reader will handle output
		fmt.Printf("Warning: Process state files not created in time for %s\n", hash)
	}

	// Read and log nohup subprocess output in the background
	go func() {
		defer func() { _ = stdoutPipe.Close() }()
		defer func() { _ = stderrPipe.Close() }()

		// Wait for output.log to be created by nohup
		var outFile *os.File
		var err error
		// Retry for up to 2 seconds
		for i := 0; i < 20; i++ {
			outFile, err = os.OpenFile(outputFile, os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0600)
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if err != nil {
			// If we can't open the file, just drain the pipes silently
			_, _ = io.Copy(io.Discard, stdoutPipe)
			_, _ = io.Copy(io.Discard, stderrPipe)
			_ = cmd.Wait()
			return
		}
		defer func() { _ = outFile.Close() }()

		// Read from both pipes and write to output.log
		done := make(chan struct{}, 2)

		go readNohupStream(stdoutPipe, "nohup-stdout", outFile, done)
		go readNohupStream(stderrPipe, "nohup-stderr", outFile, done)

		// Wait for both readers to finish
		<-done
		<-done

		_ = cmd.Wait()
	}()

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

	// Read content-type if available
	contentType := ""
	contentTypeFile := filepath.Join(processDir, "content-type")
	if data, err := os.ReadFile(contentTypeFile); err == nil {
		contentType = string(data)
	}

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
		ContentType: contentType,
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

// readNohupStream reads from a nohup subprocess stream and writes it to output.log
func readNohupStream(reader io.Reader, streamName string, outFile *os.File, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text() + "\n"
		outputLine := outputlog.OutputLine{
			Stream:    streamName,
			Timestamp: time.Now().UTC(),
			Line:      line,
		}
		formattedLine := outputlog.FormatOutputLine(outputLine)
		_, _ = outFile.WriteString(formattedLine)
	}
}
