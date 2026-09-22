package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/provider/jev"
	"github.com/danilovid/aperture/internal/storage"
)

// jevProvider is the name this destination carries in the incident feed, the
// request log and the metrics.
const jevProvider = "jev"

// writeJevError answers in Jev's own envelope — {code, message, data} with a
// non-zero code — so a client that only knows how to read Jev errors still
// understands why the gateway stopped it. The aperture object is extra detail
// for anyone who looks.
func writeJevError(w http.ResponseWriter, status int, message string, extra map[string]any) {
	payload := map[string]any{"code": -1, "message": message, "data": nil}
	for k, v := range extra {
		payload[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

// handleJevDecision proxies the Jev decision API with DLP scanning.
//
// Jev takes business fields rather than a prompt — the customer message, the
// tool arguments, the policy text — so the whole body is scanned, wherever the
// strings sit. Paths mirror the upstream ones, so an agent switches to the
// gateway by changing its base URL and nothing else.
func (h *Handlers) handleJevDecision(w http.ResponseWriter, r *http.Request) {
	path, ok := jev.Path(r.PathValue("preset"))
	if !ok {
		writeJevError(w, http.StatusNotFound,
			"unknown Jev endpoint: use /api/v1/decisions or one of the documented presets", nil)
		return
	}

	key, err := h.resolveKey(r)
	if err != nil {
		if err == storage.ErrKeyNotFound {
			writeJevError(w, http.StatusUnauthorized, "invalid API key", nil)
			return
		}
		h.Logger.Error("key lookup failed", "err", err)
		writeJevError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, jev.MaxBodyBytes+1))
	if err != nil {
		writeJevError(w, http.StatusBadRequest, "failed to read body", nil)
		return
	}
	if len(bodyBytes) > jev.MaxBodyBytes {
		// Upstream caps the body at 32 KiB; refuse here rather than spend a
		// round trip to be told.
		writeJevError(w, http.StatusRequestEntityTooLarge, "body exceeds the 32 KiB Jev limit", nil)
		return
	}

	// The native endpoint takes an optional Jev model identifier; the presets
	// take none. Either way the destination is what gets metered.
	var peek struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(bodyBytes, &peek)
	model := peek.Model
	if model == "" {
		model = jevProvider
	}

	meta := metaFor(r, key.ID, model)
	meta.provider = jevProvider
	if !h.enforceLimits(w, r, meta) {
		return
	}

	policy := h.policyFor(r.Context(), key.ID)
	if h.Inspector != nil {
		res := h.inspect(r.Context()).ScanJevRequest(bodyBytes, policy)
		h.noteNER(res.NERError)
		h.recordDLPEvents(r.Context(), meta, res.Findings)
		h.recordSuppressed(r.Context(), meta, res.Suppressed)
		if res.Verdict == inspector.ActionBlock {
			rules := blockedRules(res.Findings)
			writeJevError(w, http.StatusForbidden,
				"request blocked by DLP policy: sensitive data detected ("+strings.Join(rules, ", ")+")",
				map[string]any{"aperture": map[string]any{"blocked_by": "dlp", "rules": rules}})
			return
		}
		bodyBytes = res.Body
	}

	apiKey := key.Providers[jevProvider]
	if apiKey == "" {
		writeJevError(w, http.StatusBadRequest,
			"no Jev API key configured for this aperture key. Add it in Settings or set JEV_API_KEY.", nil)
		return
	}

	client := jev.New(h.JevBaseURL, apiKey)
	start := time.Now()
	upstream, respCT, status, err := client.Decide(r.Context(), path,
		bytes.NewReader(bodyBytes), r.Header.Get("Content-Type"))
	if err != nil {
		h.recordUsage(meta, 0, 0, http.StatusBadGateway, time.Since(start), err.Error())
		writeJevError(w, http.StatusBadGateway, "failed to reach the Jev API", nil)
		return
	}
	defer upstream.Close()

	data, readErr := io.ReadAll(upstream)
	errStr := ""
	if readErr != nil {
		errStr = readErr.Error()
	}

	// Jev answers with one envelope, so response scanning needs no window:
	// guidance and answers can quote the input straight back.
	if h.Inspector != nil && policy.ScanResponses {
		res := h.inspect(r.Context()).ScanJevResponse(data, policy)
		h.noteNER(res.NERError)
		h.recordFindings(r.Context(), meta, res.Findings, "", storage.DirectionResponse)
		h.recordFindings(r.Context(), meta, res.Suppressed, "suppressed", storage.DirectionResponse)
		if res.Verdict == inspector.ActionBlock {
			rules := blockedRules(res.Findings)
			writeJevError(w, http.StatusForbidden,
				"response blocked by DLP policy: sensitive data detected ("+strings.Join(rules, ", ")+")",
				map[string]any{"aperture": map[string]any{
					"blocked_by": "dlp", "direction": "response", "rules": rules}})
			h.recordUsage(meta, 0, 0, http.StatusForbidden, time.Since(start), errStr)
			return
		}
		data = res.Body
	}

	w.Header().Set("Content-Type", respCT)
	w.WriteHeader(status)
	w.Write(data)

	// Jev reports no token usage, so the row records the call and its latency;
	// cost stays zero and rate limits still apply.
	h.recordUsage(meta, 0, 0, status, time.Since(start), errStr)
}
