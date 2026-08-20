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

	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

const leakedKey = "AKIAIOSFODNN7EXAMPLE"

// respRouter wires a gateway whose upstream replies with whatever the test
// hands it, so a model "echoing a secret" can be simulated exactly.
func respRouter(t *testing.T, policy inspector.Policy, reply func(w http.ResponseWriter)) (http.Handler, *storage.MemDLPStore) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		reply(w)
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{
		"openai": "sk-upstream", "anthropic": "sk-ant-upstream",
	}); err != nil {
		t.Fatal(err)
	}
	dlp := storage.NewMemDLPStore(100)
	h := Routes(Options{
		KeyStore:         ks,
		DLPStore:         dlp,
		PolicyStore:      storage.NewMemPolicyStore(policy),
		Inspector:        inspector.New(),
		DLPPolicy:        policy,
		OpenAIBaseURL:    upstream.URL,
		AnthropicBaseURL: upstream.URL,
		AdminAPIKey:      "admin-test",
		Logger:           slog.Default(),
	})
	return h, dlp
}

func scanResponsesPolicy(secrets inspector.Action) inspector.Policy {
	return inspector.Policy{Secrets: secrets, PII: inspector.ActionOff, Custom: inspector.ActionOff,
		ScanResponses: true}
}

func post(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sse writes chunks as an event stream, one line each, the way providers do.
func sse(chunks ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}
}

func jsonReply(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

func eventsOf(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "" || data == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("bad SSE payload %q: %v", data, err)
		}
		out = append(out, m)
	}
	return out
}

// chatText reassembles what an OpenAI client would show the user.
func chatText(t *testing.T, body string) string {
	t.Helper()
	var b strings.Builder
	for _, evt := range eventsOf(t, body) {
		choices, _ := evt["choices"].([]any)
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if s, ok := delta["content"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// ── non-streaming ────────────────────────────────────────────────────────────

func TestResponseScanRedactsChatBody(t *testing.T) {
	h, dlp := respRouter(t, scanResponsesPolicy(inspector.ActionRedact),
		jsonReply(`{"choices":[{"message":{"role":"assistant","content":"sure: `+leakedKey+`"}}],"usage":{}}`))

	rec := post(h, "/v1/chat/completions", `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), leakedKey) {
		t.Errorf("the model's secret reached the client: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "[REDACTED:aws-access-key]") {
		t.Errorf("not redacted: %s", rec.Body.String())
	}

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Direction != storage.DirectionResponse {
		t.Fatalf("events = %+v, want one response-direction event", events)
	}
}

func TestResponseScanBlocksChatBody(t *testing.T) {
	h, _ := respRouter(t, scanResponsesPolicy(inspector.ActionBlock),
		jsonReply(`{"choices":[{"message":{"role":"assistant","content":"sure: `+leakedKey+`"}}],"usage":{}}`))

	rec := post(h, "/v1/chat/completions", `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), leakedKey) {
		t.Errorf("the secret rode along with the error: %s", rec.Body.String())
	}
	var body struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
		Aperture struct {
			Direction string `json:"direction"`
		} `json:"aperture"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Type != "aperture_dlp_blocked" || body.Aperture.Direction != "response" {
		t.Errorf("error body does not say what happened: %s", rec.Body.String())
	}
}

// Off by default: an untouched policy must not pay for response scanning.
func TestResponsesAreNotScannedByDefault(t *testing.T) {
	policy := inspector.DefaultPolicy() // secrets: block, but ScanResponses false
	h, dlp := respRouter(t, policy,
		jsonReply(`{"choices":[{"message":{"role":"assistant","content":"sure: `+leakedKey+`"}}],"usage":{}}`))

	rec := post(h, "/v1/chat/completions", `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), leakedKey) {
		t.Errorf("response was scanned without being asked: %d %s", rec.Code, rec.Body.String())
	}
	if events, _ := dlp.List(context.Background(), storage.DLPFilter{}); len(events) != 0 {
		t.Errorf("events recorded with scanning off: %+v", events)
	}
}

// ── streaming ────────────────────────────────────────────────────────────────

