package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Workspace represents a workspace with a name, directory, and pre-command
type Workspace struct {
	ID                     string    `json:"id"`   // URL-safe immutable identifier
	Name                   string    `json:"name"` // Display name (can be changed)
	Directory              string    `json:"directory"`
	PreCommand             string    `json:"pre_command"`
	DefaultTerminalCommand string    `json:"default_terminal_command"` // Default command for interactive terminal (empty means auto-detect)
	CreatedAt              time.Time `json:"created_at"`
	Path                   string    `json:"path"` // Full path to workspace directory
}

// Process represents a process within a workspace
type Process struct {
	Hash      string    `json:"hash"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time,omitempty"`
	ExitCode  int       `json:"exit_code"`           // 0 if not exited yet or exited successfully
	Signal    string    `json:"signal,omitempty"`    // signal name if terminated by signal
	Completed bool      `json:"completed"`           // true if process has finished
}

// InitWorkspaces creates the workspaces directory
func InitWorkspaces(stateDir string) error {
	workspacesDir := filepath.Join(stateDir, "workspaces")
	if err := os.MkdirAll(workspacesDir, 0700); err != nil {
		return fmt.Errorf("failed to create workspaces directory: %w", err)
	}
	return nil
}

// CreateWorkspace creates a new workspace with the given name, directory, and pre-command
func CreateWorkspace(stateDir, name, directory, preCommand string) (*Workspace, error) {
	// Validate that the directory exists
	if _, err := os.Stat(directory); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("directory does not exist: %s", directory)
		}
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	// Generate URL-safe ID from name
	id, err := generateWorkspaceID(name)
	if err != nil {
		return nil, err
	}

	// Create directory name: ID
	workspacePath := filepath.Join(stateDir, "workspaces", id)

	// Check if workspace with this ID already exists
	if _, err := os.Stat(workspacePath); err == nil {
		return nil, fmt.Errorf("workspace with ID '%s' already exists", id)
	}

	if err := os.MkdirAll(workspacePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create workspace directory: %w", err)
	}

	// Create processes subdirectory
	processesDir := filepath.Join(workspacePath, "processes")
	if err := os.MkdirAll(processesDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create processes directory: %w", err)
	}

	ws := &Workspace{
		ID:         id,
		Name:       name,
		Directory:  directory,
		PreCommand: normalizePreCommand(preCommand),
		CreatedAt:  time.Now().UTC(),
		Path:       workspacePath,
	}

	// Save workspace metadata as individual files
	if err := saveWorkspaceFiles(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// GetWorkspace retrieves a workspace by its directory name (ID)
func GetWorkspace(stateDir, dirName string) (*Workspace, error) {
	workspacePath := filepath.Join(stateDir, "workspaces", dirName)

	ws := &Workspace{
		Path: workspacePath,
	}

	// Read individual files
	if err := loadWorkspaceFiles(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// GetWorkspaceByID retrieves a workspace by its ID
func GetWorkspaceByID(stateDir, id string) (*Workspace, error) {
	return GetWorkspace(stateDir, id)
}

// UpdateWorkspace updates an existing workspace's name, directory, and pre-command
func UpdateWorkspace(stateDir, id, name, directory, preCommand, defaultTerminalCommand string) (*Workspace, error) {
	// Get the existing workspace
	ws, err := GetWorkspaceByID(stateDir, id)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	// Validate that the directory exists
	if _, err := os.Stat(directory); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("directory does not exist: %s", directory)
		}
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	// Update workspace fields
	ws.Name = name
	ws.Directory = directory
	ws.PreCommand = normalizePreCommand(preCommand)
	ws.DefaultTerminalCommand = strings.TrimSpace(defaultTerminalCommand)

	// Save updated workspace metadata
	if err := saveWorkspaceFiles(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// ListWorkspaces returns all workspaces
func ListWorkspaces(stateDir string) ([]*Workspace, error) {
	workspacesDir := filepath.Join(stateDir, "workspaces")
	entries, err := os.ReadDir(workspacesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Workspace{}, nil
		}
		return nil, fmt.Errorf("failed to read workspaces directory: %w", err)
	}

	var workspaces []*Workspace
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		ws, err := GetWorkspace(stateDir, entry.Name())
		if err != nil {
			// Skip invalid workspaces
			continue
		}
		workspaces = append(workspaces, ws)
	}

	return workspaces, nil
}

// GetProcess retrieves a process from a workspace
func GetProcess(ws *Workspace, hash string) (*Process, error) {
	processDir := filepath.Join(ws.Path, "processes", hash)

	proc := &Process{
		Hash: hash,
	}

	// Read individual files
	if err := loadProcessFiles(processDir, proc); err != nil {
		return nil, err
	}

	return proc, nil
}

// ListProcesses returns all processes in a workspace
func ListProcesses(ws *Workspace) ([]*Process, error) {
	processesDir := filepath.Join(ws.Path, "processes")
	entries, err := os.ReadDir(processesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Process{}, nil
		}
		return nil, fmt.Errorf("failed to read processes directory: %w", err)
	}

	var processes []*Process
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		proc, err := GetProcess(ws, entry.Name())
		if err != nil {
			// Skip invalid processes
			continue
		}
		processes = append(processes, proc)
	}

	return processes, nil
}

