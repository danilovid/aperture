package server

import (
	"log/slog"
	"net/http"

	"github.com/danilovid/aperture/internal/storage"
)

// Routes returns the HTTP handler with all routes.
// adminAPIKey guards all /admin/* routes with Bearer token auth; when empty,
// admin routes are denied entirely (fail closed).
// allowedOrigins is the CORS allowlist for browser clients.
func Routes(ks storage.KeyStore, ls storage.LogStore, openAIBaseURL, adminAPIKey string, allowedOrigins []string, logger *slog.Logger) http.Handler {
	h := &Handlers{
		KeyStore:      ks,
		LogStore:      ls,
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

	// Admin: provider key config
	mux.HandleFunc("GET /admin/config", h.handleAdminGetConfig)
	mux.HandleFunc("POST /admin/config", h.handleAdminSetConfig)
	mux.HandleFunc("DELETE /admin/config", h.handleAdminDeleteConfig)

	// Admin: aperture keys (for future multi-user)
	mux.HandleFunc("GET /admin/keys", h.handleAdminListKeys)
	mux.HandleFunc("DELETE /admin/keys/{id}", h.handleAdminDeleteKey)

	// Stats API (requires PostgreSQL / LogStore)
	mux.HandleFunc("GET /admin/stats/logs", h.handleStatsLogs)
	mux.HandleFunc("GET /admin/stats/summary", h.handleStatsSummary)
	mux.HandleFunc("GET /admin/stats/timeseries", h.handleStatsTimeseries)
	mux.HandleFunc("GET /admin/stats/models", h.handleStatsModels)

	handler := corsMiddleware(mux, allowedOrigins)
	handler = loggingMiddleware(handler, logger)
	handler = recoveryMiddleware(handler, logger)

	return handler
}
