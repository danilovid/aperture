package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/auth"
	"github.com/danilovid/aperture/internal/storage"
)

// Service tokens are how CI and scripts reach the admin API. A person signs in
// and carries a role; a machine carries a token and its scopes. The two meet
// in adminOrg, which answers the same question for both: which organization is
// this, and may the caller do what it is asking?
//
// The prefix is what tells a service token from an aperture key without a
// database round trip, and it is deliberately not the "ap-" agents use: a
// token pasted into the wrong field should fail immediately rather than
// mysteriously.
const (
	serviceTokenPrefix = "apt_"
	// serviceTokenHintLen is how much of the token the console may show. Ten
	// characters names a token without helping anybody guess the rest.
	serviceTokenHintLen = 10
	// serviceTokenLifetime is how long a token lasts when the caller does not
	// say. Ninety days is long enough not to be a weekly chore and short
	// enough that a forgotten token stops working.
	serviceTokenLifetime = 90 * 24 * time.Hour
)

// scopeByRoute says which scope each admin endpoint needs. It is a table
// rather than a line in every handler for the same reason requireRole is one
// function: permission rules that are spread out are permission rules that
// disagree with each other.
//
// An endpoint that is not listed cannot be reached with a service token at
// all. That is the default on purpose — members, invitations, organization
// settings and the tokens themselves are a person's business, and a machine
// that needs them is a machine somebody should think about first.
var scopeByRoute = map[string]storage.Scope{
	"GET /admin/dlp/events":       storage.ScopeEventsRead,
	"GET /admin/dlp/summary":      storage.ScopeEventsRead,
	"GET /admin/dlp/report":       storage.ScopeEventsRead,
	"GET /admin/stats/logs":       storage.ScopeEventsRead,
	"GET /admin/stats/summary":    storage.ScopeEventsRead,
	"GET /admin/stats/models":     storage.ScopeEventsRead,
	"GET /admin/stats/timeseries": storage.ScopeEventsRead,

	"GET /admin/keys":         storage.ScopeKeysRead,
	"POST /admin/keys":        storage.ScopeKeysWrite,
	"DELETE /admin/keys/{id}": storage.ScopeKeysWrite,
	"GET /admin/config":       storage.ScopeKeysRead,
	// Providers hold the organization's upstream credentials, so they go
	// with the keys: reading needs keys:read, changing them keys:write.
	"GET /admin/providers":           storage.ScopeKeysRead,
	"PUT /admin/providers/{name}":    storage.ScopeKeysWrite,
	"DELETE /admin/providers/{name}": storage.ScopeKeysWrite,
	"POST /admin/providers/test":     storage.ScopeKeysWrite,
	"POST /admin/config":             storage.ScopeKeysWrite,
	"DELETE /admin/config":           storage.ScopeKeysWrite,

	"GET /admin/policies":                   storage.ScopeEventsRead,
	"PUT /admin/policies/default":           storage.ScopePoliciesWrite,
	"PUT /admin/policies/keys/{id}":         storage.ScopePoliciesWrite,
	"DELETE /admin/policies/keys/{id}":      storage.ScopePoliciesWrite,
	"POST /admin/policies/keys/{id}/mute":   storage.ScopePoliciesWrite,
	"POST /admin/policies/keys/{id}/unmute": storage.ScopePoliciesWrite,
	"GET /admin/limits":                     storage.ScopeEventsRead,
	// Testing a policy writes nothing: it answers "what would this do to this
	// text", which is the check CI wants before a policy change lands.
	"POST /admin/policies/test": storage.ScopeEventsRead,
	// Alerts are DLP configuration like policies: reading them goes with
	// reading incidents, changing where they go with changing policies.
	"GET /admin/alerts":              storage.ScopeEventsRead,
	"PUT /admin/alerts":              storage.ScopePoliciesWrite,
	"POST /admin/alerts/test":        storage.ScopePoliciesWrite,
	"PUT /admin/limits/default":      storage.ScopePoliciesWrite,
	"PUT /admin/limits/keys/{id}":    storage.ScopePoliciesWrite,
	"DELETE /admin/limits/keys/{id}": storage.ScopePoliciesWrite,
}

