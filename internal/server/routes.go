package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

// Options configures the HTTP handler tree.
type Options struct {
	KeyStore storage.KeyStore
	LogStore storage.LogStore
	// DLPStore records rule matches; Inspector scans outbound requests.
	// DLP is disabled when Inspector is nil.
	DLPStore storage.DLPStore
	// PolicyStore holds per-key and default policies; DLPPolicy is the
	// fallback when it is nil or has no stored default.
	PolicyStore storage.PolicyStore
	Inspector   *inspector.Inspector
	DLPPolicy   inspector.Policy
	// Alerter delivers DLP events to a webhook; nil disables alerting.
	Alerter *alerter.Alerter
	// CustomProviders are user-defined OpenAI-compatible upstreams.
	CustomProviders []config.CustomProvider
	OpenAIBaseURL   string
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
		KeyStore:        o.KeyStore,
		LogStore:        o.LogStore,
		DLPStore:        o.DLPStore,
		PolicyStore:     o.PolicyStore,
		Inspector:       o.Inspector,
		DLPPolicy:       o.DLPPolicy,
		Alerter:         o.Alerter,
		CustomProviders: o.CustomProviders,
		OpenAIBaseURL:   o.OpenAIBaseURL,
		AdminAPIKey:     o.AdminAPIKey,
		ReadyCheck:      o.ReadyCheck,
		Logger:          o.Logger,
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

	// DLP: alerts
	mux.HandleFunc("GET /admin/alerts", h.handleAlertsGet)
	mux.HandleFunc("PUT /admin/alerts", h.handleAlertsPut)
	mux.HandleFunc("POST /admin/alerts/test", h.handleAlertsTest)

	// DLP: policies
	mux.HandleFunc("GET /admin/policies", h.handlePoliciesGet)
	mux.HandleFunc("PUT /admin/policies/default", h.handlePolicyPutDefault)
	mux.HandleFunc("PUT /admin/policies/keys/{id}", h.handlePolicyPutKey)
	mux.HandleFunc("DELETE /admin/policies/keys/{id}", h.handlePolicyDeleteKey)
	mux.HandleFunc("POST /admin/policies/test", h.handlePolicyTest)

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
