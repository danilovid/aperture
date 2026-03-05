package server

import (
	"log/slog"
	"net/http"

	"github.com/danilovid/aperture/internal/provider"
)

// Routes returns the HTTP handler with all routes.
func Routes(p provider.Provider, logger *slog.Logger) http.Handler {
	h := &Handlers{Provider: p, Logger: logger}
	mux := http.NewServeMux()

	// Health & readiness
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /ready", h.handleReady)

	// OpenAI-compatible API
	mux.HandleFunc("GET /v1/models", h.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)

	// Chain middleware
	handler := loggingMiddleware(mux, logger)
	handler = recoveryMiddleware(handler, logger)

	return handler
}
