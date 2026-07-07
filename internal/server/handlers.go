package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/storage"
)

// Handlers holds dependencies for API handlers.
type Handlers struct {
	KeyStore      storage.KeyStore
	LogStore      storage.LogStore
	OpenAIBaseURL string
	AdminAPIKey   string
	Logger        *slog.Logger
}

// requireAdmin returns false and writes 401 unless the request presents the
// admin key as a Bearer token. An empty AdminAPIKey denies everything
// (fail closed) — main generates a key when the env var is unset.
func (h *Handlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	token := extractBearerToken(r)
	if h.AdminAPIKey == "" || token == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(h.AdminAPIKey)) != 1 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[len("Bearer "):])
}

func (h *Handlers) resolveKey(r *http.Request) (*storage.Key, error) {
	token := extractBearerToken(r)
	if token == "" {
		return nil, storage.ErrKeyNotFound
	}
	return h.KeyStore.GetByApertureKey(r.Context(), token)
}

// ── Health ────────────────────────────────────────────────────────────────────

func (h *Handlers) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (h *Handlers) handleReady(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

// ── OpenAI-compatible API ─────────────────────────────────────────────────────

type chatRequestModel struct {
	Model string `json:"model"`
}

func (h *Handlers) handleModels(w http.ResponseWriter, r *http.Request) {
	key, err := h.resolveKey(r)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}
	// Use first available provider for models list.
	for _, candidate := range []string{"gpt-4o-mini", "claude-3-5-sonnet-20241022", "llama-3.3-70b-versatile"} {
		if p, ok := h.resolveProviderForKey(key, candidate); ok {
			body, ct, status, err := p.Models(r.Context())
			if err != nil {
				http.Error(w, `{"error":"failed to fetch models"}`, http.StatusBadGateway)
				return
			}
			defer body.Close()
			w.Header().Set("Content-Type", ct)
			w.WriteHeader(status)
			io.Copy(w, body)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": "no API key configured for any provider. Add a key in Settings."})
}

func (h *Handlers) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	key, err := h.resolveKey(r)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}

	var peek chatRequestModel
	_ = json.Unmarshal(bodyBytes, &peek)
	model := peek.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	p, ok := h.resolveProviderForKey(key, model)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "no API key configured for this model. Add the key in Settings.",
		})
		return
	}

	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}

	body, respCT, status, err := p.ChatCompletions(r.Context(), bytes.NewReader(bodyBytes), ct)
	if err != nil {
		http.Error(w, `{"error":"failed to proxy request"}`, http.StatusBadGateway)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", respCT)
	w.WriteHeader(status)

	if flusher, ok := w.(http.Flusher); ok && isStreaming(respCT) {
		buf := make([]byte, 32*1024)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				flusher.Flush()
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return
			}
		}
	} else {
		io.Copy(w, body)
	}
}

func (h *Handlers) writeAuthError(w http.ResponseWriter, err error) {
	if err == storage.ErrKeyNotFound {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid or missing API key"})
		return
	}
	h.Logger.Error("key lookup failed", "err", err)
	http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
}

func isStreaming(ct string) bool {
	return strings.HasPrefix(ct, "text/event-stream")
}

// ── Admin: provider key config ────────────────────────────────────────────────

