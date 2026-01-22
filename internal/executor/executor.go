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

	"mobileshell/pkk/outputlog"
	"mobileshell/internal/process"
	"mobileshell/internal/workspace"
)

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
	commandId := time.Now().UTC().Format(time.RFC3339Nano)

	processDir := filepath.Join(ws.Path, "processes", commandId)
	if err := os.MkdirAll(processDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create process directory: %w", err)
	}

	proc := &process.Process{
		CommandId:  commandId,
		Command:    command,
		Completed:  false,
		ProcessDir: processDir,
	}

	cmdPath := filepath.Join(processDir, "cmd")
	err = os.WriteFile(cmdPath, []byte(command), 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to write %q: %w", cmdPath, err)
	}

	// Write starttime file
	startTime := time.Now().UTC().Format(time.RFC3339Nano)
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

	if filepath.Ext(execPath) == ".test" {
		proc.ExecCmd = exec.Command("go", "run", "mobileshell/cmd/mobileshell", "nohup", nohupCommandPath)
	} else {
		proc.ExecCmd = exec.Command(execPath, "nohup", nohupCommandPath)
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

	if err := proc.ExecCmd.Start(); err != nil {
		nohupLogFile.Close()
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

// readNohupStream reads from a nohup subprocess stream and writes it to output.log. TODO: No, all writes to output.log to through the channel.
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
