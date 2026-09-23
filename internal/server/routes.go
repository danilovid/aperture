package server

import (
	"context"
	"github.com/danilovid/aperture/internal/oauth"
	"github.com/danilovid/aperture/internal/provider"
	"log/slog"
	"net/http"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/metrics"
	"github.com/danilovid/aperture/internal/storage"
)

// Options configures the HTTP handler tree.
type Options struct {
	KeyStore storage.KeyStore
	// AccountStore enables people, organizations and sessions. Nil leaves the
	// gateway single-tenant, guarded by the instance admin key alone.
	AccountStore storage.AccountStore
	// AuditStore keeps the journal of who changed what. Nil records nothing.
	AuditStore storage.AuditStore
	// ProviderStore holds each organization's upstreams. Nil means the
	// environment decides for everybody, as without a database.
	ProviderStore storage.ProviderStore
	LogStore      storage.LogStore
	// DLPStore records rule matches; Inspector scans outbound requests.
	// DLP is disabled when Inspector is nil.
	DLPStore storage.DLPStore
	// PolicyStore holds per-key and default policies; DLPPolicy is the
	// fallback when it is nil or has no stored default.
	PolicyStore storage.PolicyStore
	// LimitStore holds per-key budgets and rate limits; Tracker counts against
	// them. Both nil disables enforcement.
	LimitStore storage.LimitStore
	Tracker    *limits.Tracker
	// Metrics collects Prometheus counters; nil disables /metrics.
	Metrics   *metrics.Registry
	Inspector *inspector.Inspector
	DLPPolicy inspector.Policy
	// Alerter delivers DLP events to a webhook; nil disables alerting.
	Alerter *alerter.Alerter
	// CustomProviders are user-defined OpenAI-compatible upstreams.
	CustomProviders []config.CustomProvider
	OpenAIBaseURL   string
	// AnthropicBaseURL overrides the upstream for POST /v1/messages.
	AnthropicBaseURL string
	// JevBaseURL overrides the Jev decision API host.
	JevBaseURL string
	// AdminAPIKey guards all /admin/* routes with Bearer token auth; when
	// empty, admin routes are denied entirely (fail closed).
	AdminAPIKey string
	// AllowedOrigins is the CORS allowlist for browser clients.
	AllowedOrigins []string
	// OAuthProviders are the identity providers people may sign in with.
	OAuthProviders []*oauth.Provider
	// OAuthStateKey signs the state a sign-in carries through the provider.
	OAuthStateKey []byte
	// PublicURL is where this installation is reached, for OAuth redirects.
	// Empty means "whatever host the request came in on".
	PublicURL string
	// ReadyCheck, when set, is called by GET /ready (e.g. a DB ping).
	ReadyCheck func(ctx context.Context) error
	Logger     *slog.Logger
}

