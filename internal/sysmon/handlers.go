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

	"mobileshell/pkg/httperror"
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

	search := r.URL.Query().Get("search")

	// Get current user's UID for filtering
	currentUID := uint32(os.Getuid())

	// Fetch and filter processes
	processes, err := GetUserProcesses(currentUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get processes: %w", err)
	}

	// Filter by search term
	if search != "" {
		var filtered []*ProcessInfo
		for _, p := range processes {
			if matchesSearch(p.Name, search) || matchesSearch(p.Cmdline, search) {
				filtered = append(filtered, p)
			}
		}
		processes = filtered
	}

	// Sort processes
	SortProcesses(processes, SortColumn(sortBy), SortOrder(order))

	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "hx-sysmon-processes.html", map[string]interface{}{
		"Processes": processes,
		"SortBy":    sortBy,
		"Order":     order,
		"BasePath":  basePath,
		"Search":    search,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %w", err)
	}
	return buf.Bytes(), nil
}

func matchesSearch(text, query string) bool {
	terms := strings.Fields(strings.ToLower(query))
	lowerText := strings.ToLower(text)
	for _, term := range terms {
		if !strings.Contains(lowerText, term) {
			return false
		}
	}
	return true
}

// HandleBulkSignal sends a signal to multiple processes (POST only)
func HandleBulkSignal(ctx context.Context, r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	signalStr := r.FormValue("signal")
	if signalStr == "" {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "No signal provided"}
	}

	signalNum, err := strconv.Atoi(signalStr)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Invalid signal number"}
	}

	pids := r.Form["pids"]
	if len(pids) == 0 {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "No processes selected"}
	}

	currentUID := uint32(os.Getuid())
	count := 0
	var lastErr error

	for _, pidStr := range pids {
		pid, err := strconv.ParseInt(pidStr, 10, 32)
		if err != nil {
			continue
		}

		err = SendSignalToProcess(int32(pid), signalNum, currentUID)
		if err != nil {
			lastErr = err
			continue
		}
		count++
	}

	signalName := "signal"
	if sig := GetAllSignals(); sig != nil {
		for _, s := range sig {
			if s.Number == signalNum {
				signalName = s.Name
				break
			}
		}
	}

	if count == 0 && lastErr != nil {
		return []byte(fmt.Sprintf(`<div class="alert alert-danger alert-dismissible fade show" role="alert">
			Failed to send %s: %v
			<button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
		</div>`, signalName, lastErr)), nil
	}

	return []byte(fmt.Sprintf(`<div class="alert alert-success alert-dismissible fade show" role="alert">
		%s sent to %d processes.
		<button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
	</div>`, signalName, count)), nil
}


// HandleProcessDetail renders the process detail page
func HandleProcessDetail(tmpl *template.Template, ctx context.Context, r *http.Request, basePath string, pidStr string) ([]byte, error) {
	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Invalid PID"}
	}

	// Get process detail with ownership verification
	currentUID := uint32(os.Getuid())
	detail, err := GetProcessDetailForUser(int32(pid), currentUID)
	if err != nil {
		if strings.Contains(err.Error(), "permission denied") {
			return nil, httperror.HTTPError{StatusCode: http.StatusForbidden, Message: "Cannot view process owned by another user"}
		}
		return nil, httperror.HTTPError{StatusCode: http.StatusGone, Message: fmt.Sprintf("Process %d has exited or is no longer accessible", pid)}
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
		return nil, httperror.HTTPError{StatusCode: http.StatusMethodNotAllowed, Message: "Method not allowed"}
	}

	pid, err := strconv.ParseInt(pidStr, 10, 32)
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Invalid PID"}
	}

	if err := r.ParseForm(); err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Failed to parse form"}
	}

	signalNum, err := strconv.Atoi(r.FormValue("signal"))
	if err != nil {
		return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: "Invalid signal"}
	}

	// Send signal with ownership verification
	currentUID := uint32(os.Getuid())
	err = SendSignalToProcess(int32(pid), signalNum, currentUID)
	if err != nil {
		if strings.Contains(err.Error(), "process has exited") || strings.Contains(err.Error(), "process not found") {
			return []byte(`<div class="alert alert-warning">Process has already exited</div>`), nil
		}
		if strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "does not belong to user") {
			return nil, httperror.HTTPError{StatusCode: http.StatusForbidden, Message: "Cannot signal process owned by another user"}
		}
		if strings.Contains(err.Error(), "invalid signal") {
			return nil, httperror.HTTPError{StatusCode: http.StatusBadRequest, Message: err.Error()}
		}
		return []byte(`<div class="alert alert-danger">Failed to send signal: ` + err.Error() + `</div>`), nil
	}

	return []byte(`<div class="alert alert-success">Signal sent successfully</div>`), nil
}
