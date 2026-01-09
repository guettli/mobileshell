package sysmon

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// HandleSysmon renders the main system monitor page
func HandleSysmon(tmpl *template.Template, ctx context.Context, r *http.Request, basePath string) ([]byte, error) {
	// Get initial sort params (default: CPU descending)
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "cpu"
	}
	order := r.URL.Query().Get("order")
	if order == "" {
		order = "desc"
	}

	var buf bytes.Buffer
	err := tmpl.ExecuteTemplate(&buf, "sysmon.html", map[string]interface{}{
		"BasePath": basePath,
		"SortBy":   sortBy,
		"Order":    order,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %w", err)
	}
	return buf.Bytes(), nil
}

// HandleProcessList returns the sortable process list (HTMX endpoint)
func HandleProcessList(tmpl *template.Template, ctx context.Context, r *http.Request, basePath string) ([]byte, error) {
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "cpu"
	}
	order := r.URL.Query().Get("order")
	if order == "" {
		order = "desc"
	}

	// Get current user's UID for filtering
	currentUID := uint32(os.Getuid())

	// Fetch and filter processes
	processes, err := GetUserProcesses(currentUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get processes: %w", err)
	}

	// Sort processes
	SortProcesses(processes, SortColumn(sortBy), SortOrder(order))

	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "hx-sysmon-processes.html", map[string]interface{}{
		"Processes": processes,
		"SortBy":    sortBy,
		"Order":     order,
		"BasePath":  basePath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %w", err)
	}
	return buf.Bytes(), nil
}

// HandleProcessDetail renders the process detail page
func HandleProcessDetail(tmpl *template.Template, ctx context.Context, r *http.Request, basePath string, pidStr string) ([]byte, error) {
	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid PID: %w", err)
	}

	// Get process detail with ownership verification
	currentUID := uint32(os.Getuid())
	detail, err := GetProcessDetailForUser(int32(pid), currentUID)
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("forbidden: cannot view process owned by another user")
		}
		return nil, fmt.Errorf("process %d has exited or is no longer accessible", pid)
	}

	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "sysmon-process-detail.html", map[string]interface{}{
		"Process":  detail,
		"Signals":  GetAllSignals(),
		"BasePath": basePath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %w", err)
	}
	return buf.Bytes(), nil
}

// HandleSendSignal sends a signal to a process (POST only)
func HandleSendSignal(ctx context.Context, r *http.Request, pidStr string) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("method not allowed")
	}

	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid PID: %w", err)
	}

	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("failed to parse form: %w", err)
	}

	signalNum, err := strconv.Atoi(r.FormValue("signal"))
	if err != nil {
		return nil, fmt.Errorf("invalid signal: %w", err)
	}

	// Send signal with ownership verification
	currentUID := uint32(os.Getuid())
	err = SendSignalToProcess(int32(pid), signalNum, currentUID)
	if err != nil {
		if strings.Contains(err.Error(), "process has exited") || strings.Contains(err.Error(), "process not found") {
			return []byte(`<div class="alert alert-warning">Process has already exited</div>`), nil
		}
		if strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "does not belong to user") {
			return nil, fmt.Errorf("forbidden: cannot signal process owned by another user")
		}
		if strings.Contains(err.Error(), "invalid signal") {
			return nil, fmt.Errorf("bad request: %s", err.Error())
		}
		return []byte(`<div class="alert alert-danger">Failed to send signal: ` + err.Error() + `</div>`), nil
	}

	return []byte(`<div class="alert alert-success">Signal sent successfully</div>`), nil
}
