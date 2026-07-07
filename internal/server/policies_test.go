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

func policyTestRouter(t *testing.T) (http.Handler, *storage.MemPolicyStore) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-upstream"}); err != nil {
		t.Fatal(err)
	}
	ps := storage.NewMemPolicyStore(inspector.DefaultPolicy())
	h := Routes(Options{
		KeyStore:      ks,
		DLPStore:      storage.NewMemDLPStore(100),
		PolicyStore:   ps,
		Inspector:     inspector.New(),
		DLPPolicy:     inspector.DefaultPolicy(),
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
	return h, ps
}

func adminReq(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer admin-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPoliciesCRUD(t *testing.T) {
	h, _ := policyTestRouter(t)

	// Defaults present.
	rec := adminReq(h, http.MethodGet, "/admin/policies", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
	var got struct {
		Default inspector.Policy            `json:"default"`
		Keys    map[string]inspector.Policy `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Default.Secrets != inspector.ActionBlock || len(got.Keys) != 0 {
		t.Errorf("unexpected initial policies: %+v", got)
	}

	// Bind a per-key policy.
	rec = adminReq(h, http.MethodPut, "/admin/policies/keys/runtime",
		`{"secrets":"redact","pii":"off","custom":"alert","custom_rules":[{"name":"proj","pattern":"project-x"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = adminReq(h, http.MethodGet, "/admin/policies", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Keys["runtime"].Secrets != inspector.ActionRedact {
		t.Errorf("per-key policy not saved: %+v", got.Keys)
	}

	// Delete reverts to default.
	rec = adminReq(h, http.MethodDelete, "/admin/policies/keys/runtime", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", rec.Code)
	}
}

func TestPolicyValidation(t *testing.T) {
	h, _ := policyTestRouter(t)

	// Unknown action.
	rec := adminReq(h, http.MethodPut, "/admin/policies/default",
		`{"secrets":"explode","pii":"redact","custom":"alert"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid action accepted: %d", rec.Code)
	}

	// Broken custom regex.
	rec = adminReq(h, http.MethodPut, "/admin/policies/default",
		`{"secrets":"block","pii":"redact","custom":"alert","custom_rules":[{"name":"bad","pattern":"("}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid regex accepted: %d", rec.Code)
	}
}

func TestPolicyDryRun(t *testing.T) {
	h, _ := policyTestRouter(t)

	rec := adminReq(h, http.MethodPost, "/admin/policies/test",
		`{"text":"use AKIAIOSFODNN7EXAMPLE and mail ivan@corp.io"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Verdict      inspector.Action    `json:"verdict"`
		Findings     []inspector.Finding `json:"findings"`
		UpstreamText string              `json:"upstream_text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Verdict != inspector.ActionBlock || len(resp.Findings) != 2 || resp.UpstreamText != "" {
		t.Errorf("dry-run mismatch: %+v", resp)
	}

	// Unsaved policy override: redact-only.
	rec = adminReq(h, http.MethodPost, "/admin/policies/test",
		`{"text":"mail ivan@corp.io","policy":{"secrets":"off","pii":"redact","custom":"off"}}`)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Verdict != inspector.ActionRedact || resp.UpstreamText != "mail [REDACTED:email]" {
		t.Errorf("override dry-run mismatch: %+v", resp)
	}
}

func TestHotPolicySwitchAffectsChat(t *testing.T) {
	h, _ := policyTestRouter(t)

	send := func() int {
		body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"mail ivan@corp.io"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer ap-test")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Default policy: PII → redact → 200.
	if code := send(); code != http.StatusOK {
		t.Fatalf("before switch: %d", code)
	}

	// Tighten the key's policy to block PII — no restart.
	rec := adminReq(h, http.MethodPut, "/admin/policies/keys/runtime",
		`{"secrets":"block","pii":"block","custom":"off"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", rec.Code)
	}
	if code := send(); code != http.StatusForbidden {
		t.Errorf("after switch: %d, want 403", code)
	}

	// Revert to default → 200 again.
	adminReq(h, http.MethodDelete, "/admin/policies/keys/runtime", "")
	if code := send(); code != http.StatusOK {
		t.Errorf("after revert: %d, want 200", code)
	}
}
