package server

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/pricing"
	"github.com/danilovid/aperture/internal/storage"
)

// streamSSE copies an event stream to the client, flushing every line so the caller
// sees tokens as they arrive, and hands each `data:` payload to onData for
// usage extraction. Providers differ in their event shapes, so parsing is the
// caller's job.
func streamSSE(w io.Writer, flusher http.Flusher, upstream io.Reader, onData func(data []byte)) {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return // client went away
		}
		flusher.Flush()

		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" || data == "[DONE]" {
			continue
		}
		onData([]byte(data))
	}
}

// recordUsage writes one request-log row for a natively proxied request. The
// native paths bypass the interceptor, which speaks the chat-completions usage
// shape, so metering happens here.
func (h *Handlers) recordUsage(provider, model, keyID string, in, out, status int, latency time.Duration, errStr string) {
	if h.LogStore == nil {
		return
	}
	entry := storage.LogEntry{
		Model:            model,
		Provider:         provider,
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
