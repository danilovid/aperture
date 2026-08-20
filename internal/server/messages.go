package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/pricing"
	"github.com/danilovid/aperture/internal/provider/anthropic"
	"github.com/danilovid/aperture/internal/storage"
)

// extractClientToken returns the caller's aperture key. Anthropic clients
// (including Claude Code) authenticate with x-api-key rather than a Bearer
// header, so both are accepted.
func extractClientToken(r *http.Request) string {
	if t := extractBearerToken(r); t != "" {
		return t
	}
	return strings.TrimSpace(r.Header.Get("x-api-key"))
}

// writeAnthropicError emits an Anthropic-shaped error so SDKs surface it the
// way they surface upstream errors.
func writeAnthropicError(w http.ResponseWriter, status int, errType, message string, extra map[string]any) {
	payload := map[string]any{
		"type":  "error",
		"error": map[string]any{"type": errType, "message": message},
	}
	for k, v := range extra {
		payload[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

// handleMessages proxies the native Anthropic Messages API (POST /v1/messages)
// with DLP scanning, so agents that speak Anthropic natively — Claude Code
// among them — can be protected by pointing ANTHROPIC_BASE_URL at Aperture.
func (h *Handlers) handleMessages(w http.ResponseWriter, r *http.Request) {
	token := extractClientToken(r)
	if token == "" {
		writeAnthropicError(w, http.StatusUnauthorized, "authentication_error",
			"missing API key: send it as 'x-api-key' or 'Authorization: Bearer'", nil)
		return
	}
	key, err := h.KeyStore.GetByApertureKey(r.Context(), token)
	if err != nil {
		if err == storage.ErrKeyNotFound {
			writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "invalid API key", nil)
			return
		}
		h.Logger.Error("key lookup failed", "err", err)
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "internal error", nil)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "failed to read body", nil)
		return
	}

	var peek struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(bodyBytes, &peek)
	model := peek.Model
	if model == "" {
		model = "claude"
	}

	// DLP: scan outbound content before anything leaves the network.
	if h.Inspector != nil {
		res := h.Inspector.ScanMessagesRequest(bodyBytes, h.policyFor(r.Context(), key.ID))
		h.recordDLPEvents(r.Context(), key.ID, model, res.Findings)
		h.recordSuppressed(r.Context(), key.ID, model, res.Suppressed)
		if res.Verdict == inspector.ActionBlock {
			rules := blockedRules(res.Findings)
			writeAnthropicError(w, http.StatusForbidden, "permission_error",
				"request blocked by DLP policy: sensitive data detected ("+strings.Join(rules, ", ")+")",
				map[string]any{"aperture": map[string]any{"blocked_by": "dlp", "rules": rules}})
			return
		}
		bodyBytes = res.Body
	}

	apiKey := key.Providers["anthropic"]
	if apiKey == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
			"no Anthropic API key configured for this aperture key. Add it in Settings.", nil)
		return
	}

	client := anthropic.New(h.AnthropicBaseURL, apiKey)
	start := time.Now()
	upstream, respCT, status, err := client.Messages(r.Context(), bytes.NewReader(bodyBytes),
		r.Header.Get("Content-Type"), anthropic.PassthroughHeaders{
			Version: r.Header.Get("anthropic-version"),
			Beta:    r.Header.Get("anthropic-beta"),
		})
	if err != nil {
		h.recordMessagesUsage(model, key.ID, 0, 0, http.StatusBadGateway, time.Since(start), err.Error())
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "failed to proxy request", nil)
		return
	}
	defer upstream.Close()

	w.Header().Set("Content-Type", respCT)
	w.WriteHeader(status)

	if flusher, ok := w.(http.Flusher); ok && isStreaming(respCT) {
		in, out := h.streamMessages(w, flusher, upstream)
		h.recordMessagesUsage(model, key.ID, in, out, status, time.Since(start), "")
		return
	}

	data, readErr := io.ReadAll(upstream)
	in, out := anthropicUsageFromJSON(data)
	w.Write(data)
	errStr := ""
	if readErr != nil {
		errStr = readErr.Error()
	}
	h.recordMessagesUsage(model, key.ID, in, out, status, time.Since(start), errStr)
}

// blockedRules lists the distinct rules whose verdict was block.
func blockedRules(findings []inspector.Finding) []string {
	rules := make([]string, 0, len(findings))
	seen := map[string]bool{}
	for _, f := range findings {
		if f.Action == inspector.ActionBlock && !seen[f.Rule] {
			seen[f.Rule] = true
			rules = append(rules, f.Rule)
		}
	}
	return rules
}

// streamMessages copies an SSE stream to the client while pulling token usage
// out of the Anthropic event flow (message_start carries input tokens,
// message_delta the running output count).
func (h *Handlers) streamMessages(w io.Writer, flusher http.Flusher, upstream io.Reader) (inTok, outTok int) {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return inTok, outTok
		}
		flusher.Flush()

		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" {
			continue
		}
		var evt struct {
			Type    string `json:"type"`
			Message *struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &evt) != nil {
			continue
		}
		switch evt.Type {
		case "message_start":
			if evt.Message != nil && evt.Message.Usage != nil {
				inTok = evt.Message.Usage.InputTokens
				outTok = evt.Message.Usage.OutputTokens
			}
		case "message_delta":
			if evt.Usage != nil {
				if evt.Usage.InputTokens > 0 {
					inTok = evt.Usage.InputTokens
				}
				outTok = evt.Usage.OutputTokens
			}
		}
	}
	return inTok, outTok
}

// anthropicUsageFromJSON reads usage off a non-streaming Messages response.
func anthropicUsageFromJSON(body []byte) (inTok, outTok int) {
	var resp struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(body, &resp)
	return resp.Usage.InputTokens, resp.Usage.OutputTokens
}

// recordMessagesUsage writes one request log row. The native path bypasses the
// interceptor (which speaks the OpenAI usage shape), so metering happens here.
func (h *Handlers) recordMessagesUsage(model, keyID string, in, out, status int, latency time.Duration, errStr string) {
	if h.LogStore == nil {
		return
	}
	entry := storage.LogEntry{
		Model:            model,
		Provider:         "anthropic",
		PromptTokens:     in,
		CompletionTokens: out,
		TotalTokens:      in + out,
		CostUSD:          pricing.Calculate(model, in, out),
		LatencyMs:        latency.Milliseconds(),
		StatusCode:       status,
		KeyID:            keyID,
		Error:            errStr,
	}
	// A fresh context: the request context may already be cancelled when a
	// stream finishes, which would silently drop the row.
	if err := h.LogStore.Insert(context.Background(), entry); err != nil {
		h.Logger.Error("usage log insert failed", "err", err)
	}
}
