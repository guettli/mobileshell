package sysmon

import (
	"context"
	"html/template"
	"net/http"
)

// HandlerFunc is the signature for sysmon handlers
type HandlerFunc func(context.Context, *http.Request) ([]byte, error)

// RegisterRoutes registers all sysmon routes on the provided mux
func RegisterRoutes(
	mux *http.ServeMux,
	tmpl *template.Template,
	getBasePath func(*http.Request) string,
	authMiddleware func(http.HandlerFunc) http.HandlerFunc,
	wrapHandler func(HandlerFunc) http.HandlerFunc,
) {
	mux.HandleFunc("/sysmon", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return HandleSysmon(tmpl, ctx, r, getBasePath(r))
	})))

	mux.HandleFunc("/sysmon/hx-processes", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		return HandleProcessList(tmpl, ctx, r, getBasePath(r))
	})))

	mux.HandleFunc("/sysmon/process/{pid}", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		result, err := HandleProcessDetail(tmpl, ctx, r, getBasePath(r), r.PathValue("pid"))
		if httpErr, ok := err.(HTTPError); ok {
			return nil, HTTPError{httpErr.StatusCode, httpErr.Message}
		}
		return result, err
	})))

	mux.HandleFunc("/sysmon/process/{pid}/hx-signal", authMiddleware(wrapHandler(func(ctx context.Context, r *http.Request) ([]byte, error) {
		result, err := HandleSendSignal(ctx, r, r.PathValue("pid"))
		if httpErr, ok := err.(HTTPError); ok {
			return nil, HTTPError{httpErr.StatusCode, httpErr.Message}
		}
		return result, err
	})))
}
