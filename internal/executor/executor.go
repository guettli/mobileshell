package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Process struct {
	ID         string    `json:"id"`
	Command    string    `json:"command"`
	StartTime  time.Time `json:"start_time"`
	StdoutFile string    `json:"stdout_file"`
	StderrFile string    `json:"stderr_file"`
	PID        int       `json:"pid"`
	Status     string    `json:"status"` // "running" or "completed"
}

type Executor struct {
	processes map[string]*Process
	mu        sync.RWMutex
	outputDir string
}

func New(outputDir string) (*Executor, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, err
	}

	e := &Executor{
		processes: make(map[string]*Process),
		outputDir: outputDir,
	}

	// Load existing processes from disk
	e.loadProcesses()

	return e, nil
}

func (e *Executor) Execute(command string) (*Process, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	stdoutFile := filepath.Join(e.outputDir, id+".stdout")
	stderrFile := filepath.Join(e.outputDir, id+".stderr")

	stdout, err := os.Create(stdoutFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stdout.Close() }()

	stderr, err := os.Create(stderrFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stderr.Close() }()

	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	proc := &Process{
		ID:         id,
		Command:    command,
		StartTime:  time.Now(),
		StdoutFile: stdoutFile,
		StderrFile: stderrFile,
		PID:        cmd.Process.Pid,
		Status:     "running",
	}

	e.processes[id] = proc
	e.saveProcesses()

	// Monitor process completion in background
	go func() {
		_ = cmd.Wait()
		e.mu.Lock()
		proc.Status = "completed"
		e.saveProcesses()
		e.mu.Unlock()
	}()

	return proc, nil
}

func (e *Executor) ListProcesses() []*Process {
	e.mu.RLock()
	defer e.mu.RUnlock()

	procs := make([]*Process, 0, len(e.processes))
	for _, p := range e.processes {
		procs = append(procs, p)
	}
	return procs
}

func (e *Executor) GetProcess(id string) (*Process, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	proc, ok := e.processes[id]
	return proc, ok
}

func (e *Executor) ReadOutput(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (e *Executor) saveProcesses() {
	stateFile := filepath.Join(e.outputDir, "processes.json")
	data, err := json.MarshalIndent(e.processes, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(stateFile, data, 0644)
}

func (e *Executor) loadProcesses() {
	stateFile := filepath.Join(e.outputDir, "processes.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &e.processes)
}
