package sysmon

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// ProcessInfo represents a system process with metrics
type ProcessInfo struct {
	PID            int32
	Name           string
	Cmdline        string
	Username       string
	CPUPercent     float64
	MemoryMB       float64 // RSS in MB
	MemoryPercent  float32
	IOReadMB       float64 // Cumulative
	IOWriteMB      float64 // Cumulative
	CreateTime     time.Time
	Status         string
	PPID           int32
	NumThreads     int32
}

// ProcessDetail extends ProcessInfo with additional details
type ProcessDetail struct {
	ProcessInfo
	CPUTimesUser   float64
	CPUTimesSystem float64
	ParentInfo     *ProcessInfo  // nil if parent not accessible
	ChildrenInfo   []*ProcessInfo
}

// SortColumn defines available sort options
type SortColumn string

const (
	SortByCPU    SortColumn = "cpu"
	SortByMemory SortColumn = "memory"
	SortByIO     SortColumn = "io"
	SortByPID    SortColumn = "pid"
	SortByName   SortColumn = "name"
)

// SortOrder defines sort direction
type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)

// GetUserProcesses returns all processes owned by the specified UID
func GetUserProcesses(uid uint32) ([]*ProcessInfo, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("failed to get processes: %w", err)
	}

	var userProcesses []*ProcessInfo
	for _, p := range procs {
		// Check if process belongs to the user
		uids, err := p.Uids()
		if err != nil || len(uids) == 0 || uint32(uids[0]) != uid {
			continue
		}

		// Fetch process info
		info := fetchProcessInfo(p)
		if info != nil {
			userProcesses = append(userProcesses, info)
		}
	}

	return userProcesses, nil
}

// fetchProcessInfo retrieves detailed information for a single process
func fetchProcessInfo(p *process.Process) *ProcessInfo {
	info := &ProcessInfo{
		PID: p.Pid,
	}

	// Get name (may fail for short-lived processes)
	if name, err := p.Name(); err == nil {
		info.Name = name
	}

	// Get command line
	if cmdline, err := p.Cmdline(); err == nil {
		info.Cmdline = cmdline
	}

	// Get username
	if username, err := p.Username(); err == nil {
		info.Username = username
	}

	// Get CPU percent (may fail)
	if cpuPercent, err := p.CPUPercent(); err == nil {
		info.CPUPercent = cpuPercent
	}

	// Get memory info
	if memInfo, err := p.MemoryInfo(); err == nil {
		info.MemoryMB = float64(memInfo.RSS) / 1024 / 1024 // Convert bytes to MB
	}

	// Get memory percent
	if memPercent, err := p.MemoryPercent(); err == nil {
		info.MemoryPercent = memPercent
	}

	// Get IO counters
	if ioCounters, err := p.IOCounters(); err == nil {
		info.IOReadMB = float64(ioCounters.ReadBytes) / 1024 / 1024
		info.IOWriteMB = float64(ioCounters.WriteBytes) / 1024 / 1024
	}

	// Get create time
	if createTime, err := p.CreateTime(); err == nil {
		info.CreateTime = time.Unix(0, createTime*int64(time.Millisecond))
	}

	// Get status
	if status, err := p.Status(); err == nil {
		if len(status) > 0 {
			info.Status = status[0]
		}
	}

	// Get parent PID
	if ppid, err := p.Ppid(); err == nil {
		info.PPID = ppid
	}

	// Get number of threads
	if numThreads, err := p.NumThreads(); err == nil {
		info.NumThreads = numThreads
	}

	return info
}

// GetProcessDetail retrieves detailed information for a specific process
func GetProcessDetail(pid int32) (*ProcessDetail, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("process not found: %w", err)
	}

	// Fetch basic info
	basicInfo := fetchProcessInfo(p)
	if basicInfo == nil {
		return nil, fmt.Errorf("failed to fetch process info")
	}

	detail := &ProcessDetail{
		ProcessInfo: *basicInfo,
	}

	// Get CPU times
	if cpuTimes, err := p.Times(); err == nil {
		detail.CPUTimesUser = cpuTimes.User
		detail.CPUTimesSystem = cpuTimes.System
	}

	// Get parent process info
	if basicInfo.PPID > 0 {
		parent, err := process.NewProcess(basicInfo.PPID)
		if err == nil {
			parentInfo := fetchProcessInfo(parent)
			if parentInfo != nil {
				detail.ParentInfo = parentInfo
			}
		}
	}

	// Get children processes
	children, err := p.Children()
	if err == nil {
		for _, child := range children {
			childInfo := fetchProcessInfo(child)
			if childInfo != nil {
				detail.ChildrenInfo = append(detail.ChildrenInfo, childInfo)
			}
		}
	}

	return detail, nil
}

// SortProcesses sorts the process list by the specified column and order
func SortProcesses(processes []*ProcessInfo, column SortColumn, order SortOrder) {
	sort.Slice(processes, func(i, j int) bool {
		var less bool

		switch column {
		case SortByCPU:
			less = processes[i].CPUPercent < processes[j].CPUPercent
		case SortByMemory:
			less = processes[i].MemoryMB < processes[j].MemoryMB
		case SortByIO:
			totalI := processes[i].IOReadMB + processes[i].IOWriteMB
			totalJ := processes[j].IOReadMB + processes[j].IOWriteMB
			less = totalI < totalJ
		case SortByPID:
			less = processes[i].PID < processes[j].PID
		case SortByName:
			less = processes[i].Name < processes[j].Name
		default:
			// Default to CPU
			less = processes[i].CPUPercent < processes[j].CPUPercent
		}

		// Reverse if descending order
		if order == SortDesc {
			return !less
		}
		return less
	})
}

// VerifyProcessOwnership checks if a process belongs to the specified user
func VerifyProcessOwnership(pid int32, uid uint32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	uids, err := p.Uids()
	if err != nil {
		return fmt.Errorf("failed to get process UIDs: %w", err)
	}

	if len(uids) == 0 || uint32(uids[0]) != uid {
		return fmt.Errorf("process does not belong to user")
	}

	return nil
}

// GetProcessDetailForUser retrieves detailed information for a process and verifies ownership
func GetProcessDetailForUser(pid int32, uid uint32) (*ProcessDetail, error) {
	// Get process detail
	detail, err := GetProcessDetail(pid)
	if err != nil {
		return nil, err
	}

	// Verify ownership
	if err := VerifyProcessOwnership(pid, uid); err != nil {
		return nil, fmt.Errorf("permission denied: %w", err)
	}

	return detail, nil
}

// SendSignalToProcess sends a signal to a process after verifying ownership
func SendSignalToProcess(pid int32, signal int, uid uint32) error {
	// Validate signal
	if err := ValidateSignal(signal); err != nil {
		return err
	}

	// Get process
	p, err := process.NewProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	// Verify ownership
	if err := VerifyProcessOwnership(pid, uid); err != nil {
		return err
	}

	// Send signal
	if err := p.SendSignal(syscall.Signal(signal)); err != nil {
		if strings.Contains(err.Error(), "no such process") {
			return fmt.Errorf("process has exited")
		}
		return fmt.Errorf("failed to send signal: %w", err)
	}

	return nil
}
