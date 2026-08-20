package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

// messagesRouter wires a gateway whose Anthropic upstream is a fake that
// echoes back what it received, so tests can assert what actually left.
func messagesRouter(t *testing.T) (http.Handler, *storage.MemDLPStore, *string, *http.Header) {
	t.Helper()
	var seenBody string
	var seenHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
		seenHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"ok"}],
			"usage":{"input_tokens":11,"output_tokens":7}}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"anthropic": "sk-ant-upstream"}); err != nil {
		t.Fatal(err)
	}
	dlp := storage.NewMemDLPStore(100)
	h := Routes(Options{
		KeyStore:         ks,
		DLPStore:         dlp,
		Inspector:        inspector.New(),
		DLPPolicy:        inspector.DefaultPolicy(),
		AnthropicBaseURL: upstream.URL,
		AdminAPIKey:      "admin-test",
		Logger:           slog.Default(),
	})
	return h, dlp, &seenBody, &seenHeaders
}

func postMessages(h http.Handler, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Claude Code authenticates with x-api-key, not a Bearer header.
func TestMessagesAcceptsXAPIKeyAndBearer(t *testing.T) {
	h, _, _, _ := messagesRouter(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`

	if rec := postMessages(h, body, map[string]string{"x-api-key": "ap-test"}); rec.Code != http.StatusOK {
		t.Errorf("x-api-key: status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec := postMessages(h, body, map[string]string{"Authorization": "Bearer ap-test"}); rec.Code != http.StatusOK {
		t.Errorf("bearer: status = %d, want 200", rec.Code)
	}
	if rec := postMessages(h, body, map[string]string{"x-api-key": "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad key: status = %d, want 401", rec.Code)
	}
	if rec := postMessages(h, body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no key: status = %d, want 401", rec.Code)
	}
}

func TestMessagesBlocksSecretBeforeUpstream(t *testing.T) {
	h, dlp, seenBody, _ := messagesRouter(t)

	rec := postMessages(h, `{"model":"claude-3-5-sonnet-20241022","max_tokens":16,
		"messages":[{"role":"user","content":"deploy AKIAIOSFODNN7EXAMPLE"}]}`,
		map[string]string{"x-api-key": "ap-test"})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if *seenBody != "" {
		t.Error("blocked request still reached the upstream")
	}

	// Anthropic-shaped error so SDKs classify it correctly.
	var resp struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Aperture struct {
			BlockedBy string   `json:"blocked_by"`
			Rules     []string `json:"rules"`
		} `json:"aperture"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "error" || resp.Error.Type != "permission_error" {
		t.Errorf("not an Anthropic-shaped error: %+v", resp)
	}
	if resp.Aperture.BlockedBy != "dlp" || len(resp.Aperture.Rules) == 0 {
		t.Errorf("missing aperture detail: %+v", resp.Aperture)
	}

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Action != "blocked" || events[0].Provider != "anthropic" {
		t.Errorf("event mismatch: %+v", events)
	}
}

func TestMessagesRedactsBeforeUpstream(t *testing.T) {
	h, dlp, seenBody, _ := messagesRouter(t)

	rec := postMessages(h, `{"model":"claude-3-5-sonnet-20241022","max_tokens":16,
		"messages":[{"role":"user","content":[{"type":"text","text":"reach ivan@corp.io"}]}]}`,
		map[string]string{"x-api-key": "ap-test"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(*seenBody, "[REDACTED:email]") || strings.Contains(*seenBody, "ivan@corp.io") {
		t.Errorf("upstream body not redacted: %s", *seenBody)
	}
	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Action != "redacted" {
		t.Errorf("event mismatch: %+v", events)
	}
}

func TestMessagesForwardsHeadersAndCleanBody(t *testing.T) {
	h, _, seenBody, seenHeaders := messagesRouter(t)
	body := `{"model":"claude-3-5-sonnet-20241022","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`

	rec := postMessages(h, body, map[string]string{
		"x-api-key":         "ap-test",
		"anthropic-version": "2023-06-01",
		"anthropic-beta":    "prompt-caching-2024-07-31",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if *seenBody != body {
		t.Errorf("clean body was altered:\n got: %s\nwant: %s", *seenBody, body)
	}
	if got := seenHeaders.Get("x-api-key"); got != "sk-ant-upstream" {
		t.Errorf("upstream got wrong provider key: %q", got)
	}
	if got := seenHeaders.Get("anthropic-beta"); got != "prompt-caching-2024-07-31" {
		t.Errorf("anthropic-beta not forwarded: %q", got)
	}
	if got := seenHeaders.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version not forwarded: %q", got)
	}
}

func TestMessagesWithoutAnthropicKeyIsRejected(t *testing.T) {
	ks := config.NewRuntimeStore("ap-test").KeyStore()
	// only an OpenAI key configured — no anthropic key
	ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-x"})
	h := Routes(Options{
		KeyStore:    ks,
		Inspector:   inspector.New(),
		DLPPolicy:   inspector.DefaultPolicy(),
		AdminAPIKey: "admin-test",
		Logger:      slog.Default(),
	})
	rec := postMessages(h, `{"model":"claude-3-5-sonnet-20241022","messages":[]}`,
		map[string]string{"x-api-key": "ap-test"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAnthropicUsageParsing(t *testing.T) {
	in, out := anthropicUsageFromJSON([]byte(`{"usage":{"input_tokens":11,"output_tokens":7}}`))
	if in != 11 || out != 7 {
		t.Errorf("got %d/%d, want 11/7", in, out)
	}
}

// fakeLogStore captures usage rows so the streaming meter can be asserted.
type fakeLogStore struct{ entries []storage.LogEntry }

func (f *fakeLogStore) Insert(_ context.Context, e storage.LogEntry) error {
	f.entries = append(f.entries, e)
	return nil
}
func (f *fakeLogStore) List(context.Context, storage.LogFilter) ([]storage.LogEntry, error) {
	return f.entries, nil
}
func (f *fakeLogStore) Summary(context.Context, time.Time) (storage.StatsSummary, error) {
	return storage.StatsSummary{}, nil
}
func (f *fakeLogStore) Timeseries(context.Context, time.Time, int) ([]storage.TimeseriesBucket, error) {
	return nil, nil
}
func (f *fakeLogStore) ModelStats(context.Context, time.Time) ([]storage.ModelStat, error) {
	return nil, nil
}

func TestMessagesStreamsAndMetersUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for _, ev := range []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":31,"output_tokens":0}}}` + "\n\n",
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}` + "\n\n",
			`event: message_delta` + "\n" + `data: {"type":"message_delta","usage":{"output_tokens":17}}` + "\n\n",
		} {
			w.Write([]byte(ev))
			if f != nil {
				f.Flush()
			}
		}
	}))
	defer upstream.Close()

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"anthropic": "sk-ant-upstream"}); err != nil {
		t.Fatal(err)
	}
	logs := &fakeLogStore{}
	h := Routes(Options{
		KeyStore:         ks,
		LogStore:         logs,
		DLPStore:         storage.NewMemDLPStore(10),
		Inspector:        inspector.New(),
		DLPPolicy:        inspector.DefaultPolicy(),
		AnthropicBaseURL: upstream.URL,
		AdminAPIKey:      "admin-test",
		Logger:           slog.Default(),
	})

	rec := postMessages(h, `{"model":"claude-3-5-sonnet-20241022","max_tokens":64,"stream":true,
		"messages":[{"role":"user","content":"stream please"}]}`,
		map[string]string{"x-api-key": "ap-test"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want SSE", ct)
	}
	// The SSE frames must survive the proxy intact.
	body := rec.Body.String()
	for _, want := range []string{"event: message_start", `"text_delta"`, "event: message_delta"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream missing %q:\n%s", want, body)
		}
	}

	if len(logs.entries) != 1 {
		t.Fatalf("want 1 usage row, got %d", len(logs.entries))
	}
	e := logs.entries[0]
	if e.PromptTokens != 31 || e.CompletionTokens != 17 {
		t.Errorf("usage = %d/%d, want 31/17 (streaming meter did not read the SSE events)",
			e.PromptTokens, e.CompletionTokens)
	}
	if e.Provider != "anthropic" || e.CostUSD <= 0 {
		t.Errorf("unexpected row: provider=%s cost=%v", e.Provider, e.CostUSD)
	}
}
