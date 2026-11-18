// Package httpx contains HTTP plumbing shared by every module: router, middleware, JSON helpers.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/sangmul/medikmu-backend/internal/platform/config"
)

// Mount registers a module's routes on the mux. Patterns carry the full path, e.g. "POST /api/v1/auth/login".
type Mount func(mux *http.ServeMux)

// NewRouter builds the root handler and mounts each module's routes.
func NewRouter(cfg config.Config, mounts ...Mount) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	for _, m := range mounts {
		m(mux)
	}

	// CORS sits next to the mux so it can answer preflight (OPTIONS) requests the mux would reject with 405.
	return chain(mux, recoverer, securityHeaders, requestLogger, CORS(cfg.CORSAllowedOrigins))
}

// securityHeaders sets headers that are safe for every response of a JSON API: browsers must not sniff
// a JSON body into something executable, and URLs of this API must not leak through the Referer header.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Readyz mounts GET /readyz: 200 when check passes (e.g. a database ping), 503 otherwise.
// /healthz only says the process is up; /readyz says it can serve traffic.
func Readyz(check func(context.Context) error) Mount {
	return func(mux *http.ServeMux) {
		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := check(ctx); err != nil {
				slog.Error("readiness check failed", "error", err)
				JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
				return
			}
			JSON(w, http.StatusOK, map[string]string{"status": "ready"})
		})
	}
}

// maxBodyBytes caps JSON request bodies.
const maxBodyBytes = 1 << 20 // 1 MiB

// DecodeJSON reads a single JSON object into dst. Unknown fields and trailing data are rejected.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after JSON body")
	}
	return nil
}

// ErrorDetails is like Error but includes machine-readable details (e.g. per-field validation errors).
func ErrorDetails(w http.ResponseWriter, status int, code, message string, details any) {
	JSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}

// JSON writes v as a JSON response.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "error", err)
	}
}

// Error writes the standard error envelope (see docs/api-conventions.md).
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

type middleware func(http.Handler) http.Handler

func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "panic", rec, "path", r.URL.Path)
				Error(w, http.StatusInternalServerError, "internal_error", "Terjadi kesalahan pada server.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "duration_ms", time.Since(start).Milliseconds())
	})
}
