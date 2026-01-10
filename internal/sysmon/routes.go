package sysmon

import (
	"context"
	"html/template"
	"net/http"
)

// RegisterRoutes registers all sysmon routes on the provided mux
func RegisterRoutes(
	mux *http.ServeMux,
	tmpl *template.Template,
	getBasePath func(*http.Request) string,
	authMiddleware func(http.HandlerFunc) http.HandlerFunc,
	wrapHandler func(func(context.Context, *http.Request) ([]byte, error)) http.HandlerFunc,
) {
	mux.HandleFunc("/sysmon", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return HandleSysmon(tmpl, ctx, r, getBasePath(r))
	})))

	mux.HandleFunc("/sysmon/hx-processes", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return HandleProcessList(tmpl, ctx, r, getBasePath(r))
	})))

	mux.HandleFunc("/sysmon/process/{pid}", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return HandleProcessDetail(tmpl, ctx, r, getBasePath(r), r.PathValue("pid"))
	})))

	mux.HandleFunc("/sysmon/process/{pid}/hx-signal", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return HandleSendSignal(ctx, r, r.PathValue("pid"))
	})))

	mux.HandleFunc("/sysmon/hx-bulk-signal", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return HandleBulkSignal(ctx, r)
	})))
}
