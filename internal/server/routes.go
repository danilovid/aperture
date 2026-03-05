package server

import (
	"log/slog"
	"net/http"

	"github.com/danilovid/aperture/internal/storage"
)

// Routes returns the HTTP handler with all routes.
func Routes(ks storage.KeyStore, openAIBaseURL, adminAPIKey string, logger *slog.Logger) http.Handler {
	h := &Handlers{
		KeyStore:      ks,
		OpenAIBaseURL: openAIBaseURL,
		AdminAPIKey:   adminAPIKey,
		Logger:        logger,
	}
	mux := http.NewServeMux()

	// Health & readiness
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /ready", h.handleReady)

	// OpenAI-compatible API
	mux.HandleFunc("GET /v1/models", h.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)

	// Admin API
	mux.HandleFunc("POST /admin/keys", h.handleAdminCreateKey)
	mux.HandleFunc("GET /admin/keys", h.handleAdminListKeys)
	mux.HandleFunc("DELETE /admin/keys/{id}", h.handleAdminDeleteKey)

	// Chain middleware
	handler := loggingMiddleware(mux, logger)
	handler = recoveryMiddleware(handler, logger)

	return handler
}
