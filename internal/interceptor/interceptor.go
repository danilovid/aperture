// Package interceptor wraps a Provider to extract token usage and log each request.
package interceptor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/pricing"
	"github.com/danilovid/aperture/internal/provider"
	"github.com/danilovid/aperture/internal/storage"
)

// Provider wraps another provider.Provider and records each request to LogStore.
type Provider struct {
	inner    provider.Provider
	logStore storage.LogStore
	model    string
	prov     string
	keyID    string
}

// New wraps inner with logging. model/prov/keyID are used for the log entry.
func New(inner provider.Provider, ls storage.LogStore, model, prov, keyID string) *Provider {
	return &Provider{inner: inner, logStore: ls, model: model, prov: prov, keyID: keyID}
}

func (p *Provider) Models(ctx context.Context) (io.ReadCloser, string, int, error) {
	return p.inner.Models(ctx)
}

func (p *Provider) ChatCompletions(ctx context.Context, body io.Reader, contentType string) (io.ReadCloser, string, int, error) {
	// Read body so we can inspect and modify it.
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, "", 0, err
	}

	// Inject stream_options.include_usage=true for streaming requests
	// so OpenAI/Groq return token usage in the final SSE chunk.
	var req map[string]any
	if json.Unmarshal(bodyBytes, &req) == nil {
		if streaming, _ := req["stream"].(bool); streaming {
			req["stream_options"] = map[string]any{"include_usage": true}
			if modified, err := json.Marshal(req); err == nil {
				bodyBytes = modified
			}
		}
	}

	start := time.Now()
	rc, ct, status, err := p.inner.ChatCompletions(ctx, bytes.NewReader(bodyBytes), contentType)

	if err != nil {
		p.record(ctx, 0, 0, status, time.Since(start).Milliseconds(), err.Error())
		return rc, ct, status, err
	}

	if strings.HasPrefix(ct, "text/event-stream") {
		// Pass start time — latency is measured when the stream fully completes.
		rc = p.wrapStream(ctx, rc, status, start)
	} else {
		rc = p.wrapJSON(ctx, rc, status, time.Since(start).Milliseconds())
	}
	return rc, ct, status, nil
}

// usageFields is the OpenAI-compatible usage object.
type usageFields struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// wrapJSON reads the full non-streaming response, extracts usage, then re-wraps.
func (p *Provider) wrapJSON(ctx context.Context, rc io.ReadCloser, status int, latency int64) io.ReadCloser {
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		p.record(ctx, 0, 0, status, latency, err.Error())
		return io.NopCloser(bytes.NewReader(data))
	}

	var resp struct {
		Usage usageFields `json:"usage"`
	}
	_ = json.Unmarshal(data, &resp)
	p.record(ctx, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, status, latency, "")

	return io.NopCloser(bytes.NewReader(data))
}

// wrapStream passes SSE lines through while watching for a usage chunk at the end.
func (p *Provider) wrapStream(ctx context.Context, rc io.ReadCloser, status int, start time.Time) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer rc.Close()

		var usage usageFields
		scanner := bufio.NewScanner(rc)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)

		for scanner.Scan() {
			line := scanner.Text()
			pw.Write([]byte(line + "\n"))

			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" || data == "" {
				continue
			}
			var chunk struct {
				Usage *usageFields `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &chunk) == nil && chunk.Usage != nil {
				usage = *chunk.Usage
			}
		}

		pw.Close()
		// Use a fresh context: the request context may be cancelled by the time
		// the stream finishes (client disconnected), which would silently drop the log.
		p.record(context.Background(), usage.PromptTokens, usage.CompletionTokens, status, time.Since(start).Milliseconds(), "")
	}()

	return pr
}

func (p *Provider) record(ctx context.Context, promptTokens, completionTokens, status int, latency int64, errStr string) {
	if p.logStore == nil {
		return
	}
	total := promptTokens + completionTokens
	cost := pricing.Calculate(p.model, promptTokens, completionTokens)

	entry := storage.LogEntry{
		Model:            p.model,
		Provider:         p.prov,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      total,
		CostUSD:          cost,
		LatencyMs:        latency,
		StatusCode:       status,
		KeyID:            p.keyID,
		Error:            errStr,
	}
	_ = p.logStore.Insert(ctx, entry)
}
