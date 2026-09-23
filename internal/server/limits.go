package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/storage"
)

// limitsFor resolves the ceilings for a key: its own entry, else the default.
func (h *Handlers) limitsFor(ctx context.Context, orgID, keyID string) limits.Limits {
	if h.LimitStore == nil {
		return limits.Limits{}
	}
	if l, ok, err := h.LimitStore.GetLimits(ctx, orgID, keyID); err == nil && ok {
		return l
	} else if err != nil {
		h.Logger.Error("limits lookup failed, using default", "err", err, "key_id", keyID)
	}
	l, err := h.LimitStore.GetDefaultLimits(ctx, orgID)
	if err != nil {
		h.Logger.Error("default limits lookup failed", "err", err)
		return limits.Limits{}
	}
	return l
}

// enforceLimits checks the key's budget and rate ceiling before any work is
// done. It returns false when the request was rejected, having already written
// the 429 response.
func (h *Handlers) enforceLimits(w http.ResponseWriter, r *http.Request, m reqMeta) bool {
	if h.Tracker == nil || h.LimitStore == nil {
		return true
	}
	l := h.limitsFor(r.Context(), m.orgID, m.keyID)
	d := h.Tracker.Allow(r.Context(), m.orgID, m.keyID, l)
	if d.Allowed {
		return true
	}

	// A key cut off by its budget is worth seeing in the incident feed, and
	// worth one alert — not one per rejected request.
	if d.Reason == limits.ReasonBudget && h.Tracker.MarkNotified(m.orgID, m.keyID) {
		h.recordLimitEvent(r.Context(), m, d)
	}

	h.Metrics.ObserveLimitDenied(string(d.Reason))
	w.Header().Set("Retry-After", strconv.Itoa(d.RetryAfter))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)

	message := "rate limit exceeded: too many requests for this key"
	if d.Reason == limits.ReasonBudget {
		message = "daily budget exhausted for this key; it resets at 00:00 UTC"
	}
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "aperture_limit_exceeded",
			"reason":  string(d.Reason),
		},
		"aperture": map[string]any{
			"spent_usd":   d.Spent,
			"budget_usd":  d.Limit,
			"retry_after": d.RetryAfter,
		},
	})
	return false
}

// recordLimitEvent puts a budget cut-off in the incident feed and through the
// alerter, reusing the DLP event pipeline so operators see it where they
// already look.
func (h *Handlers) recordLimitEvent(ctx context.Context, m reqMeta, d limits.Decision) {
	if h.DLPStore == nil {
		return
	}
	e := storage.DLPEvent{
		OrgID:        m.orgID,
		KeyID:        m.keyID,
		Model:        m.model,
		Provider:     m.provider,
		Rule:         "budget-exceeded",
		Group:        "limits",
		Action:       "blocked",
		MaskedSample: "spent $" + strconv.FormatFloat(d.Spent, 'f', 4, 64) + " of $" + strconv.FormatFloat(d.Limit, 'f', 2, 64),
		Agent:        m.agent,
		Session:      m.session,
	}
	if err := h.DLPStore.Insert(ctx, e); err != nil {
		h.Logger.Error("limit event insert failed", "err", err)
	}
	if h.Alerter != nil {
		h.Alerter.Notify(e)
	}
}

// ── Admin API ─────────────────────────────────────────────────────────────────

func (h *Handlers) requireLimitStore(w http.ResponseWriter, r *http.Request, min storage.Role) (string, bool) {
	orgID, ok := h.adminOrg(w, r, min)
	if !ok {
		return "", false
	}
	if h.LimitStore == nil {
		h.writePolicyError(w, "limits disabled", http.StatusServiceUnavailable)
		return "", false
	}
	return orgID, true
}

func validateLimits(l limits.Limits) error {
	if l.BudgetDailyUSD < 0 {
		return errNegative("budget_daily_usd")
	}
	if l.RequestsPerMinute < 0 {
		return errNegative("requests_per_minute")
	}
	return nil
}

type limitError string

func (e limitError) Error() string { return string(e) }

func errNegative(field string) error { return limitError(field + " must not be negative") }

// GET /admin/limits → {"default": {...}, "keys": {...}, "spent_usd": {...}}
func (h *Handlers) handleLimitsGet(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireLimitStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	def, err := h.LimitStore.GetDefaultLimits(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("get default limits failed", "err", err)
		h.writePolicyError(w, "failed to load limits", http.StatusInternalServerError)
		return
	}
	keys, err := h.LimitStore.ListLimits(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("list limits failed", "err", err)
		h.writePolicyError(w, "failed to load limits", http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = map[string]limits.Limits{}
	}

	// Today's spend, so the console can show usage next to the ceiling.
	spent := map[string]float64{}
	if h.Tracker != nil {
		for id := range keys {
			spent[id] = h.Tracker.Spent(orgID, id)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"default": def, "keys": keys, "spent_usd": spent})
}

// PUT /admin/limits/default
func (h *Handlers) handleLimitsPutDefault(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireLimitStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	var l limits.Limits
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		h.writePolicyError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validateLimits(l); err != nil {
		h.writePolicyError(w, err.Error(), http.StatusBadRequest)
		return
	}
	before, _ := h.LimitStore.GetDefaultLimits(r.Context(), orgID)
	if err := h.LimitStore.SetDefaultLimits(r.Context(), orgID, l); err != nil {
		h.Logger.Error("set default limits failed", "err", err)
		h.writePolicyError(w, "failed to save limits", http.StatusInternalServerError)
		return
	}
	h.auditChange(r, orgID, "limits.update", defaultTarget, limitsChange(before, l))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// PUT /admin/limits/keys/{id}
func (h *Handlers) handleLimitsPutKey(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireLimitStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" || id == "default" {
		h.writePolicyError(w, "invalid key id", http.StatusBadRequest)
		return
	}
	var l limits.Limits
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
		h.writePolicyError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validateLimits(l); err != nil {
		h.writePolicyError(w, err.Error(), http.StatusBadRequest)
		return
	}
	before := h.limitsFor(r.Context(), orgID, id)
	if err := h.LimitStore.SetLimits(r.Context(), orgID, id, l); err != nil {
		h.Logger.Error("set key limits failed", "err", err, "key_id", id)
		h.writePolicyError(w, "failed to save limits", http.StatusInternalServerError)
		return
	}
	meta := limitsChange(before, l)
	meta["key_id"] = id
	h.auditChange(r, orgID, "limits.update", h.keyName(r.Context(), orgID, id), meta)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// DELETE /admin/limits/keys/{id} — the key falls back to the default.
func (h *Handlers) handleLimitsDeleteKey(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireLimitStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "" || id == "default" {
		h.writePolicyError(w, "invalid key id", http.StatusBadRequest)
		return
	}
	before := h.limitsFor(r.Context(), orgID, id)
	if err := h.LimitStore.DeleteLimits(r.Context(), orgID, id); err != nil {
		h.Logger.Error("delete key limits failed", "err", err, "key_id", id)
		h.writePolicyError(w, "failed to delete limits", http.StatusInternalServerError)
		return
	}
	after, _ := h.LimitStore.GetDefaultLimits(r.Context(), orgID)
	meta := limitsChange(before, after)
	meta["key_id"] = id
	h.audit(r, orgID, "limits.reset", h.keyName(r.Context(), orgID, id), meta)
	w.WriteHeader(http.StatusNoContent)
}