// GetProcessDir returns the directory path for a process
func GetProcessDir(ws *Workspace, hash string) string {
	return filepath.Join(ws.Path, "processes", hash)
}

// saveWorkspaceFiles saves workspace data as individual files
func saveWorkspaceFiles(ws *Workspace) error {
	// Write ID file
	if err := os.WriteFile(filepath.Join(ws.Path, "id"), []byte(ws.ID), 0600); err != nil {
		return fmt.Errorf("failed to write id file: %w", err)
	}

	// Write name file
	if err := os.WriteFile(filepath.Join(ws.Path, "name"), []byte(ws.Name), 0600); err != nil {
		return fmt.Errorf("failed to write name file: %w", err)
	}

	// Write directory file
	if err := os.WriteFile(filepath.Join(ws.Path, "directory"), []byte(ws.Directory), 0600); err != nil {
		return fmt.Errorf("failed to write directory file: %w", err)
	}

	// Write pre-command file (if not empty), or remove it if empty
	preCommandPath := filepath.Join(ws.Path, "pre-command")
	if ws.PreCommand != "" {
		if err := os.WriteFile(preCommandPath, []byte(ws.PreCommand), 0600); err != nil {
			return fmt.Errorf("failed to write pre-command file: %w", err)
		}
	} else {
		// Remove pre-command file if it exists (ignore error if file doesn't exist)
		_ = os.Remove(preCommandPath)
	}

	// Write created-at file
	createdAt := ws.CreatedAt.Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(ws.Path, "created-at"), []byte(createdAt), 0600); err != nil {
		return fmt.Errorf("failed to write created-at file: %w", err)
	}

	return nil
}

// loadWorkspaceFiles loads workspace data from individual files
func loadWorkspaceFiles(ws *Workspace) error {
	// Read ID file
	idData, err := os.ReadFile(filepath.Join(ws.Path, "id"))
	if err != nil {
		return fmt.Errorf("failed to read id file: %w", err)
	}
	ws.ID = strings.TrimSpace(string(idData))

	// Read name file
	nameData, err := os.ReadFile(filepath.Join(ws.Path, "name"))
	if err != nil {
		return fmt.Errorf("failed to read name file: %w", err)
	}
	ws.Name = string(nameData)

	// Read directory file
	dirData, err := os.ReadFile(filepath.Join(ws.Path, "directory"))
	if err != nil {
		return fmt.Errorf("failed to read directory file: %w", err)
	}
	ws.Directory = string(dirData)

	// Read pre-command file (optional)
	preCommandData, err := os.ReadFile(filepath.Join(ws.Path, "pre-command"))
	if err == nil {
		ws.PreCommand = string(preCommandData)
	}

	// Read created-at file
	createdAtData, err := os.ReadFile(filepath.Join(ws.Path, "created-at"))
	if err != nil {
		return fmt.Errorf("failed to read created-at file: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, string(createdAtData))
	if err != nil {
		return fmt.Errorf("failed to parse created-at: %w", err)
	}
	ws.CreatedAt = createdAt

	return nil
}

// loadProcessFiles loads process data from individual files
func loadProcessFiles(processDir string, proc *Process) error {
	// Read command file
	cmdData, err := os.ReadFile(filepath.Join(processDir, "cmd"))
	if err != nil {
		return fmt.Errorf("failed to read cmd file: %w", err)
	}
	proc.Command = string(cmdData)

	// Read starttime file
	startTimeData, err := os.ReadFile(filepath.Join(processDir, "starttime"))
	if err != nil {
		return fmt.Errorf("failed to read starttime file: %w", err)
	}
	startTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(startTimeData)))
	if err != nil {
		return fmt.Errorf("failed to parse starttime: %w", err)
	}
	proc.StartTime = startTime

	// Read completed file
	completedData, err := os.ReadFile(filepath.Join(processDir, "completed"))
	if err != nil {
		return fmt.Errorf("failed to read completed file: %w", err)
	}
	proc.Completed = strings.TrimSpace(string(completedData)) == "true"

	// Read PID file (optional)
	pidData, err := os.ReadFile(filepath.Join(processDir, "pid"))
	if err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
		if err == nil {
			proc.PID = pid
		}
	}

	// Read endtime file (optional)
	endTimeData, err := os.ReadFile(filepath.Join(processDir, "endtime"))
	if err == nil {
		endTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(endTimeData)))
		if err == nil {
			proc.EndTime = endTime
		}
	}

	// Read exit-status file (optional)
	exitStatusData, err := os.ReadFile(filepath.Join(processDir, "exit-status"))
	if err == nil && len(exitStatusData) > 0 {
		exitCode, err := strconv.Atoi(strings.TrimSpace(string(exitStatusData)))
		if err == nil {
			proc.ExitCode = exitCode
		}
	}

	// Read signal file (optional)
	signalData, err := os.ReadFile(filepath.Join(processDir, "signal"))
	if err == nil && len(signalData) > 0 {
		proc.Signal = strings.TrimSpace(string(signalData))
	}

	return nil
}

