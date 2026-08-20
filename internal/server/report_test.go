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
	"github.com/danilovid/aperture/internal/report"
	"github.com/danilovid/aperture/internal/storage"
)

// reportRouter runs the gateway the way a team evaluating Aperture does:
// alert-only for secrets, so nothing is rejected yet and the report has to
// say what flipping the switch would cost.
func reportRouter(t *testing.T) (http.Handler, storage.PolicyStore) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-upstream"}); err != nil {
		t.Fatal(err)
	}
	alertOnly := inspector.Policy{
		Secrets: inspector.ActionAlert,
		PII:     inspector.ActionRedact,
		Custom:  inspector.ActionOff,
	}
	ps := storage.NewMemPolicyStore(alertOnly)
	h := Routes(Options{
		KeyStore:      ks,
		DLPStore:      storage.NewMemDLPStore(100),
		PolicyStore:   ps,
		Inspector:     inspector.New(),
		DLPPolicy:     alertOnly,
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
	return h, ps
}

func chatAs(h http.Handler, agent, content string) int {
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"` + content + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	if agent != "" {
		req.Header.Set("X-Aperture-Agent", agent)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func getReport(t *testing.T, h http.Handler, query string) report.Report {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/dlp/report"+query, nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/dlp/report = %d: %s", rec.Code, rec.Body.String())
	}
	var rep report.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode report: %v\n%s", err, rec.Body.String())
	}
	return rep
}

func TestReportAnswersWhatBlockWouldCost(t *testing.T) {
	h, _ := reportRouter(t)

	// A week of alert-mode traffic: two leaked keys and an email, all passed.
	for i := 0; i < 3; i++ {
		if code := chatAs(h, "ci-bot", "deploy with AKIAIOSFODNN7EXAMPLE"); code != http.StatusOK {
			t.Fatalf("alert-mode request = %d, want 200 (nothing should be rejected yet)", code)
		}
	}
	chatAs(h, "qa-bot", "ping alice@example.com")

	rep := getReport(t, h, "?period=7d")

	if rep.Period != "7d" {
		t.Errorf("period = %q, want 7d", rep.Period)
	}
	if rep.Totals.Total != 4 || rep.Totals.Blocked != 0 {
		t.Errorf("totals = %+v, want 4 events and no blocks", rep.Totals)
	}
	secrets := rep.WouldBlock.Groups["secrets"]
	if secrets.WouldBlock != 3 {
		t.Errorf("secrets would_block = %d, want 3", secrets.WouldBlock)
	}
	if rep.WouldBlock.Total != 4 {
		t.Errorf("total would_block = %d, want 4 (3 secrets + 1 redacted email)", rep.WouldBlock.Total)
	}
	if len(rep.Rules) == 0 || rep.Rules[0].Rule != "aws-access-key" {
		t.Fatalf("rules = %+v, want aws-access-key first", rep.Rules)
	}
	if rep.Rules[0].Sample == "AKIAIOSFODNN7EXAMPLE" {
		t.Error("report leaked the raw secret instead of a masked sample")
	}
	if rep.DefaultPolicy.Secrets != "alert" {
		t.Errorf("default policy = %+v, want secrets=alert", rep.DefaultPolicy)
	}
	if len(rep.Keys) != 1 || rep.Keys[0].Policy.Secrets != "alert" {
		t.Errorf("keys = %+v, want one key carrying its policy", rep.Keys)
	}

	agents := map[string]int64{}
	for _, a := range rep.Agents {
		agents[a.Agent] = a.WouldBlock
	}
	if agents["ci-bot"] != 3 || agents["qa-bot"] != 1 {
		t.Errorf("agent breakdown = %+v", agents)
	}
}

// Traffic already blocked is not a change, and muted rules stay muted.
func TestReportExcludesBlockedAndMuted(t *testing.T) {
	h, ps := reportRouter(t)
	ctx := context.Background()
	if err := ps.SetPolicy(ctx, "runtime", inspector.Policy{
		Secrets:    inspector.ActionBlock,
		PII:        inspector.ActionAlert,
		Custom:     inspector.ActionOff,
		MutedRules: []string{"email"},
	}); err != nil {
		t.Fatal(err)
	}

	if code := chatAs(h, "", "deploy with AKIAIOSFODNN7EXAMPLE"); code != http.StatusForbidden {
		t.Fatalf("block-mode request = %d, want 403", code)
	}
	chatAs(h, "", "ping alice@example.com") // muted → recorded as suppressed

	rep := getReport(t, h, "")
	if rep.Period != "7d" {
		t.Errorf("period = %q, want the 7d default", rep.Period)
	}
	if rep.Totals.Blocked != 1 || rep.Totals.Suppressed != 1 {
		t.Errorf("totals = %+v, want 1 blocked and 1 suppressed", rep.Totals)
	}
	if rep.WouldBlock.Total != 0 {
		t.Errorf("would_block = %d, want 0: one is already blocked, the other is muted on purpose",
			rep.WouldBlock.Total)
	}
}

func TestReportRequiresAdmin(t *testing.T) {
	h, _ := reportRouter(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/dlp/report", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated report = %d, want 401", rec.Code)
	}
}
