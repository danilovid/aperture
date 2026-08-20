package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Streaming handlers gate on w.(http.Flusher). The logging middleware wraps the
// writer, and embedding http.ResponseWriter does not promote Flush — so without
// an explicit Flush method every SSE response silently buffers. This locks the
// fix in place.
func TestLoggingMiddlewarePreservesFlusher(t *testing.T) {
	var sawFlusher, sawUnwrap bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawFlusher = w.(http.Flusher)
		_, sawUnwrap = w.(interface{ Unwrap() http.ResponseWriter })
	})

	h := loggingMiddleware(next, slog.Default())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !sawFlusher {
		t.Error("handler did not receive an http.Flusher — streaming responses would be buffered")
	}
	if !sawUnwrap {
		t.Error("wrapper does not expose Unwrap for http.ResponseController")
	}
}
