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

// jevRouter wires a gateway in front of a fake Jev API that records what it
// was actually sent — the only way to prove a redaction reached the wire.
func jevRouter(t *testing.T, policy inspector.Policy) (http.Handler, *storage.MemDLPStore, *string, *string) {
	t.Helper()
	var sawBody, sawPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sawBody, sawPath = string(b), r.URL.Path
		if r.Header.Get("Authorization") != "Bearer jev-upstream-key" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"code":-1,"message":"Sign in or provide a Jev API key.","data":null}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"message":"ok","data":{"decision":"confirm","confidence":0.86,
			"probabilities":{"allow":0.08,"confirm":0.71,"review":0.16,"deny":0.05},
			"guidance":"Obtain explicit user confirmation before invoking the tool.","answers":{}}}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"jev": "jev-upstream-key"}); err != nil {
		t.Fatal(err)
	}
	dlp := storage.NewMemDLPStore(50)
	h := Routes(Options{
		KeyStore:    ks,
		DLPStore:    dlp,
		PolicyStore: storage.NewMemPolicyStore(policy),
		Inspector:   inspector.New(),
		DLPPolicy:   policy,
		JevBaseURL:  upstream.URL,
		AdminAPIKey: "admin-test",
		Logger:      slog.Default(),
	})
	return h, dlp, &sawBody, &sawPath
}

func jevPost(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func redactPolicyWithPII() inspector.Policy {
	return inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionRedact, Custom: inspector.ActionOff}
}

func TestJevProxiesAndRedacts(t *testing.T) {
	h, dlp, sawBody, sawPath := jevRouter(t, redactPolicyWithPII())

	rec := jevPost(h, "/api/v1/decisions/tool-guard", `{"tool":"issue_customer_refund",
		"action":"Refund after a duplicate charge","arguments_summary":["contact=alice@example.com"]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if *sawPath != "/api/v1/decisions/tool-guard" {
		t.Errorf("upstream path = %q, want the preset path unchanged", *sawPath)
	}
	if strings.Contains(*sawBody, "alice@example.com") {
		t.Errorf("the email reached the Jev API: %s", *sawBody)
	}
	if !strings.Contains(*sawBody, "[REDACTED:email]") {
		t.Errorf("body was not redacted: %s", *sawBody)
	}
	// The decision must come back untouched.
	var out struct {
		Code int `json:"code"`
		Data struct {
			Decision   string  `json:"decision"`
			Confidence float64 `json:"confidence"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Code != 0 || out.Data.Decision != "confirm" || out.Data.Confidence != 0.86 {
		t.Errorf("decision envelope altered: %s", rec.Body.String())
	}

	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Provider != "jev" {
		t.Fatalf("events = %+v, want one attributed to jev", events)
	}
}

// A blocked request must never reach the decision API, and the client should
// get an error in the shape it already parses.
func TestJevBlocksInItsOwnEnvelope(t *testing.T) {
	h, _, sawBody, _ := jevRouter(t, redactPolicyWithPII())

	rec := jevPost(h, "/api/v1/decisions", `{"state":{"note":"key AKIAIOSFODNN7EXAMPLE"},
		"questions":{"action":{"type":"noul","instructions":"Proceed?"}}}`)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if *sawBody != "" {
		t.Errorf("upstream was called with a blocked body: %s", *sawBody)
	}
	var out struct {
		Code     int    `json:"code"`
		Message  string `json:"message"`
		Data     any    `json:"data"`
		Aperture struct {
			BlockedBy string   `json:"blocked_by"`
			Rules     []string `json:"rules"`
		} `json:"aperture"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("error body is not JSON: %s", rec.Body.String())
	}
	if out.Code == 0 || out.Data != nil {
		t.Errorf("error must use Jev's envelope (code != 0, data null): %s", rec.Body.String())
	}
	if out.Aperture.BlockedBy != "dlp" || len(out.Aperture.Rules) != 1 {
		t.Errorf("aperture detail missing: %s", rec.Body.String())
	}
}

func TestJevResponseScanning(t *testing.T) {
	policy := redactPolicyWithPII()
	policy.ScanResponses = true
	h, dlp, _, _ := jevRouter(t, policy)

	// The fake API's guidance is clean, so nothing should change; what matters
	// is that the response path runs and records nothing spurious.
	rec := jevPost(h, "/api/v1/decisions/route", `{"task":"Resolve a disputed charge"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	events, _ := dlp.List(context.Background(), storage.DLPFilter{Direction: storage.DirectionResponse})
	if len(events) != 0 {
		t.Errorf("clean response produced events: %+v", events)
	}
}

func TestJevRejectsUnknownPreset(t *testing.T) {
	h, _, sawBody, _ := jevRouter(t, redactPolicyWithPII())
	rec := jevPost(h, "/api/v1/decisions/not-a-preset", `{"task":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if *sawBody != "" {
		t.Error("an unknown path was forwarded upstream — the gateway must not be an open proxy")
	}
}

func TestJevRequiresApertureKey(t *testing.T) {
	h, _, _, _ := jevRouter(t, redactPolicyWithPII())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decisions/route", strings.NewReader(`{"task":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestJevRefusesOversizedBody(t *testing.T) {
	h, _, sawBody, _ := jevRouter(t, redactPolicyWithPII())
	big := `{"task":"` + strings.Repeat("x", 33*1024) + `"}`
	rec := jevPost(h, "/api/v1/decisions/route", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if *sawBody != "" {
		t.Error("an oversized body was forwarded upstream")
	}
}

// Without a Jev key the gateway must say so plainly instead of calling
// upstream and relaying a confusing 401.
func TestJevWithoutProviderKey(t *testing.T) {
	ks := config.NewRuntimeStore("ap-test").KeyStore()
	// A working key for another provider, so the aperture key resolves and the
	// only thing missing is the Jev credential.
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-other"}); err != nil {
		t.Fatal(err)
	}
	h := Routes(Options{
		KeyStore:    ks,
		DLPStore:    storage.NewMemDLPStore(10),
		PolicyStore: storage.NewMemPolicyStore(redactPolicyWithPII()),
		Inspector:   inspector.New(),
		DLPPolicy:   redactPolicyWithPII(),
		AdminAPIKey: "admin-test",
		Logger:      slog.Default(),
	})
	rec := jevPost(h, "/api/v1/decisions/route", `{"task":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "JEV_API_KEY") {
		t.Errorf("the error should say how to fix it: %s", rec.Body.String())
	}
}
