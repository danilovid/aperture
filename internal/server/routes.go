package server

import (
	"log/slog"
	"net/http"
)

// Routes returns the HTTP handler with all routes.
func Routes(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// Health & readiness
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /ready", handleReady)

	// OpenAI-compatible API (placeholders for now)
	mux.HandleFunc("GET /v1/models", handleModels)
	mux.HandleFunc("POST /v1/chat/completions", handleChatCompletions)

	// Chain middleware
	handler := loggingMiddleware(mux, logger)
	handler = recoveryMiddleware(handler, logger)

	return handler
}
