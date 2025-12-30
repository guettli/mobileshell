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
	ID         string    `json:"id"`   // URL-safe immutable identifier
	Name       string    `json:"name"` // Display name (can be changed)
	Directory  string    `json:"directory"`
	PreCommand string    `json:"pre_command"`
	CreatedAt  time.Time `json:"created_at"`
	Path       string    `json:"path"` // Full path to workspace directory
}

// Process represents a process within a workspace
type Process struct {
	Hash      string    `json:"hash"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time,omitempty"`
	ExitCode  *int      `json:"exit_code,omitempty"` // nil if not exited yet
	Status    string    `json:"status"`              // "running" or "completed"
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
	id := generateWorkspaceID(name)

	// Create directory name: YYYY-MM-DD_ID
	dateStr := time.Now().Format("2006-01-02")
	workspaceDirName := fmt.Sprintf("%s_%s", dateStr, id)
	workspacePath := filepath.Join(stateDir, "workspaces", workspaceDirName)

	// Check if workspace with this ID already exists today
	if _, err := os.Stat(workspacePath); err == nil {
		return nil, fmt.Errorf("workspace with ID '%s' already exists for today", id)
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
		PreCommand: preCommand,
		CreatedAt:  time.Now(),
		Path:       workspacePath,
	}

	// Save workspace metadata as individual files
	if err := saveWorkspaceFiles(ws); err != nil {
		return nil, err
	}

	return ws, nil
}

// GetWorkspace retrieves a workspace by its directory name (YYYY-MM-DD_ID)
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
// If multiple workspaces exist with the same ID (from different days), returns the most recent
func GetWorkspaceByID(stateDir, id string) (*Workspace, error) {
	workspacesDir := filepath.Join(stateDir, "workspaces")
	entries, err := os.ReadDir(workspacesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspaces directory: %w", err)
	}

	// Find all workspaces with matching ID, sorted by date (newest first)
	var matchingDirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Check if directory name ends with _ID
		if strings.HasSuffix(entry.Name(), "_"+id) {
			matchingDirs = append(matchingDirs, entry.Name())
		}
	}

	if len(matchingDirs) == 0 {
		return nil, fmt.Errorf("workspace not found: %s", id)
	}

	// Sort in reverse order to get most recent first
	// Since format is YYYY-MM-DD_ID, lexicographic sort works
	for i := 0; i < len(matchingDirs); i++ {
		for j := i + 1; j < len(matchingDirs); j++ {
			if matchingDirs[i] < matchingDirs[j] {
				matchingDirs[i], matchingDirs[j] = matchingDirs[j], matchingDirs[i]
			}
		}
	}

	// Return the most recent one
	return GetWorkspace(stateDir, matchingDirs[0])
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

// CreateProcess creates a new process in a workspace
func CreateProcess(ws *Workspace, command string) (string, error) {
	// Generate hash for the process
	hash := generateProcessHash(command, time.Now())

	processDir := filepath.Join(ws.Path, "processes", hash)
	if err := os.MkdirAll(processDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create process directory: %w", err)
	}

	proc := &Process{
		Hash:      hash,
		Command:   command,
		StartTime: time.Now(),
		Status:    "pending",
	}

	// Save process metadata as individual files
	if err := saveProcessFiles(processDir, proc); err != nil {
		return "", err
	}

	// Create empty stdout/stderr files
	if err := os.WriteFile(filepath.Join(processDir, "stdout"), []byte{}, 0600); err != nil {
		return "", fmt.Errorf("failed to create stdout file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(processDir, "stderr"), []byte{}, 0600); err != nil {
		return "", fmt.Errorf("failed to create stderr file: %w", err)
	}

	return hash, nil
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

// UpdateProcessPID updates the PID of a running process
func UpdateProcessPID(ws *Workspace, hash string, pid int) error {
	processDir := filepath.Join(ws.Path, "processes", hash)

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

// UpdateProcessExit updates a process when it exits
func UpdateProcessExit(ws *Workspace, hash string, exitCode int) error {
	processDir := filepath.Join(ws.Path, "processes", hash)

	// Write exit-status file
	if err := os.WriteFile(filepath.Join(processDir, "exit-status"), []byte(strconv.Itoa(exitCode)), 0600); err != nil {
		return fmt.Errorf("failed to write exit-status file: %w", err)
	}

	// Write endtime file
	endTime := time.Now().Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(processDir, "endtime"), []byte(endTime), 0600); err != nil {
		return fmt.Errorf("failed to write endtime file: %w", err)
	}

	// Update status file
	if err := os.WriteFile(filepath.Join(processDir, "status"), []byte("completed"), 0600); err != nil {
		return fmt.Errorf("failed to write status file: %w", err)
	}

	return nil
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

	// Write pre-command file (if not empty)
	if ws.PreCommand != "" {
		if err := os.WriteFile(filepath.Join(ws.Path, "pre-command"), []byte(ws.PreCommand), 0600); err != nil {
			return fmt.Errorf("failed to write pre-command file: %w", err)
		}
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

// saveProcessFiles saves process data as individual files
func saveProcessFiles(processDir string, proc *Process) error {
	// Write command file
	if err := os.WriteFile(filepath.Join(processDir, "cmd"), []byte(proc.Command), 0600); err != nil {
		return fmt.Errorf("failed to write cmd file: %w", err)
	}

	// Write starttime file
	startTime := proc.StartTime.Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(processDir, "starttime"), []byte(startTime), 0600); err != nil {
		return fmt.Errorf("failed to write starttime file: %w", err)
	}

	// Write status file
	if err := os.WriteFile(filepath.Join(processDir, "status"), []byte(proc.Status), 0600); err != nil {
		return fmt.Errorf("failed to write status file: %w", err)
	}

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

	// Read status file
	statusData, err := os.ReadFile(filepath.Join(processDir, "status"))
	if err != nil {
		return fmt.Errorf("failed to read status file: %w", err)
	}
	proc.Status = strings.TrimSpace(string(statusData))

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
			proc.ExitCode = &exitCode
		}
	}

	return nil
}

// generateProcessHash generates a unique hash for a process
func generateProcessHash(command string, timestamp time.Time) string {
	data := fmt.Sprintf("%s:%d", command, timestamp.UnixNano())
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])[:16] // Use first 16 characters
}

// generateWorkspaceID generates a URL-safe ID from a name
// The ID is based on the name but guaranteed to be URL-safe
func generateWorkspaceID(name string) string {
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
		id = "workspace"
	}

	// Limit length
	if len(id) > 50 {
		id = id[:50]
	}

	return id
}
