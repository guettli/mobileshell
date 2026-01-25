package sysmon

import (
	"os"
	"testing"

	"github.com/shirou/gopsutil/v3/process"
)

func TestGetUserProcesses(t *testing.T) {
	t.Parallel()
	currentUID := uint32(os.Getuid())
	processes, err := GetUserProcesses(currentUID)
	if err != nil {
		t.Fatalf("GetUserProcesses failed: %v", err)
	}

	if len(processes) == 0 {
		t.Fatal("Expected at least one process for current user")
	}

	// Verify all returned processes belong to current user
	for _, p := range processes {
		proc, err := process.NewProcess(p.PID)
		if err != nil {
			// Process may have exited, skip
			continue
		}
		uids, err := proc.Uids()
		if err != nil {
			// Process may have exited, skip
			continue
		}
		if len(uids) > 0 && uint32(uids[0]) != currentUID {
			t.Errorf("Process %d (UID %d) does not belong to current user (UID %d)", p.PID, uids[0], currentUID)
		}
	}
}

func TestSortProcessesByCPU(t *testing.T) {
	t.Parallel()
	processes := []*ProcessInfo{
		{PID: 1, CPUPercent: 10.0},
		{PID: 2, CPUPercent: 50.0},
		{PID: 3, CPUPercent: 5.0},
	}

	// Test descending sort
	SortProcesses(processes, SortByCPU, SortDesc)
	if processes[0].PID != 2 || processes[1].PID != 1 || processes[2].PID != 3 {
		t.Errorf("CPU descending sort failed: got PIDs %d, %d, %d", processes[0].PID, processes[1].PID, processes[2].PID)
	}

	// Test ascending sort
	SortProcesses(processes, SortByCPU, SortAsc)
	if processes[0].PID != 3 || processes[1].PID != 1 || processes[2].PID != 2 {
		t.Errorf("CPU ascending sort failed: got PIDs %d, %d, %d", processes[0].PID, processes[1].PID, processes[2].PID)
	}
}

func TestSortProcessesByMemory(t *testing.T) {
	t.Parallel()
	processes := []*ProcessInfo{
		{PID: 1, MemoryMB: 100.0},
		{PID: 2, MemoryMB: 500.0},
		{PID: 3, MemoryMB: 50.0},
	}

	SortProcesses(processes, SortByMemory, SortDesc)
	if processes[0].PID != 2 || processes[1].PID != 1 || processes[2].PID != 3 {
		t.Errorf("Memory descending sort failed: got PIDs %d, %d, %d", processes[0].PID, processes[1].PID, processes[2].PID)
	}
}

func TestSortProcessesByIO(t *testing.T) {
	t.Parallel()
	processes := []*ProcessInfo{
		{PID: 1, IOReadMB: 10.0, IOWriteMB: 5.0},   // Total: 15
		{PID: 2, IOReadMB: 100.0, IOWriteMB: 50.0}, // Total: 150
		{PID: 3, IOReadMB: 1.0, IOWriteMB: 1.0},    // Total: 2
	}

	SortProcesses(processes, SortByIO, SortDesc)
	if processes[0].PID != 2 || processes[1].PID != 1 || processes[2].PID != 3 {
		t.Errorf("I/O descending sort failed: got PIDs %d, %d, %d", processes[0].PID, processes[1].PID, processes[2].PID)
	}
}

func TestSortProcessesByPID(t *testing.T) {
	t.Parallel()
	processes := []*ProcessInfo{
		{PID: 100},
		{PID: 50},
		{PID: 200},
	}

	SortProcesses(processes, SortByPID, SortAsc)
	if processes[0].PID != 50 || processes[1].PID != 100 || processes[2].PID != 200 {
		t.Errorf("PID ascending sort failed: got PIDs %d, %d, %d", processes[0].PID, processes[1].PID, processes[2].PID)
	}
}

func TestSortProcessesByName(t *testing.T) {
	t.Parallel()
	processes := []*ProcessInfo{
		{PID: 1, Name: "zsh"},
		{PID: 2, Name: "bash"},
		{PID: 3, Name: "python"},
	}

	SortProcesses(processes, SortByName, SortAsc)
	if processes[0].Name != "bash" || processes[1].Name != "python" || processes[2].Name != "zsh" {
		t.Errorf("Name ascending sort failed: got names %s, %s, %s", processes[0].Name, processes[1].Name, processes[2].Name)
	}
}

