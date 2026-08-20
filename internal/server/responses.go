package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/provider/openai"
)

// responsesUsage is the token block the Responses API reports. It differs from
// chat completions, which uses prompt_tokens/completion_tokens.
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// handleResponses proxies the OpenAI Responses API (POST /v1/responses) with
// DLP scanning. Newer OpenAI-based agents — Codex CLI among them — speak this
// API rather than chat completions.
func (h *Handlers) handleResponses(w http.ResponseWriter, r *http.Request) {
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

	// DLP: scan outbound content before anything leaves the network.
	if h.Inspector != nil {
		res := h.Inspector.ScanResponsesRequest(bodyBytes, h.policyFor(r.Context(), key.ID))
		h.recordDLPEvents(r.Context(), meta, res.Findings)
		h.recordSuppressed(r.Context(), meta, res.Suppressed)
		if res.Verdict == inspector.ActionBlock {
			h.writeDLPBlocked(w, res.Findings)
			return
		}
		bodyBytes = res.Body
	}

	apiKey := key.Providers["openai"]
	if apiKey == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "no OpenAI API key configured for this aperture key. Add it in Settings.",
		})
		return
	}

	client := openai.New(h.OpenAIBaseURL, apiKey)
	start := time.Now()
	upstream, respCT, status, err := client.Responses(r.Context(),
		bytes.NewReader(bodyBytes), r.Header.Get("Content-Type"))
	if err != nil {
		h.recordUsage(meta, 0, 0, http.StatusBadGateway, time.Since(start), err.Error())
		http.Error(w, `{"error":"failed to proxy request"}`, http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	w.Header().Set("Content-Type", respCT)
	w.WriteHeader(status)

	if flusher, ok := w.(http.Flusher); ok && isStreaming(respCT) {
		in, out := h.streamResponses(w, flusher, upstream)
		h.recordUsage(meta, in, out, status, time.Since(start), "")
		return
	}

	data, readErr := io.ReadAll(upstream)
	var resp struct {
		Usage responsesUsage `json:"usage"`
	}
	_ = json.Unmarshal(data, &resp)
	w.Write(data)
	errStr := ""
	if readErr != nil {
		errStr = readErr.Error()
	}
	h.recordUsage(meta, resp.Usage.InputTokens, resp.Usage.OutputTokens,
		status, time.Since(start), errStr)
}

// streamResponses relays the SSE stream and reads usage off the terminal
// response.completed event, which carries the finished response object.
func (h *Handlers) streamResponses(w io.Writer, flusher http.Flusher, upstream io.Reader) (inTok, outTok int) {
	streamSSE(w, flusher, upstream, func(data []byte) {
		var evt struct {
			Type     string `json:"type"`
			Response *struct {
				Usage *responsesUsage `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal(data, &evt) != nil {
			return
		}
		if evt.Response != nil && evt.Response.Usage != nil {
			inTok = evt.Response.Usage.InputTokens
			outTok = evt.Response.Usage.OutputTokens
		}
	})
	return inTok, outTok
}