// scopeForRequest returns the scope this route needs and whether a service
// token may reach it at all.
func scopeForRequest(r *http.Request) (storage.Scope, bool) {
	// r.Pattern is the route the mux matched, which is what the table is
	// keyed by. An unrouted request never reaches a handler, so an empty
	// pattern means something wrapped this outside the mux — fail closed.
	scope, ok := scopeByRoute[r.Pattern]
	return scope, ok
}

// tokenResponse carries the token exactly once, at creation, like an
// invitation: the database holds only its hash.
type tokenResponse struct {
	storage.ServiceToken
	Token string `json:"token"`
}

func (h *Handlers) handleCreateServiceToken(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	var req struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresIn string   `json:"expires_in"` // Go duration, e.g. "720h"; empty means the default
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "name is required — a token nobody can identify is a token nobody will revoke",
		})
		return
	}
	scopes, err := parseScopes(req.Scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	expires := time.Now().Add(serviceTokenLifetime)
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err != nil || d <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": `expires_in must be a positive Go duration, such as "720h"`,
			})
			return
		}
		expires = time.Now().Add(d)
	}

	// The hash NewSessionToken returns is of the raw value; what the caller
	// will present is the prefixed one, so that is what gets hashed.
	raw, _, err := auth.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the token"})
		return
	}
	token := serviceTokenPrefix + raw
	created, err := h.AccountStore.CreateServiceToken(r.Context(), storage.ServiceToken{
		OrgID: c.OrgID, Name: name, Scopes: scopes, CreatedBy: c.User.ID,
		Hint: token[:serviceTokenHintLen] + "…", ExpiresAt: &expires,
	}, auth.HashSessionToken(token))
	if err != nil {
		h.Logger.Error("create service token failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the token"})
		return
	}

	h.Logger.Info("service token created",
		"org", c.OrgID, "name", name, "scopes", req.Scopes, "by", c.User.Email)
	writeJSON(w, http.StatusCreated, tokenResponse{ServiceToken: *created, Token: token})
}

// parseScopes validates what the caller asked for. An empty list is refused
// rather than quietly meaning "everything" or "nothing": both would be a
// surprise, and one of them would be a dangerous one.
func parseScopes(in []string) ([]storage.Scope, error) {
	if len(in) == 0 {
		return nil, errors.New("at least one scope is required (" + scopeList() + ")")
	}
	seen := map[storage.Scope]bool{}
	out := make([]storage.Scope, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if !storage.ValidScope(s) {
			return nil, errors.New("unknown scope " + s + " (want one of: " + scopeList() + ")")
		}
		scope := storage.Scope(s)
		if seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out, nil
}

func scopeList() string {
	names := make([]string, 0, len(storage.AllScopes))
	for _, s := range storage.AllScopes {
		names = append(names, string(s))
	}
	return strings.Join(names, ", ")
}

func (h *Handlers) handleListServiceTokens(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	tokens, err := h.AccountStore.ServiceTokensOf(r.Context(), c.OrgID)
	if err != nil {
		h.Logger.Error("list service tokens failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load tokens"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens, "scopes": storage.AllScopes})
}

func (h *Handlers) handleRevokeServiceToken(w http.ResponseWriter, r *http.Request) {
	c := h.requireRole(w, r, storage.RoleAdmin)
	if c == nil {
		return
	}
	if err := h.AccountStore.RevokeServiceToken(r.Context(), c.OrgID, r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such token"})
		return
	}
	h.Logger.Info("service token revoked", "org", c.OrgID, "by", c.User.Email)
	w.WriteHeader(http.StatusNoContent)
}