func TestGetProcessDetail(t *testing.T) {
	t.Parallel()
	// Use current process (guaranteed to exist)
	pid := int32(os.Getpid())
	detail, err := GetProcessDetail(pid)
	if err != nil {
		t.Fatalf("GetProcessDetail failed: %v", err)
	}

	if detail.PID != pid {
		t.Errorf("Expected PID %d, got %d", pid, detail.PID)
	}

	// Parent should exist (unless we're PID 1, which is unlikely)
	if detail.PPID > 0 && detail.ParentInfo == nil {
		// Note: Parent info may be nil if permission denied, so this is not always an error
		t.Logf("Parent process (PID %d) info not accessible (may be permission issue)", detail.PPID)
	}
}

func TestGetProcessDetailNonExistent(t *testing.T) {
	t.Parallel()
	// Use a PID that's unlikely to exist
	pid := int32(999999)
	_, err := GetProcessDetail(pid)

	if err == nil {
		t.Error("Expected error for non-existent process, got nil")
	}
}

func TestValidateSignal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		signal int
		valid  bool
	}{
		{1, true},    // SIGHUP
		{2, true},    // SIGINT
		{9, true},    // SIGKILL
		{15, true},   // SIGTERM
		{0, false},   // Invalid
		{-1, false},  // Invalid
		{999, false}, // Invalid
	}

	for _, tt := range tests {
		err := ValidateSignal(tt.signal)
		if tt.valid && err != nil {
			t.Errorf("ValidateSignal(%d) expected valid, got error: %v", tt.signal, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidateSignal(%d) expected invalid, got nil error", tt.signal)
		}
	}
}

func TestGetAllSignals(t *testing.T) {
	t.Parallel()
	signals := GetAllSignals()

	if len(signals) == 0 {
		t.Fatal("GetAllSignals returned empty list")
	}

	// Check that common signals are present
	foundSIGTERM := false
	foundSIGKILL := false
	foundSIGHUP := false

	for _, sig := range signals {
		if sig.Name == "SIGTERM" {
			foundSIGTERM = true
		}
		if sig.Name == "SIGKILL" {
			foundSIGKILL = true
		}
		if sig.Name == "SIGHUP" {
			foundSIGHUP = true
		}

		// Verify all signals have required fields
		if sig.Number == 0 || sig.Name == "" || sig.Description == "" {
			t.Errorf("Signal %v has missing fields", sig)
		}
	}

	if !foundSIGTERM || !foundSIGKILL || !foundSIGHUP {
		t.Error("Common signals (SIGTERM, SIGKILL, SIGHUP) not found in signal list")
	}
}

func TestVerifyProcessOwnership(t *testing.T) {
	t.Parallel()
	currentUID := uint32(os.Getuid())
	currentPID := int32(os.Getpid())

	// Test with current process (should succeed)
	err := VerifyProcessOwnership(currentPID, currentUID)
	if err != nil {
		t.Errorf("VerifyProcessOwnership failed for current process: %v", err)
	}

	// Test with wrong UID (should fail)
	err = VerifyProcessOwnership(currentPID, currentUID+9999)
	if err == nil {
		t.Error("VerifyProcessOwnership should fail for wrong UID")
	}

	// Test with non-existent PID (should fail)
	err = VerifyProcessOwnership(999999, currentUID)
	if err == nil {
		t.Error("VerifyProcessOwnership should fail for non-existent PID")
	}
}

func TestGetProcessDetailForUser(t *testing.T) {
	t.Parallel()
	currentUID := uint32(os.Getuid())
	currentPID := int32(os.Getpid())

	// Test with current process (should succeed)
	detail, err := GetProcessDetailForUser(currentPID, currentUID)
	if err != nil {
		t.Fatalf("GetProcessDetailForUser failed: %v", err)
	}
	if detail.PID != currentPID {
		t.Errorf("Expected PID %d, got %d", currentPID, detail.PID)
	}

	// Test with wrong UID (should fail)
	_, err = GetProcessDetailForUser(currentPID, currentUID+9999)
	if err == nil {
		t.Error("GetProcessDetailForUser should fail for wrong UID")
	}
}

func TestSendSignalToProcess(t *testing.T) {
	t.Parallel()
	currentUID := uint32(os.Getuid())

	// Test with invalid signal (should fail)
	err := SendSignalToProcess(int32(os.Getpid()), 999, currentUID)
	if err == nil {
		t.Error("SendSignalToProcess should fail for invalid signal")
	}

	// Test with non-existent PID (should fail)
	err = SendSignalToProcess(999999, 15, currentUID)
	if err == nil {
		t.Error("SendSignalToProcess should fail for non-existent PID")
	}

	// Test with wrong UID (should fail)
	err = SendSignalToProcess(int32(os.Getpid()), 0, currentUID+9999)
	if err == nil {
		t.Error("SendSignalToProcess should fail for wrong UID")
	}
}