// GenerateProcessHash generates a unique hash for a process
func GenerateProcessHash(command string, timestamp time.Time) string {
	data := fmt.Sprintf("%s:%d", command, timestamp.UnixNano())
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])[:16] // Use first 16 characters
}

// normalizePreCommand normalizes the pre-command by handling shebang prefixes
// If the command starts with #!, it's used as-is
// If the command is non-empty and doesn't start with #!, prepend #!/usr/bin/env bash
// If the command is empty, return empty string
func normalizePreCommand(preCommand string) string {
	// Trim whitespace
	preCommand = strings.TrimSpace(preCommand)

	// If empty, return empty
	if preCommand == "" {
		return ""
	}

	// Normalize line endings: convert CRLF (\r\n) to LF (\n)
	// This prevents issues when pre-commands are entered on Windows or copied from Windows sources
	preCommand = strings.ReplaceAll(preCommand, "\r\n", "\n")
	// Also remove any remaining standalone \r characters
	preCommand = strings.ReplaceAll(preCommand, "\r", "")

	// If starts with shebang, use as-is
	if strings.HasPrefix(preCommand, "#!") {
		return preCommand
	}

	// Otherwise, prepend default shebang
	return "#!/usr/bin/env bash\n" + preCommand
}

// ExtractShellFromShebang extracts the shell binary from a shebang line
// Returns "bash" by default if no shebang is found or if parsing fails
func ExtractShellFromShebang(preCommand string) string {
	lines := strings.Split(preCommand, "\n")
	if len(lines) == 0 {
		return "bash"
	}

	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "#!") {
		return "bash"
	}

	// Remove #! prefix and trim spaces
	shebang := strings.TrimSpace(strings.TrimPrefix(firstLine, "#!"))

	// Handle common cases:
	// #!/usr/bin/env bash -> bash
	// #!/usr/bin/env fish -> fish
	// #!/bin/bash -> bash
	// #!/bin/sh -> sh
	parts := strings.Fields(shebang)
	if len(parts) == 0 {
		return "bash"
	}

	// If using env, the shell is the second part
	if strings.HasSuffix(parts[0], "/env") && len(parts) > 1 {
		return parts[1]
	}

	// Otherwise, extract the basename from the path
	shell := filepath.Base(parts[0])
	if shell == "" {
		return "bash"
	}

	return shell
}

// generateWorkspaceID generates a URL-safe ID from a name
// The ID is based on the name but guaranteed to be URL-safe
func generateWorkspaceID(name string) (string, error) {
	// Convert to lowercase
	id := strings.ToLower(name)

	// Replace spaces and special characters with hyphens
	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, id)

	// Remove leading/trailing hyphens and collapse multiple hyphens
	id = strings.Trim(id, "-")
	for strings.Contains(id, "--") {
		id = strings.ReplaceAll(id, "--", "-")
	}

	// Ensure it's not empty
	if id == "" {
		return "", fmt.Errorf("workspace name must contain at least one valid character (a-z, 0-9)")
	}

	// Limit length
	if len(id) > 50 {
		id = id[:50]
	}

	return id, nil
}
