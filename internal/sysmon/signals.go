package sysmon

import (
	"fmt"
	"syscall"
)

// Signal represents a Unix signal
type Signal struct {
	Number      int
	Name        string
	Description string
}

// GetAllSignals returns all standard POSIX signals
func GetAllSignals() []Signal {
	return []Signal{
		{Number: int(syscall.SIGHUP), Name: "SIGHUP", Description: "Hangup"},
		{Number: int(syscall.SIGINT), Name: "SIGINT", Description: "Interrupt"},
		{Number: int(syscall.SIGQUIT), Name: "SIGQUIT", Description: "Quit"},
		{Number: int(syscall.SIGILL), Name: "SIGILL", Description: "Illegal instruction"},
		{Number: int(syscall.SIGTRAP), Name: "SIGTRAP", Description: "Trace/breakpoint trap"},
		{Number: int(syscall.SIGABRT), Name: "SIGABRT", Description: "Aborted"},
		{Number: int(syscall.SIGBUS), Name: "SIGBUS", Description: "Bus error"},
		{Number: int(syscall.SIGFPE), Name: "SIGFPE", Description: "Floating point exception"},
		{Number: int(syscall.SIGKILL), Name: "SIGKILL", Description: "Killed (uncatchable)"},
		{Number: int(syscall.SIGUSR1), Name: "SIGUSR1", Description: "User defined signal 1"},
		{Number: int(syscall.SIGSEGV), Name: "SIGSEGV", Description: "Segmentation fault"},
		{Number: int(syscall.SIGUSR2), Name: "SIGUSR2", Description: "User defined signal 2"},
		{Number: int(syscall.SIGPIPE), Name: "SIGPIPE", Description: "Broken pipe"},
		{Number: int(syscall.SIGALRM), Name: "SIGALRM", Description: "Alarm clock"},
		{Number: int(syscall.SIGTERM), Name: "SIGTERM", Description: "Terminated"},
		{Number: int(syscall.SIGSTKFLT), Name: "SIGSTKFLT", Description: "Stack fault"},
		{Number: int(syscall.SIGCHLD), Name: "SIGCHLD", Description: "Child exited"},
		{Number: int(syscall.SIGCONT), Name: "SIGCONT", Description: "Continue"},
		{Number: int(syscall.SIGSTOP), Name: "SIGSTOP", Description: "Stop (uncatchable)"},
		{Number: int(syscall.SIGTSTP), Name: "SIGTSTP", Description: "Terminal stop"},
		{Number: int(syscall.SIGTTIN), Name: "SIGTTIN", Description: "Background read from tty"},
		{Number: int(syscall.SIGTTOU), Name: "SIGTTOU", Description: "Background write to tty"},
		{Number: int(syscall.SIGURG), Name: "SIGURG", Description: "Urgent I/O condition"},
		{Number: int(syscall.SIGXCPU), Name: "SIGXCPU", Description: "CPU time limit exceeded"},
		{Number: int(syscall.SIGXFSZ), Name: "SIGXFSZ", Description: "File size limit exceeded"},
		{Number: int(syscall.SIGVTALRM), Name: "SIGVTALRM", Description: "Virtual timer expired"},
		{Number: int(syscall.SIGPROF), Name: "SIGPROF", Description: "Profiling timer expired"},
		{Number: int(syscall.SIGWINCH), Name: "SIGWINCH", Description: "Window size changed"},
		{Number: int(syscall.SIGIO), Name: "SIGIO", Description: "I/O possible"},
		{Number: int(syscall.SIGPWR), Name: "SIGPWR", Description: "Power failure"},
		{Number: int(syscall.SIGSYS), Name: "SIGSYS", Description: "Bad system call"},
	}
}

// ValidateSignal checks if a signal number is valid
func ValidateSignal(signum int) error {
	if signum <= 0 || signum > 31 {
		return fmt.Errorf("invalid signal number: %d", signum)
	}
	return nil
}
