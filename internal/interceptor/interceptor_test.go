package interceptor

import (
	"context"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mutegate/mutegate/internal/storage"
)

// canned answers every chat completion with the same body.
type canned struct{ body, contentType string }

func (c canned) Models(context.Context) (io.ReadCloser, string, int, error) {
	return io.NopCloser(strings.NewReader(`{}`)), "application/json", 200, nil
}

func (c canned) ChatCompletions(context.Context, io.Reader, string) (io.ReadCloser, string, int, error) {
	return io.NopCloser(strings.NewReader(c.body)), c.contentType, 200, nil
}

// meter runs one request through the interceptor and returns what it recorded.
func meter(t *testing.T, inner canned, request string) storage.LogEntry {
	t.Helper()
	got := make(chan storage.LogEntry, 1)
	p := New(inner, nil, storage.LogEntry{Model: "gpt-4o-mini"}, func(e storage.LogEntry) { got <- e })
	rc, _, _, err := p.ChatCompletions(context.Background(), strings.NewReader(request), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(rc) // a stream is recorded once it has been read to the end
	rc.Close()
	select {
	case e := <-got:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("nothing recorded")
		return storage.LogEntry{}
	}
}

// OpenAI caches long prompts on its own and reports the cached part in
// prompt_tokens_details; it is billed at the cache-read rate, not in full.
func TestCachedPromptTokensCostLess(t *testing.T) {
	// gpt-4o-mini: $0.15 per million in, $0.075 a cached one.
	want := (200_000*0.15 + 800_000*0.075) / 1e6

	e := meter(t, canned{contentType: "application/json", body: `{"choices":[],
		"usage":{"prompt_tokens":1000000,"completion_tokens":0,"total_tokens":1000000,
		"prompt_tokens_details":{"cached_tokens":800000}}}`},
		`{"model":"gpt-4o-mini","messages":[]}`)
	if e.PromptTokens != 1_000_000 || math.Abs(e.CostUSD-want) > 1e-9 {
		t.Errorf("non-streaming: %d tokens, $%v; want 1000000, $%v", e.PromptTokens, e.CostUSD, want)
	}

	e = meter(t, canned{contentType: "text/event-stream", body: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		`data: {"choices":[],"usage":{"prompt_tokens":1000000,"completion_tokens":0,"total_tokens":1000000,"prompt_tokens_details":{"cached_tokens":800000}}}` + "\n\n" +
		"data: [DONE]\n\n"},
		`{"model":"gpt-4o-mini","stream":true,"messages":[]}`)
	if e.PromptTokens != 1_000_000 || math.Abs(e.CostUSD-want) > 1e-9 {
		t.Errorf("streaming: %d tokens, $%v; want 1000000, $%v", e.PromptTokens, e.CostUSD, want)
	}
}