func (h *Handlers) handleAdminGetConfig(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	providers, err := h.KeyStore.GetProviderKeys(r.Context())
	if err != nil {
		h.Logger.Error("get provider keys failed", "err", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	configured := len(providers) > 0
	configuredList := make([]string, 0, len(providers))
	for llm := range providers {
		configuredList = append(configuredList, llm)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"configured":           configured,
		"configured_providers": configuredList,
	})
}

func (h *Handlers) handleAdminSetConfig(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req struct {
		OpenAIAPIKey    string `json:"openai_api_key"`
		AnthropicAPIKey string `json:"anthropic_api_key"`
		GroqAPIKey      string `json:"groq_api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	providers := map[string]string{
		"openai":    req.OpenAIAPIKey,
		"anthropic": req.AnthropicAPIKey,
		"groq":      req.GroqAPIKey,
	}
	if err := h.KeyStore.SetProviderKeys(r.Context(), providers); err != nil {
		h.Logger.Error("set provider keys failed", "err", err)
		http.Error(w, `{"error":"failed to save keys"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *Handlers) handleAdminDeleteConfig(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := h.KeyStore.ClearProviderKeys(r.Context()); err != nil {
		h.Logger.Error("clear provider keys failed", "err", err)
		http.Error(w, `{"error":"failed to clear keys"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// ── Admin: aperture key management ───────────────────────────────────────────

func (h *Handlers) handleAdminListKeys(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	keys, err := h.KeyStore.List(r.Context())
	if err != nil {
		h.Logger.Error("list keys failed", "err", err)
		http.Error(w, `{"error":"failed to list keys"}`, http.StatusInternalServerError)
		return
	}
	if keys == nil {
		keys = []storage.Key{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

func (h *Handlers) handleAdminDeleteKey(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"key id required"}`, http.StatusBadRequest)
		return
	}
	if err := h.KeyStore.Delete(r.Context(), id); err != nil {
		if err == storage.ErrKeyNotFound {
			http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
			return
		}
		h.Logger.Error("delete key failed", "err", err)
		http.Error(w, `{"error":"failed to delete key"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Stats ─────────────────────────────────────────────────────────────────────

func (h *Handlers) handleStatsLogs(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.LogStore == nil {
		h.writeNoLogStore(w)
		return
	}
	limit, offset := 50, 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	entries, err := h.LogStore.List(r.Context(), storage.LogFilter{Limit: limit, Offset: offset})
	if err != nil {
		h.Logger.Error("list logs failed", "err", err)
		http.Error(w, `{"error":"failed to query logs"}`, http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []storage.LogEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"logs": entries})
}

func (h *Handlers) handleStatsSummary(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.LogStore == nil {
		h.writeNoLogStore(w)
		return
	}
	sum, err := h.LogStore.Summary(r.Context(), sinceParam(r))
	if err != nil {
		h.Logger.Error("summary failed", "err", err)
		http.Error(w, `{"error":"failed to query summary"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sum)
}

func (h *Handlers) handleStatsTimeseries(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.LogStore == nil {
		h.writeNoLogStore(w)
		return
	}
	bucketHours := 1
	if v := r.URL.Query().Get("bucket_hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			bucketHours = n
		}
	}
	buckets, err := h.LogStore.Timeseries(r.Context(), sinceParam(r), bucketHours)
	if err != nil {
		h.Logger.Error("timeseries failed", "err", err)
		http.Error(w, `{"error":"failed to query timeseries"}`, http.StatusInternalServerError)
		return
	}
	if buckets == nil {
		buckets = []storage.TimeseriesBucket{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"buckets": buckets})
}

func (h *Handlers) handleStatsModels(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.LogStore == nil {
		h.writeNoLogStore(w)
		return
	}
	stats, err := h.LogStore.ModelStats(r.Context(), sinceParam(r))
	if err != nil {
		h.Logger.Error("model stats failed", "err", err)
		http.Error(w, `{"error":"failed to query model stats"}`, http.StatusInternalServerError)
		return
	}
	if stats == nil {
		stats = []storage.ModelStat{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"models": stats})
}

func (h *Handlers) writeNoLogStore(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]string{"error": "stats unavailable: DATABASE_URL not set"})
}

func sinceParam(r *http.Request) time.Time {
	switch r.URL.Query().Get("period") {
	case "7d":
		return time.Now().Add(-7 * 24 * time.Hour)
	case "30d":
		return time.Now().Add(-30 * 24 * time.Hour)
	default:
		return time.Now().Add(-24 * time.Hour)
	}
}
