// Package server exposes the REST API and the Swagger UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/dantotech/monsieur-logger/api"
	"github.com/dantotech/monsieur-logger/internal/generator"
	"github.com/dantotech/monsieur-logger/internal/version"
	"github.com/dantotech/monsieur-logger/web"
)

// Server wires HTTP handlers to the run manager.
type Server struct {
	mgr  *generator.Manager
	host generator.Host
	log  *slog.Logger
}

// New returns a Server.
func New(mgr *generator.Manager, host generator.Host, log *slog.Logger) *Server {
	return &Server{mgr: mgr, host: host, log: log}
}

// Handler returns the HTTP handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)

	mux.HandleFunc("GET /api/v1/info", s.info)
	mux.HandleFunc("POST /api/v1/runs", s.startRun)
	mux.HandleFunc("GET /api/v1/runs", s.listRuns)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.getRun)
	mux.HandleFunc("DELETE /api/v1/runs/{id}", s.stopRun)

	sub, _ := fs.Sub(web.Swagger, "swagger")
	mux.HandleFunc("GET /swagger/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(api.OpenAPISpec)
	})
	mux.Handle("GET /swagger/", http.StripPrefix("/swagger/", http.FileServerFS(sub)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/", http.StatusFound)
	})

	return s.logRequests(mux)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		s.log.Debug("http request", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	lim := s.mgr.Limits()
	writeJSON(w, http.StatusOK, map[string]any{
		"app":       "monsieur-logger",
		"version":   version.Version,
		"commit":    version.Commit,
		"date":      version.Date,
		"pod":       s.host.Pod,
		"namespace": s.host.Namespace,
		"node":      s.host.Node,
		"limits": map[string]int{
			"max_logs_per_second":  lim.MaxLogsPerSecond,
			"max_log_length":       lim.MaxLogLength,
			"max_duration_seconds": lim.MaxDurationSeconds,
		},
	})
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var spec generator.Spec
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	st, err := s.mgr.Start(spec)
	switch {
	case errors.Is(err, generator.ErrRunExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, generator.ErrTooManyRuns), errors.Is(err, generator.ErrShutdown):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		w.Header().Set("Location", "/api/v1/runs/"+st.RunID)
		writeJSON(w, http.StatusCreated, st)
	}
}

func (s *Server) listRuns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.List())
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	st, err := s.mgr.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) stopRun(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	st, err := s.mgr.Stop(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
