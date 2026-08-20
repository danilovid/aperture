package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/metrics"
)

// routeLabel reduces a request path to a bounded metric label: the matched
// pattern when the mux exposes one, otherwise a coarse prefix.
func routeLabel(r *http.Request) string {
	if p := r.Pattern; p != "" {
		if _, path, ok := strings.Cut(p, " "); ok {
			return path
		}
		return p
	}
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		return "/admin/*"
	}
	return r.URL.Path
}

type responseWriter struct {
	http.ResponseWriter
	status  int
	written int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}

// Flush forwards to the underlying writer. Embedding http.ResponseWriter does
// not promote Flush (it is not part of that interface), so without this the
// wrapper hides the server's http.Flusher and every streaming response would
// be buffered until the handler returns instead of reaching the client live.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the original writer to http.ResponseController.
func (rw *responseWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

func loggingMiddleware(next http.Handler, logger *slog.Logger, reg *metrics.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		// Path is the route pattern, not the raw URL, so ids never become
		// label values.
		reg.ObserveHTTP(routeLabel(r), rw.status, time.Since(start).Seconds())

		logger.Info("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Int64("bytes", rw.written),
			slog.Duration("dur", time.Since(start)),
		)
	})
}

// corsMiddleware reflects the request Origin only when it is in the allowlist.
// Requests without an Origin header (curl, server SDKs) are unaffected.
func corsMiddleware(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func recoveryMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("panic recovered", slog.Any("panic", err))
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