// The secret is split across three chunks — exactly the case the window exists
// for.
func TestResponseScanRedactsAcrossStreamChunks(t *testing.T) {
	h, dlp := respRouter(t, scanResponsesPolicy(inspector.ActionRedact), sse(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"here: AKIAIOS"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"FODNN7EX"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"AMPLE, enjoy"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	))

	rec := post(h, "/v1/chat/completions",
		`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	body := rec.Body.String()
	if strings.Contains(body, leakedKey) {
		t.Errorf("secret streamed to the client:\n%s", body)
	}
	if got, want := chatText(t, body), "here: [REDACTED:aws-access-key], enjoy"; got != want {
		t.Errorf("client would see %q, want %q", got, want)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("stream did not finish cleanly")
	}

	events, _ := dlp.List(context.Background(), storage.DLPFilter{Direction: storage.DirectionResponse})
	if len(events) != 1 || events[0].Rule != "aws-access-key" {
		t.Errorf("events = %+v, want the streamed match recorded once", events)
	}
}

func TestResponseScanBlocksMidStream(t *testing.T) {
	h, dlp := respRouter(t, scanResponsesPolicy(inspector.ActionBlock), sse(
		`{"choices":[{"index":0,"delta":{"content":"the key is AKIA"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"IOSFODNN7EXAMPLE"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":" — do not tell anyone"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	))

	rec := post(h, "/v1/chat/completions",
		`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	body := rec.Body.String()
	if strings.Contains(body, leakedKey) || strings.Contains(body, "do not tell anyone") {
		t.Errorf("stream continued after the block:\n%s", body)
	}
	if !strings.Contains(body, "aperture_dlp_blocked") {
		t.Errorf("client was not told why the stream stopped:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("blocked stream must still terminate cleanly")
	}
	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) == 0 || events[0].Direction != storage.DirectionResponse {
		t.Errorf("events = %+v, want the block recorded on the response side", events)
	}
}

// Clean streams must arrive intact — the window is not allowed to eat text.
func TestCleanStreamIsUnchanged(t *testing.T) {
	h, _ := respRouter(t, scanResponsesPolicy(inspector.ActionBlock), sse(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hello, "}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"world"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"! How are you?"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	))

	rec := post(h, "/v1/chat/completions",
		`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if got, want := chatText(t, rec.Body.String()), "Hello, world! How are you?"; got != want {
		t.Errorf("client would see %q, want %q", got, want)
	}
}

// A secret the model puts into a tool call is as dangerous as one in the text.
func TestResponseScanRedactsStreamedToolArguments(t *testing.T) {
	h, _ := respRouter(t, scanResponsesPolicy(inspector.ActionRedact), sse(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"shell","arguments":"{\"cmd\":\"echo AKIAIOS"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"FODNN7EXAMPLE\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	))

	rec := post(h, "/v1/chat/completions",
		`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	body := rec.Body.String()
	if strings.Contains(body, leakedKey) {
		t.Errorf("secret streamed inside tool arguments:\n%s", body)
	}
	if !strings.Contains(body, "REDACTED:aws-access-key") {
		t.Errorf("tool arguments were not redacted:\n%s", body)
	}
}

// ── Anthropic and Responses dialects ─────────────────────────────────────────

func TestResponseScanRedactsAnthropicStream(t *testing.T) {
	h, dlp := respRouter(t, scanResponsesPolicy(inspector.ActionRedact), func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"key AKIAIOS\"}}\n\n"))
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"FODNN7EXAMPLE done\"}}\n\n"))
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, leakedKey) {
		t.Errorf("secret streamed to the Anthropic client:\n%s", body)
	}
	if !strings.Contains(body, "REDACTED:aws-access-key") {
		t.Errorf("not redacted:\n%s", body)
	}
	// The tail must still arrive, before the block is closed.
	if !strings.Contains(body, "done") {
		t.Errorf("text held back at the end was never released:\n%s", body)
	}
	if strings.Index(body, "done") > strings.Index(body, "content_block_stop") {
		t.Errorf("the released tail landed after the block was closed:\n%s", body)
	}
	if events, _ := dlp.List(context.Background(), storage.DLPFilter{}); len(events) != 1 {
		t.Errorf("events = %+v, want one", events)
	}
}

func TestResponseScanRedactsResponsesStreamIncludingTerminalEvents(t *testing.T) {
	h, _ := respRouter(t, scanResponsesPolicy(inspector.ActionRedact), sse(
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"key AKIAIOS"}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"FODNN7EXAMPLE end"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"key `+leakedKey+` end"}`,
		`{"type":"response.completed","response":{"output_text":"key `+leakedKey+` end","output":[{"type":"message","content":[{"type":"output_text","text":"key `+leakedKey+` end"}]}],"usage":{"input_tokens":1,"output_tokens":2}}}`,
	))

	rec := post(h, "/v1/responses", `{"model":"gpt-4o-mini","stream":true,"input":"hi"}`)

	body := rec.Body.String()
	if strings.Contains(body, leakedKey) {
		t.Errorf("a client reading the terminal events would still see the secret:\n%s", body)
	}
	if strings.Count(body, "REDACTED:aws-access-key") < 4 {
		t.Errorf("deltas and terminal events must all be redacted:\n%s", body)
	}
	if !strings.Contains(body, `"end"`) && !strings.Contains(body, "end") {
		t.Errorf("held-back tail never released:\n%s", body)
	}
}