// Routes returns the HTTP handler with all routes.
func Routes(o Options) http.Handler {
	h := &Handlers{
		KeyStore:         o.KeyStore,
		AccountStore:     o.AccountStore,
		AuditStore:       o.AuditStore,
		ProviderStore:    o.ProviderStore,
		transports:       provider.NewTransports(),
		logins:           newLoginLimiter(),
		LogStore:         o.LogStore,
		DLPStore:         o.DLPStore,
		PolicyStore:      o.PolicyStore,
		LimitStore:       o.LimitStore,
		Tracker:          o.Tracker,
		Metrics:          o.Metrics,
		Inspector:        o.Inspector,
		DLPPolicy:        o.DLPPolicy,
		Alerter:          o.Alerter,
		CustomProviders:  o.CustomProviders,
		OpenAIBaseURL:    o.OpenAIBaseURL,
		AnthropicBaseURL: o.AnthropicBaseURL,
		JevBaseURL:       o.JevBaseURL,
		AdminAPIKey:      o.AdminAPIKey,
		oauthProviders:   o.OAuthProviders,
		oauthStateKey:    o.OAuthStateKey,
		oauthClient:      oauth.HTTPClient(),
		publicURL:        o.PublicURL,
		ReadyCheck:       o.ReadyCheck,
		Logger:           o.Logger,
	}
	mux := http.NewServeMux()

	// People: sign-in, registration by invitation, the current session.
	mux.HandleFunc("POST /api/auth/register", h.handleRegister)
	mux.HandleFunc("POST /api/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", h.handleLogout)
	mux.HandleFunc("GET /api/auth/me", h.handleMe)
	mux.HandleFunc("POST /api/auth/switch-org", h.handleSwitchOrg)

	// Signing in through Google, GitHub or Yandex, and managing those ways in.
	mux.HandleFunc("GET /api/auth/providers", h.handleOAuthProviders)
	mux.HandleFunc("POST /api/auth/oauth/{provider}/start", h.handleOAuthStart)
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", h.handleOAuthCallback)
	mux.HandleFunc("GET /api/auth/identities", h.handleIdentities)
	mux.HandleFunc("DELETE /api/auth/identities/{id}", h.handleUnlinkIdentity)

	// Organization membership.
	mux.HandleFunc("GET /api/members", h.handleMembers)
	mux.HandleFunc("PUT /api/members/{id}/role", h.handleSetMemberRole)
	mux.HandleFunc("DELETE /api/members/{id}", h.handleRemoveMember)
	mux.HandleFunc("POST /api/invitations", h.handleCreateInvitation)
	mux.HandleFunc("GET /api/invitations", h.handleListInvitations)
	mux.HandleFunc("DELETE /api/invitations/{id}", h.handleRevokeInvitation)
	// Joining, as opposed to being invited: the door for somebody who already
	// has an account and is being brought into a second organization.
	mux.HandleFunc("POST /api/invitations/accept", h.handleAcceptInvitation)
	// What an invitation is for, before anybody types a password into it.
	mux.HandleFunc("POST /api/invitations/lookup", h.handleLookupInvitation)

	// The organization the caller is signed in to.
	mux.HandleFunc("PATCH /api/organizations/current", h.handleRenameOrganization)
	mux.HandleFunc("DELETE /api/organizations/current", h.handleDeleteOrganization)
	mux.HandleFunc("POST /api/organizations/current/leave", h.handleLeaveOrganization)

	// Credentials for CI and scripts.
	mux.HandleFunc("GET /api/tokens", h.handleListServiceTokens)
	mux.HandleFunc("POST /api/tokens", h.handleCreateServiceToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", h.handleRevokeServiceToken)

	// The operator of the installation, authenticated with ADMIN_API_KEY.
	mux.HandleFunc("POST /api/instance/organizations", h.handleCreateOrganization)
	mux.HandleFunc("POST /api/instance/organizations/{id}/restore", h.handleRestoreOrganization)
	// Bringing someone into an organization that already exists — above all
	// the default one, which a migration created and nobody was invited to.
	mux.HandleFunc("POST /api/instance/organizations/{id}/invitations", h.handleInviteToOrganization)

	// Health & readiness
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /ready", h.handleReady)
	mux.HandleFunc("GET /metrics", h.handleMetrics)

	// OpenAI-compatible API
	mux.HandleFunc("GET /v1/models", h.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)
	// Native Anthropic Messages API — lets Claude Code and other Anthropic
	// clients be proxied by pointing ANTHROPIC_BASE_URL here.
	mux.HandleFunc("POST /v1/messages", h.handleMessages)
	// OpenAI Responses API — what Codex-style agents speak.
	mux.HandleFunc("POST /v1/responses", h.handleResponses)
	// Jev decision API: business fields in, a typed decision out. Same paths
	// as upstream, so an agent switches by changing its base URL.
	mux.HandleFunc("POST /api/v1/decisions", h.handleJevDecision)
	mux.HandleFunc("POST /api/v1/decisions/{preset}", h.handleJevDecision)

	// Admin: provider key config
	mux.HandleFunc("GET /admin/config", h.handleAdminGetConfig)
	mux.HandleFunc("POST /admin/config", h.handleAdminSetConfig)
	mux.HandleFunc("DELETE /admin/config", h.handleAdminDeleteConfig)

	// Admin: the organization's upstreams
	mux.HandleFunc("GET /admin/providers", h.handleProvidersList)
	mux.HandleFunc("PUT /admin/providers/{name}", h.handleProviderPut)
	mux.HandleFunc("DELETE /admin/providers/{name}", h.handleProviderDelete)
	mux.HandleFunc("POST /admin/providers/test", h.handleProviderTest)

	// Admin: aperture keys
	mux.HandleFunc("GET /admin/keys", h.handleAdminListKeys)
	mux.HandleFunc("POST /admin/keys", h.handleAdminCreateKey)
	mux.HandleFunc("DELETE /admin/keys/{id}", h.handleAdminDeleteKey)

	// DLP: incident feed & summary
	mux.HandleFunc("GET /admin/dlp/events", h.handleDLPEvents)
	mux.HandleFunc("GET /admin/dlp/summary", h.handleDLPSummary)
	mux.HandleFunc("GET /admin/dlp/report", h.handleDLPReport)

	// DLP: alerts
	mux.HandleFunc("GET /admin/alerts", h.handleAlertsGet)
	mux.HandleFunc("PUT /admin/alerts", h.handleAlertsPut)
	mux.HandleFunc("POST /admin/alerts/test", h.handleAlertsTest)

	// DLP: policies
	mux.HandleFunc("GET /admin/policies", h.handlePoliciesGet)
	mux.HandleFunc("PUT /admin/policies/default", h.handlePolicyPutDefault)
	mux.HandleFunc("PUT /admin/policies/keys/{id}", h.handlePolicyPutKey)
	mux.HandleFunc("DELETE /admin/policies/keys/{id}", h.handlePolicyDeleteKey)
	mux.HandleFunc("POST /admin/policies/keys/{id}/mute", h.handlePolicyMute)
	mux.HandleFunc("POST /admin/policies/keys/{id}/unmute", h.handlePolicyUnmute)
	mux.HandleFunc("POST /admin/policies/test", h.handlePolicyTest)

	// Who changed what, for owners and admins
	mux.HandleFunc("GET /admin/audit", h.handleAuditList)

	// Per-key budgets and rate limits
	mux.HandleFunc("GET /admin/limits", h.handleLimitsGet)
	mux.HandleFunc("PUT /admin/limits/default", h.handleLimitsPutDefault)
	mux.HandleFunc("PUT /admin/limits/keys/{id}", h.handleLimitsPutKey)
	mux.HandleFunc("DELETE /admin/limits/keys/{id}", h.handleLimitsDeleteKey)

	// Stats API (requires PostgreSQL / LogStore)
	mux.HandleFunc("GET /admin/stats/logs", h.handleStatsLogs)
	mux.HandleFunc("GET /admin/stats/summary", h.handleStatsSummary)
	mux.HandleFunc("GET /admin/stats/timeseries", h.handleStatsTimeseries)
	mux.HandleFunc("GET /admin/stats/models", h.handleStatsModels)

	// Sessions resolve before CSRF so a cookie-less request is never asked
	// for a token it has no way to hold.
	handler := h.csrfMiddleware(mux)
	handler = h.sessionMiddleware(handler)
	handler = corsMiddleware(handler, o.AllowedOrigins)
	handler = loggingMiddleware(handler, o.Logger, o.Metrics)
	handler = recoveryMiddleware(handler, o.Logger)

	return handler
}
