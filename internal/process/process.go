package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Process struct {
	CommandId   string
	Command     string
	StartTime   time.Time
	OutputFile  string
	PID         int
	Completed   bool // true if process has finished
	WorkspaceTS string
	ExitCode    int
	Signal      string
	EndTime     time.Time
	ContentType string // MIME type of stdout output
	ProcessDir  string
}

func LoadProcessFromDir(processDir string) (*Process, error) {
	// Read command file
	cmdData, err := os.ReadFile(filepath.Join(processDir, "cmd"))
	if err != nil {
		return nil, fmt.Errorf("failed to read cmd file: %w", err)
	}
	proc := Process{
		Command:    string(cmdData),
		ProcessDir: processDir,
	}

	// Read starttime file
	startTimeData, err := os.ReadFile(filepath.Join(processDir, "starttime"))
	if err != nil {
		return nil, fmt.Errorf("failed to read starttime file: %w", err)
	}
	startTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(startTimeData)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse starttime: %w", err)
	}
	proc.StartTime = startTime

	// Read completed file
	completedData, err := os.ReadFile(filepath.Join(processDir, "completed"))
	if err != nil {
		return nil, fmt.Errorf("failed to read completed file: %w", err)
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

	return &proc, nil
}
