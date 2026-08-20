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
	"github.com/danilovid/aperture/internal/ner"
	"github.com/danilovid/aperture/internal/storage"
)

// stubNER speaks the sidecar contract and marks every occurrence of a name,
// so the whole path — gateway, HTTP client, model service — is exercised.
func stubNER(t *testing.T, name string, fail bool) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var req struct {
			Texts []string `json:"texts"`
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)

		type span struct {
			Start int     `json:"start"`
			End   int     `json:"end"`
			Label string  `json:"label"`
			Score float64 `json:"score"`
		}
		results := make([]struct {
			Spans []span `json:"spans"`
		}, len(req.Texts))
		for i, text := range req.Texts {
			if idx := strings.Index(text, name); idx >= 0 {
				results[i].Spans = append(results[i].Spans, span{
					Start: idx, End: idx + len(name), Label: "PER", Score: 0.98,
				})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func nerRouter(t *testing.T, nerURL string, failClosed bool, policy inspector.Policy) (http.Handler, *storage.MemDLPStore) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{}}`))
	}))
	t.Cleanup(upstream.Close)

	det, err := ner.New(ner.Config{URL: nerURL, Timeout: 2 * time.Second, MinScore: 0.5})
	if err != nil {
		t.Fatal(err)
	}

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-upstream"}); err != nil {
		t.Fatal(err)
	}
	dlp := storage.NewMemDLPStore(50)
	h := Routes(Options{
		KeyStore:      ks,
		DLPStore:      dlp,
		PolicyStore:   storage.NewMemPolicyStore(policy),
		Inspector:     inspector.New().WithDetector(det, failClosed),
		DLPPolicy:     policy,
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
	return h, dlp
}

func nerChat(h http.Handler, content string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"`+content+`"}]}`))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNEREndToEndRedactsAName(t *testing.T) {
	srv, calls := stubNER(t, "Ivan Petrov", false)
	policy := inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionRedact, NER: true}
	h, dlp := nerRouter(t, srv.URL, false, policy)

	if code := nerChat(h, "please ask Ivan Petrov to review").Code; code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if *calls != 1 {
		t.Errorf("model service called %d times, want 1", *calls)
	}

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 {
		t.Fatalf("events = %+v, want the name recorded", events)
	}
	if events[0].Rule != "ner:person" || events[0].Group != "pii" {
		t.Errorf("event = %+v, want a ner:person PII finding", events[0])
	}
	if strings.Contains(events[0].MaskedSample, "Petrov") {
		t.Errorf("the incident feed stored the name in the clear: %q", events[0].MaskedSample)
	}
}

func TestNERBlocksWhenPIIBlocks(t *testing.T) {
	srv, _ := stubNER(t, "Ivan Petrov", false)
	policy := inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionBlock, NER: true}
	h, _ := nerRouter(t, srv.URL, false, policy)

	rec := nerChat(h, "please ask Ivan Petrov to review")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ner:person") {
		t.Errorf("the error should name the rule: %s", rec.Body.String())
	}
}

// The gateway must survive its model service: regexes still run.
func TestNERFailOpenKeepsTheGatewayWorking(t *testing.T) {
	srv, _ := stubNER(t, "Ivan Petrov", true) // always 500
	policy := inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionRedact, NER: true}
	h, dlp := nerRouter(t, srv.URL, false, policy)

	if code := nerChat(h, "please ask Ivan Petrov to review").Code; code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a broken model service must not stop traffic", code)
	}
	if code := nerChat(h, "leaking AKIAIOSFODNN7EXAMPLE").Code; code != http.StatusForbidden {
		t.Errorf("status = %d, want the regex detector to still block", code)
	}
	if events, _ := dlp.List(context.Background(), storage.DLPFilter{}); len(events) != 1 {
		t.Errorf("events = %+v, want only the regex finding", events)
	}
}

func TestNERFailClosedRefusesTraffic(t *testing.T) {
	srv, _ := stubNER(t, "Ivan Petrov", true)
	policy := inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionRedact, NER: true}
	h, dlp := nerRouter(t, srv.URL, true, policy)

	rec := nerChat(h, "a perfectly ordinary prompt")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when failing closed", rec.Code)
	}
	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Rule != "ner:unavailable" {
		t.Errorf("events = %+v, want the outage recorded", events)
	}
}

// The stage is per policy: a key that has not asked for it pays nothing.
func TestNERNotCalledWithoutThePolicyFlag(t *testing.T) {
	srv, calls := stubNER(t, "Ivan Petrov", false)
	policy := inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionRedact}
	h, _ := nerRouter(t, srv.URL, false, policy)

	nerChat(h, "please ask Ivan Petrov to review")
	if *calls != 0 {
		t.Errorf("model service called %d times with ner off", *calls)
	}
}

// The console's dry-run must show what live traffic would get, model stage
// included — otherwise turning the toggle on looks like it did nothing.
func TestNERAppearsInThePolicyDryRun(t *testing.T) {
	srv, _ := stubNER(t, "Ivan Petrov", false)
	policy := inspector.Policy{
		Secrets: inspector.ActionBlock, PII: inspector.ActionRedact,
		Custom: inspector.ActionOff, NER: true,
	}
	h, _ := nerRouter(t, srv.URL, false, policy)

	body, _ := json.Marshal(map[string]any{
		"text":   "please ask Ivan Petrov to review",
		"policy": policy,
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/policies/test", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Verdict      string `json:"verdict"`
		UpstreamText string `json:"upstream_text"`
		Findings     []struct {
			Rule string `json:"rule"`
		} `json:"findings"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Verdict != "redact" || len(out.Findings) != 1 || out.Findings[0].Rule != "ner:person" {
		t.Fatalf("dry run = %+v, want the name found", out)
	}
	if strings.Contains(out.UpstreamText, "Ivan Petrov") {
		t.Errorf("preview shows the name reaching the provider: %q", out.UpstreamText)
	}
}
