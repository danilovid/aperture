package server

import (
	"log/slog"
	"net/http"

	"github.com/danilovid/aperture/internal/storage"
)

// RuntimeConfig is optional — for setting OpenAI key via admin panel (no DB mode).
type RuntimeConfig interface {
	SetOpenAIKey(key string)
	ClearKey()
	GetMaskedKey() string
	IsConfigured() bool
}

// Routes returns the HTTP handler with all routes.
// runtimeConfig can be nil when using PostgreSQL.
func Routes(ks storage.KeyStore, openAIBaseURL string, runtimeConfig RuntimeConfig, logger *slog.Logger) http.Handler {
	h := &Handlers{
		KeyStore:       ks,
		OpenAIBaseURL:  openAIBaseURL,
		RuntimeConfig:  runtimeConfig,
		Logger:         logger,
	}
	mux := http.NewServeMux()

	// Health & readiness
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /ready", h.handleReady)

	// OpenAI-compatible API
	mux.HandleFunc("GET /v1/models", h.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)

	// Admin API (no auth for test project)
	mux.HandleFunc("GET /admin/config", h.handleAdminGetConfig)
	mux.HandleFunc("POST /admin/config", h.handleAdminSetConfig)
	mux.HandleFunc("DELETE /admin/config", h.handleAdminDeleteConfig)
	mux.HandleFunc("POST /admin/keys", h.handleAdminCreateKey)
	mux.HandleFunc("GET /admin/keys", h.handleAdminListKeys)
	mux.HandleFunc("DELETE /admin/keys/{id}", h.handleAdminDeleteKey)

	// Chain middleware
	handler := corsMiddleware(mux)
	handler = loggingMiddleware(handler, logger)
	handler = recoveryMiddleware(handler, logger)

	return handler
}
