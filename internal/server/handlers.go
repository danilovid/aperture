package server

import (
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danilovid/aperture/internal/provider"
)

// Handlers holds dependencies for API handlers.
type Handlers struct {
	Provider provider.Provider
	Logger   *slog.Logger
}

func (h *Handlers) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (h *Handlers) handleReady(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

func (h *Handlers) handleModels(w http.ResponseWriter, r *http.Request) {
	body, contentType, status, err := h.Provider.Models(r.Context())
	if err != nil {
		h.Logger.Error("models request failed", "err", err)
		http.Error(w, `{"error":"failed to fetch models"}`, http.StatusBadGateway)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	io.Copy(w, body)
}

func (h *Handlers) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}

	body, respContentType, status, err := h.Provider.ChatCompletions(r.Context(), r.Body, contentType)
	if err != nil {
		h.Logger.Error("chat completions request failed", "err", err)
		http.Error(w, `{"error":"failed to proxy request"}`, http.StatusBadGateway)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", respContentType)
	w.WriteHeader(status)

	// For SSE streaming, flush each chunk so the client receives data immediately
	if flusher, ok := w.(http.Flusher); ok && isStreaming(respContentType) {
		buf := make([]byte, 32*1024)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				flusher.Flush()
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return
			}
		}
	} else {
		io.Copy(w, body)
	}
}

func isStreaming(contentType string) bool {
	return strings.HasPrefix(contentType, "text/event-stream")
}
