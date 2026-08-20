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
	streamSSEFiltered(w, flusher, upstream, onData, nil)
}

// streamSSEFiltered is streamSSE with a filter that may rewrite each line
// before it reaches the client, or stop the stream outright — that is how
// response scanning redacts a delta in flight and how a block tears the
// stream down. The filter returns the exact bytes to write, so it can also
// insert events of its own. A nil filter passes every line through unchanged.
func streamSSEFiltered(w io.Writer, flusher http.Flusher, upstream io.Reader,
	onData func(data []byte), filter func(line string) (emit string, stop bool),
) {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		out, stop := line+"\n", false
		if filter != nil {
			out, stop = filter(line)
		}
		if out != "" {
			if _, err := w.Write([]byte(out)); err != nil {
				return // client went away
			}
			flusher.Flush()
		}
		if stop {
			return
		}

		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" || data == "[DONE]" {
			continue
		}
		if onData != nil {
			onData([]byte(data))
		}
	}
}

// recordUsage writes one request-log row for a natively proxied request. The
// native paths bypass the interceptor, which speaks the chat-completions usage
// shape, so metering happens here.
func (h *Handlers) recordUsage(m reqMeta, in, out, status int, latency time.Duration, errStr string) {
	entry := storage.LogEntry{
		Model:            m.model,
		Provider:         h.resolveLLM(m.model),
		PromptTokens:     in,
		CompletionTokens: out,
		TotalTokens:      in + out,
		CostUSD:          pricing.Calculate(m.model, in, out),
		LatencyMs:        latency.Milliseconds(),
		StatusCode:       status,
		KeyID:            m.keyID,
		Error:            errStr,
		Agent:            m.agent,
		Session:          m.session,
	}
	h.observeUsage(entry)
	if h.LogStore == nil {
		return
	}
	// A fresh context: the request context may already be cancelled when a
	// stream finishes, which would silently drop the row.
	if err := h.LogStore.Insert(context.Background(), entry); err != nil {
		h.Logger.Error("usage log insert failed", "err", err)
	}
}
