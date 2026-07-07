package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

func TestAlertFiresOnBlock(t *testing.T) {
	// Webhook capture.
	var mu sync.Mutex
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
	}))
	defer hook.Close()

	// Fake upstream provider.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer upstream.Close()

	ks := config.NewRuntimeStore("ap-test").KeyStore()
	ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-x"})

	al := alerter.New(alerter.Config{URL: hook.URL, Format: alerter.FormatJSON, Actions: []string{"blocked"}}, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go al.Run(ctx)

	h := Routes(Options{
		KeyStore:      ks,
		DLPStore:      storage.NewMemDLPStore(100),
		Inspector:     inspector.New(),
		DLPPolicy:     inspector.DefaultPolicy(),
		Alerter:       al,
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "admin-test",
		Logger:        slog.Default(),
	})

	// A blocked secret should trigger a webhook; a redacted email should not.
	post := func(content string) {
		body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"` + content + `"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer ap-test")
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	post("deploy AKIAIOSFODNN7EXAMPLE")
	post("mail ivan@corp.io")

	// Delivery is async; wait briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 alert (blocked only), got %d: %v", len(got), got)
	}
	var payload map[string]any
	json.Unmarshal([]byte(got[0]), &payload)
	if payload["action"] != "blocked" || payload["rule"] != "aws-access-key" {
		t.Errorf("unexpected alert payload: %v", payload)
	}
}

func TestAlertsAdminAPI(t *testing.T) {
	al := alerter.New(alerter.Config{}, slog.Default())
	h := Routes(Options{
		KeyStore:    config.NewRuntimeStore("ap-test").KeyStore(),
		Inspector:   inspector.New(),
		Alerter:     al,
		AdminAPIKey: "admin-test",
		Logger:      slog.Default(),
	})

	// PUT config.
	req := httptest.NewRequest(http.MethodPut, "/admin/alerts", strings.NewReader(`{"url":"https://hooks.slack.com/services/T/B/XYZSECRET","format":"slack"}`))
	req.Header.Set("Authorization", "Bearer admin-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", rec.Code, rec.Body.String())
	}

	// GET returns masked URL.
	req = httptest.NewRequest(http.MethodGet, "/admin/alerts", nil)
	req.Header.Set("Authorization", "Bearer admin-test")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var cfg alerter.Config
	json.Unmarshal(rec.Body.Bytes(), &cfg)
	if strings.Contains(cfg.URL, "XYZSECRET") {
		t.Errorf("GET leaked webhook URL: %s", cfg.URL)
	}

	// Telegram without chat_id is rejected.
	req = httptest.NewRequest(http.MethodPut, "/admin/alerts", strings.NewReader(`{"url":"https://api.telegram.org/botX/sendMessage","format":"telegram"}`))
	req.Header.Set("Authorization", "Bearer admin-test")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("telegram without chat_id accepted: %d", rec.Code)
	}
}
