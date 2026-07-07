package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

// Options configures the HTTP handler tree.
type Options struct {
	KeyStore storage.KeyStore
	LogStore storage.LogStore
	// DLPStore records rule matches; Inspector scans outbound requests.
	// DLP is disabled when Inspector is nil.
	DLPStore      storage.DLPStore
	Inspector     *inspector.Inspector
	DLPPolicy     inspector.Policy
	OpenAIBaseURL string
	// AdminAPIKey guards all /admin/* routes with Bearer token auth; when
	// empty, admin routes are denied entirely (fail closed).
	AdminAPIKey string
	// AllowedOrigins is the CORS allowlist for browser clients.
	AllowedOrigins []string
	// ReadyCheck, when set, is called by GET /ready (e.g. a DB ping).
	ReadyCheck func(ctx context.Context) error
	Logger     *slog.Logger
}

// Routes returns the HTTP handler with all routes.
func Routes(o Options) http.Handler {
	h := &Handlers{
		KeyStore:      o.KeyStore,
		LogStore:      o.LogStore,
		DLPStore:      o.DLPStore,
		Inspector:     o.Inspector,
		DLPPolicy:     o.DLPPolicy,
		OpenAIBaseURL: o.OpenAIBaseURL,
		AdminAPIKey:   o.AdminAPIKey,
		ReadyCheck:    o.ReadyCheck,
		Logger:        o.Logger,
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

	// Admin: aperture keys
	mux.HandleFunc("GET /admin/keys", h.handleAdminListKeys)
	mux.HandleFunc("POST /admin/keys", h.handleAdminCreateKey)
	mux.HandleFunc("DELETE /admin/keys/{id}", h.handleAdminDeleteKey)

	// DLP: incident feed & summary
	mux.HandleFunc("GET /admin/dlp/events", h.handleDLPEvents)
	mux.HandleFunc("GET /admin/dlp/summary", h.handleDLPSummary)

	// Stats API (requires PostgreSQL / LogStore)
	mux.HandleFunc("GET /admin/stats/logs", h.handleStatsLogs)
	mux.HandleFunc("GET /admin/stats/summary", h.handleStatsSummary)
	mux.HandleFunc("GET /admin/stats/timeseries", h.handleStatsTimeseries)
	mux.HandleFunc("GET /admin/stats/models", h.handleStatsModels)

	handler := corsMiddleware(mux, o.AllowedOrigins)
	handler = loggingMiddleware(handler, o.Logger)
	handler = recoveryMiddleware(handler, o.Logger)

	return handler
}
