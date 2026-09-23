package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danilovid/mutegate/internal/alerter"
	"github.com/danilovid/mutegate/internal/storage"
)

// Alerts belong to the organization: its incidents, its webhook. An admin of
// the organization sets them; the operator can too, naming the organization.

// GET /admin/alerts — the organization's webhook config (URL masked).
func (h *Handlers) handleAlertsGet(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.adminOrg(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	if h.Alerter == nil {
		http.Error(w, `{"error":"alerts disabled"}`, http.StatusServiceUnavailable)
		return
	}
	cfg, err := h.Alerter.ConfigFor(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("load alert settings failed", "err", err)
		http.Error(w, `{"error":"could not load alert settings"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// PUT /admin/alerts — replace the organization's webhook config.
func (h *Handlers) handleAlertsPut(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.adminOrg(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	if h.Alerter == nil {
		http.Error(w, `{"error":"alerts disabled"}`, http.StatusServiceUnavailable)
		return
	}
	var cfg alerter.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if cfg.Format == "" {
		cfg.Format = alerter.FormatJSON
	}
	if !alerter.ValidFormat(string(cfg.Format)) {
		http.Error(w, `{"error":"invalid format (want json|slack|telegram)"}`, http.StatusBadRequest)
		return
	}
	if cfg.Format == alerter.FormatTelegram && cfg.ChatID == "" {
		http.Error(w, `{"error":"chat_id is required for the telegram format"}`, http.StatusBadRequest)
		return
	}
	before, _ := h.Alerter.ConfigFor(r.Context(), orgID)
	if err := h.Alerter.SetConfigFor(r.Context(), orgID, cfg); err != nil {
		h.Logger.Error("save alert settings failed", "err", err)
		http.Error(w, `{"error":"could not save alert settings"}`, http.StatusInternalServerError)
		return
	}
	after, _ := h.Alerter.ConfigFor(r.Context(), orgID)
	h.auditChange(r, orgID, "alerts.update", "", alertsChange(before, after, cfg.URL != before.URL))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// POST /admin/alerts/test — send a synthetic alert to verify the destination.
func (h *Handlers) handleAlertsTest(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.adminOrg(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	if h.Alerter == nil {
		http.Error(w, `{"error":"alerts disabled"}`, http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := h.Alerter.SendTestFor(ctx, orgID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
