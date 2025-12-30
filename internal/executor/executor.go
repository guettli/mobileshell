package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"mobileshell/internal/workspace"
)

type Process struct {
	ID              string    `json:"id"`
	Command         string    `json:"command"`
	StartTime       time.Time `json:"start_time"`
	StdoutFile      string    `json:"stdout_file"`
	StderrFile      string    `json:"stderr_file"`
	PID             int       `json:"pid"`
	Status          string    `json:"status"` // "running", "completed", or "pending"
	WorkspaceTS     string    `json:"workspace_timestamp"`
	Hash            string    `json:"hash"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	EndTime         time.Time `json:"end_time,omitempty"`
}

type Executor struct {
	mu              sync.RWMutex
	stateDir        string
	workspaceMgr    *workspace.Manager
	currentWorkspace *workspace.Workspace
	executablePath  string
}

func New(stateDir string) (*Executor, error) {
	// Get the path to the current executable
	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Initialize workspace manager
	mgr, err := workspace.New(stateDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace manager: %w", err)
	}

	e := &Executor{
		stateDir:       stateDir,
		workspaceMgr:   mgr,
		executablePath: execPath,
	}

	return e, nil
}

// SetWorkspace changes the current workspace
func (e *Executor) SetWorkspace(name, directory, preCommand string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ws, err := e.workspaceMgr.CreateWorkspace(name, directory, preCommand)
	if err != nil {
		return err
	}

	e.currentWorkspace = ws
	return nil
}

// GetCurrentWorkspace returns the current workspace
func (e *Executor) GetCurrentWorkspace() *workspace.Workspace {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentWorkspace
}

// SelectWorkspace selects an existing workspace by directory name (YYYY-MM-DD_ID)
func (e *Executor) SelectWorkspace(dirName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ws, err := e.workspaceMgr.GetWorkspace(dirName)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}

	e.currentWorkspace = ws
	return nil
}

// SelectWorkspaceByID selects an existing workspace by its ID
func (e *Executor) SelectWorkspaceByID(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ws, err := e.workspaceMgr.GetWorkspaceByID(id)
	if err != nil {
		return fmt.Errorf("failed to get workspace by ID: %w", err)
	}

	e.currentWorkspace = ws
	return nil
}

// Execute spawns a new process in the current workspace
func (e *Executor) Execute(command string) (*Process, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.currentWorkspace == nil {
		return nil, fmt.Errorf("no workspace set")
	}

	// Create process in workspace
	hash, err := e.workspaceMgr.CreateProcess(e.currentWorkspace, command)
	if err != nil {
		return nil, fmt.Errorf("failed to create process: %w", err)
	}

	// Get workspace timestamp from path
	workspaceTS := filepath.Base(e.currentWorkspace.Path)

	// Spawn the process using `mobileshell nohup` in the background
	cmd := exec.Command(e.executablePath, "nohup", "--state-dir", e.stateDir, workspaceTS, hash)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to spawn nohup process: %w", err)
	}

	// Don't wait for the nohup process - let it run independently
	go func() {
		_ = cmd.Wait()
	}()

	// Get process directory for file paths
	processDir := e.workspaceMgr.GetProcessDir(e.currentWorkspace, hash)

	proc := &Process{
		ID:          hash,
		Command:     command,
		StartTime:   time.Now(),
		StdoutFile:  filepath.Join(processDir, "stdout"),
		StderrFile:  filepath.Join(processDir, "stderr"),
		Status:      "pending",
		WorkspaceTS: workspaceTS,
		Hash:        hash,
	}

	return proc, nil
}

// ListProcesses returns all processes from all workspaces
func (e *Executor) ListProcesses() []*Process {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var allProcesses []*Process

	// Get all workspaces
	workspaces, err := e.workspaceMgr.ListWorkspaces()
	if err != nil {
		return allProcesses
	}

	for _, ws := range workspaces {
		workspaceProcs, err := e.workspaceMgr.ListProcesses(ws)
		if err != nil {
			continue
		}

		workspaceTS := filepath.Base(ws.Path)

		for _, wp := range workspaceProcs {
			processDir := e.workspaceMgr.GetProcessDir(ws, wp.Hash)

			proc := &Process{
				ID:          wp.Hash,
				Command:     wp.Command,
				StartTime:   wp.StartTime,
				EndTime:     wp.EndTime,
				StdoutFile:  filepath.Join(processDir, "stdout"),
				StderrFile:  filepath.Join(processDir, "stderr"),
				PID:         wp.PID,
				Status:      wp.Status,
				WorkspaceTS: workspaceTS,
				Hash:        wp.Hash,
				ExitCode:    wp.ExitCode,
			}

			allProcesses = append(allProcesses, proc)
		}
	}

	return allProcesses
}

// GetProcess retrieves a process by ID (hash)
func (e *Executor) GetProcess(id string) (*Process, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Search through all workspaces for the process
	workspaces, err := e.workspaceMgr.ListWorkspaces()
	if err != nil {
		return nil, false
	}

	for _, ws := range workspaces {
		wp, err := e.workspaceMgr.GetProcess(ws, id)
		if err != nil {
			continue
		}

		workspaceTS := filepath.Base(ws.Path)
		processDir := e.workspaceMgr.GetProcessDir(ws, wp.Hash)

		proc := &Process{
			ID:          wp.Hash,
			Command:     wp.Command,
			StartTime:   wp.StartTime,
			EndTime:     wp.EndTime,
			StdoutFile:  filepath.Join(processDir, "stdout"),
			StderrFile:  filepath.Join(processDir, "stderr"),
			PID:         wp.PID,
			Status:      wp.Status,
			WorkspaceTS: workspaceTS,
			Hash:        wp.Hash,
			ExitCode:    wp.ExitCode,
		}

		return proc, true
	}

	return nil, false
}

// ReadOutput reads output from a file
func (e *Executor) ReadOutput(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ListWorkspaces returns all workspaces
func (e *Executor) ListWorkspaces() ([]*workspace.Workspace, error) {
	return e.workspaceMgr.ListWorkspaces()
}
