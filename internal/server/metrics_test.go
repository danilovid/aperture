package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/metrics"
	"github.com/danilovid/aperture/internal/storage"
)

// metricsRouter wires a gateway with a metrics registry and an upstream that
// reports usage, so the exposition reflects real traffic.
func metricsRouter(t *testing.T, reg *metrics.Registry) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	t.Cleanup(upstream.Close)

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-upstream"}); err != nil {
		t.Fatal(err)
	}
	return Routes(Options{
		KeyStore:      ks,
		DLPStore:      storage.NewMemDLPStore(50),
		PolicyStore:   storage.NewMemPolicyStore(inspector.DefaultPolicy()),
		LimitStore:    storage.NewMemLimitStore(limits.Limits{}),
		Tracker:       limits.NewTracker(nil),
		Metrics:       reg,
		Inspector:     inspector.New(),
		DLPPolicy:     inspector.DefaultPolicy(),
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want the Prometheus text format", ct)
	}
	return rec.Body.String()
}

func chatWith(h http.Handler, prompt string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"`+prompt+`"}]}`))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMetricsReflectTraffic(t *testing.T) {
	reg := metrics.New()
	h := metricsRouter(t, reg)

	if got := chatWith(h, "hello").Code; got != http.StatusOK {
		t.Fatalf("clean request = %d, want 200", got)
	}
	// A secret makes the gateway block, which should show up as a DLP event.
	if got := chatWith(h, "key AKIAIOSFODNN7EXAMPLE").Code; got != http.StatusForbidden {
		t.Fatalf("blocked request = %d, want 403", got)
	}

	body := scrape(t, h)
	for _, want := range []string{
		`aperture_http_requests_total{path="/v1/chat/completions",status="200"} 1`,
		`aperture_http_requests_total{path="/v1/chat/completions",status="403"} 1`,
		`aperture_llm_requests_total{provider="openai",model="gpt-4o",status="200"} 1`,
		`aperture_tokens_total{direction="prompt"} 10`,
		`aperture_tokens_total{direction="completion"} 5`,
		`aperture_dlp_events_total{rule="aws-access-key",action="blocked"} 1`,
		"aperture_http_request_duration_seconds_count",
		"aperture_cost_usd_total{provider=\"openai\"}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing from /metrics:\n%s\n--- got ---\n%s", want, body)
		}
	}
}

// Paths carry ids; labels must not, or the series count grows without bound.
func TestMetricsPathLabelIsTheRoutePattern(t *testing.T) {
	reg := metrics.New()
	h := metricsRouter(t, reg)

	for _, id := range []string{"key-a", "key-b", "key-c"} {
		req := httptest.NewRequest(http.MethodDelete, "/admin/limits/keys/"+id, nil)
		req.Header.Set("Authorization", "Bearer admin-test")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	body := scrape(t, h)
	if !strings.Contains(body, `path="/admin/limits/keys/{id}"`) {
		t.Errorf("path label is not the route pattern:\n%s", body)
	}
	for _, id := range []string{"key-a", "key-b", "key-c"} {
		if strings.Contains(body, "keys/"+id) {
			t.Errorf("key id %q leaked into a metric label:\n%s", id, body)
		}
	}
}

// The endpoint is unauthenticated, so it must never carry key material.
func TestMetricsExposeNoSecrets(t *testing.T) {
	reg := metrics.New()
	h := metricsRouter(t, reg)
	chatWith(h, "hello")

	body := scrape(t, h)
	for _, secret := range []string{"ap-test", "admin-test", "sk-upstream"} {
		if strings.Contains(body, secret) {
			t.Errorf("%q exposed on /metrics:\n%s", secret, body)
		}
	}
}

func TestMetricsDisabledWithoutRegistry(t *testing.T) {
	h := metricsRouter(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /metrics without a registry = %d, want 503", rec.Code)
	}
}
