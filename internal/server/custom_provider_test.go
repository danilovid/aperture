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
	"github.com/danilovid/aperture/internal/storage"
)

func TestResolveLLMPrefersCustomPrefix(t *testing.T) {
	h := &Handlers{CustomProviders: []config.CustomProvider{
		{Name: "deepseek", BaseURL: "https://api.deepseek.com/v1", Prefixes: []string{"deepseek"}},
		{Name: "ollama", BaseURL: "http://localhost:11434/v1", Prefixes: []string{"qwen"}},
	}}
	cases := map[string]string{
		"deepseek-chat":              "deepseek",
		"DeepSeek-Reasoner":          "deepseek", // case-insensitive
		"qwen2.5-coder":              "ollama",
		"gpt-4o-mini":                "openai", // falls back to built-in
		"claude-3-5-sonnet-20241022": "anthropic",
	}
	for model, want := range cases {
		if got := h.resolveLLM(model); got != want {
			t.Errorf("resolveLLM(%q) = %q, want %q", model, got, want)
		}
	}
}

// A custom provider request is proxied to its base URL via the OpenAI-compatible
// client, and DLP events record the custom provider name.
func TestCustomProviderProxiesAndScans(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	// custom provider key stored under its name
	ks.SetProviderKeys(context.Background(), map[string]string{"deepseek": "sk-ds"})
	dlp := storage.NewMemDLPStore(10)

	h := Routes(Options{
		KeyStore:    ks,
		DLPStore:    dlp,
		Inspector:   inspector.New(),
		DLPPolicy:   inspector.DefaultPolicy(),
		AdminAPIKey: "admin-test",
		CustomProviders: []config.CustomProvider{
			{Name: "deepseek", BaseURL: upstream.URL, Prefixes: []string{"deepseek"}},
		},
		Logger: slog.Default(),
	})

	// clean request → proxied to <base>/chat/completions
	body := `{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/chat/completions" {
		t.Errorf("upstream path = %q, want /chat/completions", gotPath)
	}

	// secret → blocked, event attributed to the custom provider
	body = `{"model":"deepseek-chat","messages":[{"role":"user","content":"AKIAIOSFODNN7EXAMPLE"}]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer ap-test")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("secret not blocked: %d", rec.Code)
	}
	events, _ := dlp.List(context.Background(), storage.DLPFilter{})
	if len(events) != 1 || events[0].Provider != "deepseek" {
		t.Errorf("event provider = %+v, want deepseek", events)
	}
}
