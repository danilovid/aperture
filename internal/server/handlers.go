package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/metrics"
	"github.com/danilovid/aperture/internal/storage"
)

// Handlers holds dependencies for API handlers.
type Handlers struct {
	KeyStore         storage.KeyStore
	LogStore         storage.LogStore
	DLPStore         storage.DLPStore
	PolicyStore      storage.PolicyStore
	LimitStore       storage.LimitStore
	Tracker          *limits.Tracker
	Metrics          *metrics.Registry
	Inspector        *inspector.Inspector
	DLPPolicy        inspector.Policy // fallback when PolicyStore is nil
	Alerter          *alerter.Alerter
	CustomProviders  []config.CustomProvider
	OpenAIBaseURL    string
	AnthropicBaseURL string
	AdminAPIKey      string
	ReadyCheck       func(ctx context.Context) error
	Logger           *slog.Logger
}

// policyFor resolves the effective DLP policy for a key: per-key binding,
// then the stored default, then the env-configured fallback.
func (h *Handlers) policyFor(ctx context.Context, keyID string) inspector.Policy {
	if h.PolicyStore == nil {
		return h.DLPPolicy
	}
	if p, ok, err := h.PolicyStore.GetPolicy(ctx, keyID); err == nil && ok {
		return p
	} else if err != nil {
		h.Logger.Error("policy lookup failed, using default", "err", err, "key_id", keyID)
	}
	p, err := h.PolicyStore.GetDefaultPolicy(ctx)
	if err != nil {
		h.Logger.Error("default policy lookup failed, using env fallback", "err", err)
		return h.DLPPolicy
	}
	return p
}

// inspect returns the request-scoped inspector, so the NER model call inherits
// the request's deadline and cancellation.
func (h *Handlers) inspect(ctx context.Context) *inspector.Inspector {
	return h.Inspector.WithContext(ctx)
}

