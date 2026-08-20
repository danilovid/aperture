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

// responsesRouter wires a gateway whose OpenAI upstream reports what it got.
// stream=true makes the fake answer with an SSE stream.
func responsesRouter(t *testing.T) (http.Handler, *storage.MemDLPStore, *fakeLogStore, *string) {
	t.Helper()
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
		if strings.Contains(seen, `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			f, _ := w.(http.Flusher)
			for _, ev := range []string{
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n",
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":42,\"output_tokens\":9}}}\n\n",
			} {
				w.Write([]byte(ev))
				if f != nil {
					f.Flush()
				}
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"resp_1","object":"response","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":13,"output_tokens":4}}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-upstream"}); err != nil {
		t.Fatal(err)
	}
	dlp := storage.NewMemDLPStore(100)
	logs := &fakeLogStore{}
	h := Routes(Options{
		KeyStore:      ks,
		LogStore:      logs,
		DLPStore:      dlp,
		PolicyStore:   storage.NewMemPolicyStore(inspector.DefaultPolicy()),
		Inspector:     inspector.New(),
		DLPPolicy:     inspector.DefaultPolicy(),
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
	return h, dlp, logs, &seen
}

func postResponses(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestResponsesBlocksSecretBeforeUpstream(t *testing.T) {
	h, dlp, _, seen := responsesRouter(t)

	rec := postResponses(h, `{"model":"gpt-4o-mini","input":[
		{"type":"function_call_output","call_id":"c1","output":"AWS_KEY=AKIAIOSFODNN7EXAMPLE"}]}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if *seen != "" {
		t.Error("blocked request reached the upstream")
	}
	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Action != "blocked" {
		t.Errorf("event mismatch: %+v", events)
	}
}

func TestResponsesRedactsBeforeUpstream(t *testing.T) {
	h, _, _, seen := responsesRouter(t)

	rec := postResponses(h, `{"model":"gpt-4o-mini","input":[
		{"role":"user","content":[{"type":"input_text","text":"reach ivan@corp.io"}]}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(*seen, "[REDACTED:email]") || strings.Contains(*seen, "ivan@corp.io") {
		t.Errorf("upstream body not redacted: %s", *seen)
	}
}

func TestResponsesCleanBodyAndUsage(t *testing.T) {
	h, _, logs, seen := responsesRouter(t)
	body := `{"model":"gpt-4o-mini","input":"hello"}`

	if rec := postResponses(h, body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if *seen != body {
		t.Errorf("clean body altered:\n got: %s\nwant: %s", *seen, body)
	}
	if len(logs.entries) != 1 {
		t.Fatalf("want 1 usage row, got %d", len(logs.entries))
	}
	e := logs.entries[0]
	if e.PromptTokens != 13 || e.CompletionTokens != 4 || e.Provider != "openai" {
		t.Errorf("usage row wrong: %+v", e)
	}
}

func TestResponsesStreamsAndMetersUsage(t *testing.T) {
	h, _, logs, _ := responsesRouter(t)

	rec := postResponses(h, `{"model":"gpt-4o-mini","stream":true,"input":"stream please"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "response.output_text.delta") {
		t.Errorf("SSE frames did not reach the client:\n%s", rec.Body.String())
	}
	if len(logs.entries) != 1 {
		t.Fatalf("want 1 usage row, got %d", len(logs.entries))
	}
	if e := logs.entries[0]; e.PromptTokens != 42 || e.CompletionTokens != 9 {
		t.Errorf("streaming usage = %d/%d, want 42/9", e.PromptTokens, e.CompletionTokens)
	}
}

func TestResponsesRequiresKey(t *testing.T) {
	h, _, _, _ := responsesRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestResponsesBlockedErrorShape(t *testing.T) {
	h, _, _, _ := responsesRouter(t)
	rec := postResponses(h, `{"model":"gpt-4o-mini","input":"key AKIAIOSFODNN7EXAMPLE"}`)
	var resp struct {
		Error struct {
			Type  string   `json:"type"`
			Rules []string `json:"rules"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error.Type != "aperture_dlp_blocked" || len(resp.Error.Rules) == 0 {
		t.Errorf("unexpected error payload: %+v", resp.Error)
	}
}
