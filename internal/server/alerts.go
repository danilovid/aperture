package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danilovid/aperture/internal/alerter"
)

// GET /admin/alerts — current webhook config (URL masked).
func (h *Handlers) handleAlertsGet(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.Alerter == nil {
		http.Error(w, `{"error":"alerts disabled"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.Alerter.Config())
}

// PUT /admin/alerts — replace the webhook config.
func (h *Handlers) handleAlertsPut(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
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
	h.Alerter.SetConfig(cfg)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// POST /admin/alerts/test — send a synthetic alert to verify the destination.
func (h *Handlers) handleAlertsTest(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.Alerter == nil {
		http.Error(w, `{"error":"alerts disabled"}`, http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := h.Alerter.SendTest(ctx); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
