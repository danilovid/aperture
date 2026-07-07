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

// dlpTestRouter wires a router with DLP enabled and a fake upstream that
// echoes the request body it received.
func dlpTestRouter(t *testing.T) (http.Handler, *storage.MemDLPStore, *string) {
	t.Helper()
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		upstreamBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-upstream"}); err != nil {
		t.Fatal(err)
	}
	dlp := storage.NewMemDLPStore(100)
	h := Routes(Options{
		KeyStore:      ks,
		DLPStore:      dlp,
		Inspector:     inspector.New(),
		DLPPolicy:     inspector.DefaultPolicy(),
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
	return h, dlp, &upstreamBody
}

func postChat(h http.Handler, content string) *httptest.ResponseRecorder {
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"` + content + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDLPBlocksSecret(t *testing.T) {
	h, dlp, upstreamBody := dlpTestRouter(t)

	rec := postChat(h, "deploy with AKIAIOSFODNN7EXAMPLE now")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
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
	if *upstreamBody != "" {
		t.Error("blocked request reached upstream")
	}

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Action != "blocked" || events[0].Rule != "aws-access-key" {
		t.Errorf("event mismatch: %+v", events)
	}
	if strings.Contains(events[0].MaskedSample, "IOSFODNN7EXAMPLE") {
		t.Error("event leaked the raw secret")
	}
}

func TestDLPRedactsPII(t *testing.T) {
	h, dlp, upstreamBody := dlpTestRouter(t)

	rec := postChat(h, "reach me at ivan.petrov@corp.io")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(*upstreamBody, "[REDACTED:email]") || strings.Contains(*upstreamBody, "ivan.petrov@corp.io") {
		t.Errorf("upstream body not redacted: %s", *upstreamBody)
	}

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Action != "redacted" {
		t.Errorf("event mismatch: %+v", events)
	}
}

func TestDLPPassesCleanTraffic(t *testing.T) {
	h, dlp, upstreamBody := dlpTestRouter(t)

	rec := postChat(h, "write a haiku about proxies")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(*upstreamBody, "write a haiku about proxies") {
		t.Errorf("clean body altered: %s", *upstreamBody)
	}
	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 0 {
		t.Errorf("clean request produced events: %+v", events)
	}
}

func TestDLPEventsEndpoint(t *testing.T) {
	h, _, _ := dlpTestRouter(t)
	postChat(h, "key AKIAIOSFODNN7EXAMPLE")
	postChat(h, "mail a@b.co")

	req := httptest.NewRequest(http.MethodGet, "/admin/dlp/events?action=blocked", nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Events []storage.DLPEvent `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) != 1 || resp.Events[0].Action != "blocked" {
		t.Errorf("filtered events mismatch: %+v", resp.Events)
	}

	// Summary
	req = httptest.NewRequest(http.MethodGet, "/admin/dlp/summary", nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var sum storage.DLPSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Total != 2 || sum.Blocked != 1 || sum.Redacted != 1 {
		t.Errorf("summary mismatch: %+v", sum)
	}
}
