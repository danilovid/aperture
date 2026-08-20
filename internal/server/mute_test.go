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

// chatRouterWithDLP wires a gateway with a policy store (so mutes can be
// saved), a DLP store and an upstream that reports what it received.
func chatRouterWithDLP(t *testing.T) (http.Handler, *storage.MemDLPStore, *string) {
	t.Helper()
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		upstreamBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
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
		PolicyStore:   storage.NewMemPolicyStore(inspector.DefaultPolicy()),
		Inspector:     inspector.New(),
		DLPPolicy:     inspector.DefaultPolicy(),
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
	return h, dlp, &upstreamBody
}

// Muting from the incident feed must work even when the key has no policy of
// its own yet — the common case when you click "mute" on a fresh key.
func TestMuteCreatesKeyPolicyFromDefault(t *testing.T) {
	h, ps := policyTestRouter(t)

	rec := adminReq(h, http.MethodPost, "/admin/policies/keys/runtime/mute", `{"rule":"email"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("mute status = %d: %s", rec.Code, rec.Body.String())
	}

	p, ok, err := ps.GetPolicy(context.Background(), "runtime")
	if err != nil || !ok {
		t.Fatalf("per-key policy not created: ok=%v err=%v", ok, err)
	}
	if len(p.MutedRules) != 1 || p.MutedRules[0] != "email" {
		t.Errorf("muted rules = %+v", p.MutedRules)
	}
	// The rest of the default policy is preserved.
	if p.Secrets != inspector.ActionBlock {
		t.Errorf("default actions lost on mute: %+v", p)
	}
}

func TestMuteIsIdempotentAndUnmuteReverses(t *testing.T) {
	h, ps := policyTestRouter(t)

	adminReq(h, http.MethodPost, "/admin/policies/keys/runtime/mute", `{"rule":"email"}`)
	adminReq(h, http.MethodPost, "/admin/policies/keys/runtime/mute", `{"rule":"email"}`)
	p, _, _ := ps.GetPolicy(context.Background(), "runtime")
	if len(p.MutedRules) != 1 {
		t.Errorf("muting twice duplicated the rule: %+v", p.MutedRules)
	}

	rec := adminReq(h, http.MethodPost, "/admin/policies/keys/runtime/unmute", `{"rule":"EMAIL"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unmute status = %d", rec.Code)
	}
	p, _, _ = ps.GetPolicy(context.Background(), "runtime")
	if len(p.MutedRules) != 0 {
		t.Errorf("unmute (case-insensitive) failed: %+v", p.MutedRules)
	}
}

func TestMuteRequiresRule(t *testing.T) {
	h, _ := policyTestRouter(t)
	if rec := adminReq(h, http.MethodPost, "/admin/policies/keys/runtime/mute", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty rule accepted: %d", rec.Code)
	}
	if rec := adminReq(h, http.MethodPost, "/admin/policies/keys/default/mute", `{"rule":"email"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("muting the reserved 'default' id accepted: %d", rec.Code)
	}
}

func TestMuteRequiresAdmin(t *testing.T) {
	h, _ := policyTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/policies/keys/runtime/mute", strings.NewReader(`{"rule":"email"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("mute without admin key: %d, want 401", rec.Code)
	}
}

// The whole point: a mute changes live traffic immediately, and the silenced
// match is still recorded so it is auditable.
func TestMuteAffectsLiveTrafficAndIsRecorded(t *testing.T) {
	h, dlp, upstreamBody := chatRouterWithDLP(t)

	send := func() int {
		body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"mail ivan@corp.io"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer ap-test")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Before: PII is redacted.
	send()
	if !strings.Contains(*upstreamBody, "[REDACTED:email]") {
		t.Fatalf("expected redaction before muting: %s", *upstreamBody)
	}

	rec := adminReq(h, http.MethodPost, "/admin/policies/keys/runtime/mute", `{"rule":"email"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("mute failed: %s", rec.Body.String())
	}

	// After: the address reaches the provider untouched — no restart needed.
	send()
	if !strings.Contains(*upstreamBody, "ivan@corp.io") || strings.Contains(*upstreamBody, "REDACTED") {
		t.Errorf("mute did not take effect on live traffic: %s", *upstreamBody)
	}

	// …and the suppression is visible in the feed, not silent.
	events, _ := dlp.List(context.Background(), storage.DLPFilter{Action: "suppressed"})
	if len(events) != 1 || events[0].Rule != "email" {
		t.Errorf("suppressed event not recorded: %+v", events)
	}
	sum, _ := dlp.Summary(context.Background(), events[0].Ts.Add(-time.Hour))
	if sum.Suppressed != 1 {
		t.Errorf("summary.suppressed = %d, want 1", sum.Suppressed)
	}
}

func TestDryRunReportsSuppressed(t *testing.T) {
	h, _ := policyTestRouter(t)
	rec := adminReq(h, http.MethodPost, "/admin/policies/test",
		`{"text":"key AKIAIOSFODNN7EXAMPLE","policy":{"secrets":"block","pii":"redact","custom":"off","allowlist":["AKIAIOSFODNN7EXAMPLE"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Verdict    string              `json:"verdict"`
		Findings   []inspector.Finding `json:"findings"`
		Suppressed []inspector.Finding `json:"suppressed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Verdict != "off" || len(resp.Findings) != 0 {
		t.Errorf("allowlisted text still flagged: %+v", resp)
	}
	if len(resp.Suppressed) != 1 || resp.Suppressed[0].Rule != "aws-access-key" {
		t.Errorf("dry-run does not explain the suppression: %+v", resp.Suppressed)
	}
}

func TestAllowlistValidationRejectsBadPattern(t *testing.T) {
	h, _ := policyTestRouter(t)
	rec := adminReq(h, http.MethodPut, "/admin/policies/default",
		`{"secrets":"block","pii":"redact","custom":"alert","allowlist":["("]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid allowlist regex accepted: %d", rec.Code)
	}
}
