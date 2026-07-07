package alerter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/danilovid/aperture/internal/storage"
)

type capture struct {
	mu     sync.Mutex
	bodies []string
}

func (c *capture) server(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(b))
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func blockEvent() storage.DLPEvent {
	return storage.DLPEvent{KeyID: "ci", Rule: "aws-access-key", Group: "secrets", Action: "blocked", MaskedSample: "AKIA****"}
}

func TestDeliverAndFilter(t *testing.T) {
	cap := &capture{}
	srv := cap.server(t)
	a := New(Config{URL: srv.URL, Format: FormatJSON, Actions: []string{"blocked"}}, nil)

	// Redacted event must not fire (only blocked configured).
	a.deliver(context.Background(), storage.DLPEvent{KeyID: "ci", Rule: "email", Action: "redacted"})
	if cap.count() != 0 {
		t.Fatalf("redacted event fired despite action filter")
	}

	// Notify() also filters before enqueue.
	a.Notify(storage.DLPEvent{KeyID: "ci", Rule: "email", Action: "redacted"})
	if len(a.ch) != 0 {
		t.Fatalf("redacted event enqueued")
	}

	a.deliver(context.Background(), blockEvent())
	if cap.count() != 1 {
		t.Fatalf("blocked event not delivered: %d", cap.count())
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(cap.bodies[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != "dlp.blocked" || payload["rule"] != "aws-access-key" {
		t.Errorf("unexpected payload: %v", payload)
	}
}

func TestDebounce(t *testing.T) {
	cap := &capture{}
	srv := cap.server(t)
	a := New(Config{URL: srv.URL, Format: FormatJSON, DebounceSeconds: 60}, nil)

	base := time.Now()
	a.now = func() time.Time { return base }

	a.deliver(context.Background(), blockEvent())
	a.deliver(context.Background(), blockEvent()) // within window → suppressed
	if cap.count() != 1 {
		t.Fatalf("debounce failed: %d deliveries", cap.count())
	}

	// A different rule for the same key is independent.
	e2 := blockEvent()
	e2.Rule = "github-token"
	a.deliver(context.Background(), e2)
	if cap.count() != 2 {
		t.Fatalf("different rule suppressed: %d", cap.count())
	}

	// After the window, the first rule fires again.
	a.now = func() time.Time { return base.Add(61 * time.Second) }
	a.deliver(context.Background(), blockEvent())
	if cap.count() != 3 {
		t.Fatalf("window expiry did not re-enable: %d", cap.count())
	}
}

func TestSlackAndTelegramFormats(t *testing.T) {
	cap := &capture{}
	srv := cap.server(t)

	a := New(Config{URL: srv.URL, Format: FormatSlack}, nil)
	if err := a.post(context.Background(), a.cfg, blockEvent()); err != nil {
		t.Fatal(err)
	}
	var slack map[string]string
	json.Unmarshal([]byte(cap.bodies[0]), &slack)
	if slack["text"] == "" {
		t.Errorf("slack payload missing text: %v", slack)
	}

	a.SetConfig(Config{URL: srv.URL, Format: FormatTelegram, ChatID: "12345"})
	if err := a.post(context.Background(), a.cfg, blockEvent()); err != nil {
		t.Fatal(err)
	}
	var tg map[string]string
	json.Unmarshal([]byte(cap.bodies[1]), &tg)
	if tg["chat_id"] != "12345" || tg["text"] == "" {
		t.Errorf("telegram payload wrong: %v", tg)
	}
}

func TestConfigMasksURL(t *testing.T) {
	a := New(Config{URL: "https://hooks.slack.com/services/T00/B00/XXXSECRET", Format: FormatSlack}, nil)
	if got := a.Config().URL; got != "https://hooks.slack.com/****" {
		t.Errorf("URL not masked: %q", got)
	}
}