// noteNER surfaces a model-stage failure. The scan still happened with the
// regex detectors — or the traffic was refused, when the gateway fails closed.
func (h *Handlers) noteNER(err error) {
	if err == nil {
		return
	}
	h.Logger.Warn("ner model stage failed, regex detectors still applied", "err", err)
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

// reqMeta identifies one request for logging: which key, which model, and —
// when the caller supplies the headers — which agent and session. Several
// agents usually share a key, so the key alone cannot answer "whose run".
type reqMeta struct {
	keyID   string
	model   string
	agent   string
	session string
}

// maxAttrLen bounds attribution values so a caller cannot push arbitrary
// amounts of header data into the logs.
const maxAttrLen = 128

func attrValue(r *http.Request, header string) string {
	v := strings.TrimSpace(r.Header.Get(header))
	if len(v) > maxAttrLen {
		v = v[:maxAttrLen]
	}
	return v
}

// metaFor reads the optional X-Aperture-Agent / X-Aperture-Session headers.
func metaFor(r *http.Request, keyID, model string) reqMeta {
	return reqMeta{
		keyID:   keyID,
		model:   model,
		agent:   attrValue(r, "X-Aperture-Agent"),
		session: attrValue(r, "X-Aperture-Session"),
	}
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
	if h.ReadyCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := h.ReadyCheck(ctx); err != nil {
			h.Logger.Error("readiness check failed", "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

// handleMetrics serves the Prometheus exposition. It carries no keys, samples
// or prompt content — only counters — so it is not behind the admin token;
// block it at your proxy if you do not want it reachable.
func (h *Handlers) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if h.Metrics == nil {
		http.Error(w, "metrics disabled", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	h.Metrics.Render(w)
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
		if p, ok := h.resolveProviderForKey(key, reqMeta{keyID: key.ID, model: candidate}); ok {
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

	meta := metaFor(r, key.ID, model)
	if !h.enforceLimits(w, r, meta) {
		return
	}

	// DLP: scan outbound content before anything leaves the network.
	policy := h.policyFor(r.Context(), key.ID)
	if h.Inspector != nil {
		res := h.inspect(r.Context()).ScanChatRequest(bodyBytes, policy)
		h.noteNER(res.NERError)
		h.recordDLPEvents(r.Context(), meta, res.Findings)
		h.recordSuppressed(r.Context(), meta, res.Suppressed)
		if res.Verdict == inspector.ActionBlock {
			h.writeDLPBlocked(w, res.Findings)
			return
		}
		bodyBytes = res.Body
	}

	p, ok := h.resolveProviderForKey(key, meta)
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

	rs := h.newRespScanner(meta, policy)

	if flusher, ok := w.(http.Flusher); ok && isStreaming(respCT) {
		w.Header().Set("Content-Type", respCT)
		w.WriteHeader(status)
		if rs != nil {
			streamSSEFiltered(w, flusher, body, nil, rs.chatFilter())
			// A blocked stream cancels the request context on the way out;
			// the incident must still reach the feed.
			rs.record(context.WithoutCancel(r.Context()))
			return
		}
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
		return
	}

	if rs != nil {
		data, err := io.ReadAll(body)
		if err != nil {
			http.Error(w, `{"error":"failed to read upstream response"}`, http.StatusBadGateway)
			return
		}
		res := h.inspect(r.Context()).ScanChatResponse(data, policy)
		h.noteNER(res.NERError)
		h.recordFindings(r.Context(), meta, res.Findings, "", storage.DirectionResponse)
		h.recordFindings(r.Context(), meta, res.Suppressed, "suppressed", storage.DirectionResponse)
		if res.Verdict == inspector.ActionBlock {
			h.writeDLPBlockedResponse(w, res.Findings)
			return
		}
		w.Header().Set("Content-Type", respCT)
		w.WriteHeader(status)
		w.Write(res.Body)
		return
	}

	w.Header().Set("Content-Type", respCT)
	w.WriteHeader(status)
	io.Copy(w, body)
}

// observeUsage feeds a completed request into the budget counters and the
// metrics registry. Both proxy paths funnel through here.
func (h *Handlers) observeUsage(e storage.LogEntry) {
	if h.Tracker != nil {
		h.Tracker.AddSpend(e.KeyID, e.CostUSD)
	}
	h.Metrics.ObserveLLM(e.Provider, e.Model, e.StatusCode, e.PromptTokens, e.CompletionTokens, e.CostUSD)
}

// ── DLP ───────────────────────────────────────────────────────────────────────

func dlpEventAction(a inspector.Action) string {
	switch a {
	case inspector.ActionBlock:
		return "blocked"
	case inspector.ActionRedact:
		return "redacted"
	default:
		return "alerted"
	}
}

func (h *Handlers) recordDLPEvents(ctx context.Context, m reqMeta, findings []inspector.Finding) {
	h.recordFindings(ctx, m, findings, "", storage.DirectionRequest)
}

// recordSuppressed logs matches a mute or allowlist entry held back. They are
// stored as action "suppressed" so silencing a detector shows up in the feed
// and the summary instead of vanishing.
func (h *Handlers) recordSuppressed(ctx context.Context, m reqMeta, findings []inspector.Finding) {
	h.recordFindings(ctx, m, findings, "suppressed", storage.DirectionRequest)
}

// recordFindings writes one DLP event per finding. actionOverride, when set,
// replaces the action derived from the finding; direction says whether the
// match was in what the agent sent or in what the model sent back.
func (h *Handlers) recordFindings(ctx context.Context, m reqMeta, findings []inspector.Finding, actionOverride, direction string) {
	if h.DLPStore == nil || len(findings) == 0 {
		return
	}
	llm := h.resolveLLM(m.model)
	for _, f := range findings {
		action := actionOverride
		if action == "" {
			action = dlpEventAction(f.Action)
		}
		e := storage.DLPEvent{
			KeyID:        m.keyID,
			Model:        m.model,
			Provider:     llm,
			Rule:         f.Rule,
			Group:        string(f.Group),
			Action:       action,
			MaskedSample: f.MaskedSample,
			Agent:        m.agent,
			Session:      m.session,
			Direction:    direction,
		}
		h.Metrics.ObserveDLP(e.Rule, e.Action)
		if err := h.DLPStore.Insert(ctx, e); err != nil {
			h.Logger.Error("dlp event insert failed", "err", err)
		}
		if h.Alerter != nil {
			h.Alerter.Notify(e)
		}
	}
}

// writeDLPBlockedResponse rejects a response that carried sensitive data. The
// upstream answer is discarded; the caller still paid for it, so the usage log
// records the request as normal.
func (h *Handlers) writeDLPBlockedResponse(w http.ResponseWriter, findings []inspector.Finding) {
	rules := blockedRules(findings)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "response blocked by DLP policy: sensitive data detected (" +
				strings.Join(rules, ", ") + ")",
			"type":  "aperture_dlp_blocked",
			"rules": rules,
		},
		"aperture": map[string]any{"blocked_by": "dlp", "direction": "response"},
	})
}

func (h *Handlers) writeDLPBlocked(w http.ResponseWriter, findings []inspector.Finding) {
	rules := make([]string, 0, len(findings))
	seen := map[string]bool{}
	for _, f := range findings {
		if f.Action == inspector.ActionBlock && !seen[f.Rule] {
			seen[f.Rule] = true
			rules = append(rules, f.Rule)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "request blocked by DLP policy: sensitive data detected (" + strings.Join(rules, ", ") + ")",
			"type":    "aperture_dlp_blocked",
			"rules":   rules,
		},
	})
}

func (h *Handlers) handleDLPEvents(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.DLPStore == nil {
		http.Error(w, `{"error":"dlp disabled"}`, http.StatusServiceUnavailable)
		return
	}
	f := storage.DLPFilter{
		Action:    r.URL.Query().Get("action"),
		Rule:      r.URL.Query().Get("rule"),
		KeyID:     r.URL.Query().Get("key_id"),
		Agent:     r.URL.Query().Get("agent"),
		Session:   r.URL.Query().Get("session"),
		Direction: r.URL.Query().Get("direction"),
		Limit:     50,
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			f.Limit = n
		}
	}
	if r.URL.Query().Get("period") != "" {
		f.Since = sinceParam(r)
	}
	events, err := h.DLPStore.List(r.Context(), f)
	if err != nil {
		h.Logger.Error("dlp events list failed", "err", err)
		http.Error(w, `{"error":"failed to query events"}`, http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []storage.DLPEvent{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"events": events})
}

func (h *Handlers) handleDLPSummary(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if h.DLPStore == nil {
		http.Error(w, `{"error":"dlp disabled"}`, http.StatusServiceUnavailable)
		return
	}
	sum, err := h.DLPStore.Summary(r.Context(), sinceParam(r))
	if err != nil {
		h.Logger.Error("dlp summary failed", "err", err)
		http.Error(w, `{"error":"failed to query summary"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sum)
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

func (h *Handlers) handleAdminCreateKey(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req struct {
		ApertureKey     string `json:"aperture_key"`
		Name            string `json:"name"`
		OpenAIAPIKey    string `json:"openai_api_key"`
		AnthropicAPIKey string `json:"anthropic_api_key"`
		GroqAPIKey      string `json:"groq_api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.ApertureKey == "" {
		req.ApertureKey = config.GenerateKey("ap")
	}
	if req.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}

	key, err := h.KeyStore.Create(r.Context(), req.ApertureKey, req.Name, map[string]string{
		"openai":    req.OpenAIAPIKey,
		"anthropic": req.AnthropicAPIKey,
		"groq":      req.GroqAPIKey,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotSupported) {
			http.Error(w, `{"error":"key management requires PostgreSQL (set DATABASE_URL)"}`, http.StatusNotImplemented)
			return
		}
		h.Logger.Error("create key failed", "err", err)
		http.Error(w, `{"error":"failed to create key"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	// The full aperture_key is returned once, on creation.
	json.NewEncoder(w).Encode(key)
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
