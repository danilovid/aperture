package alerter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	current := func() Config {
		cfg, _ := a.resolve(context.Background(), storage.DefaultOrgID)
		return cfg
	}
	if err := a.post(context.Background(), current(), blockEvent()); err != nil {
		t.Fatal(err)
	}
	var slack map[string]string
	json.Unmarshal([]byte(cap.bodies[0]), &slack)
	if slack["text"] == "" {
		t.Errorf("slack payload missing text: %v", slack)
	}

	a.SetConfig(Config{URL: srv.URL, Format: FormatTelegram, ChatID: "12345"})
	if err := a.post(context.Background(), current(), blockEvent()); err != nil {
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

// Config() masks the URL, so saving an unchanged form must not overwrite the
// real webhook with its own mask (which would silently break delivery).
func TestSetConfigKeepsURLWhenMaskIsEchoedBack(t *testing.T) {
	const real = "https://hooks.slack.com/services/T000/B000/secret"
	a := New(Config{URL: real, Format: FormatSlack}, nil)

	round := a.Config() // what the console loaded
	round.Actions = []string{"blocked", "redacted"}
	a.SetConfig(round) // and what it saves back

	stored := func() Config {
		cfg, _ := a.resolve(context.Background(), storage.DefaultOrgID)
		return cfg
	}
	if got := stored().URL; got != real {
		t.Errorf("URL = %q, want the original %q", got, real)
	}
	if len(stored().Actions) != 2 {
		t.Errorf("actions not applied: %v", stored().Actions)
	}

	// An explicitly emptied URL still disables alerting.
	a.SetConfig(Config{URL: "", Format: FormatJSON})
	if stored().URL != "" {
		t.Errorf("URL = %q, want it cleared", stored().URL)
	}
}

// The environment's webhook is the default organization's. Another
// organization's incidents must never reach it — that would put one tenant's
// rule names, keys and agents in the operator's channel.
func TestTheEnvironmentWebhookIsTheDefaultOrganizations(t *testing.T) {
	cap := &capture{}
	srv := cap.server(t)
	a := New(Config{URL: srv.URL, Format: FormatJSON}, nil)

	other := blockEvent()
	other.OrgID = "22222222-2222-2222-2222-222222222222"
	a.deliver(context.Background(), other)
	if cap.count() != 0 {
		t.Fatal("another organization's incident went to the environment's webhook")
	}
	a.Notify(other)
	if len(a.ch) != 0 {
		t.Error("another organization's incident was even queued for it")
	}

	mine := blockEvent()
	mine.OrgID = storage.DefaultOrgID
	a.deliver(context.Background(), mine)
	if cap.count() != 1 {
		t.Errorf("the default organization's incident was not delivered: %d", cap.count())
	}
}

// Each organization's settings are its own, kept in the store, and its
// incidents go to its own webhook only.
func TestEachOrganizationHasItsOwnWebhook(t *testing.T) {
	envHook, orgHook := &capture{}, &capture{}
	envSrv, orgSrv := envHook.server(t), orgHook.server(t)
	store := storage.NewMemAlertStore()
	a := New(Config{URL: envSrv.URL, Format: FormatJSON}, nil).WithStore(store)
	ctx := context.Background()
	const orgB = "22222222-2222-2222-2222-222222222222"

	if err := a.SetConfigFor(ctx, orgB, Config{URL: orgSrv.URL + "/hooks/b-secret-token", Format: FormatJSON}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.GetAlertSettings(ctx, orgB); !ok {
		t.Fatal("B's settings were not stored")
	}

	b := blockEvent()
	b.OrgID = orgB
	a.deliver(ctx, b)
	if orgHook.count() != 1 || envHook.count() != 0 {
		t.Errorf("B's incident: its webhook got %d, the environment's got %d", orgHook.count(), envHook.count())
	}

	d := blockEvent()
	d.OrgID = storage.DefaultOrgID
	a.deliver(ctx, d)
	if envHook.count() != 1 || orgHook.count() != 1 {
		t.Errorf("the default organization's incident went to the wrong place: env %d, B %d", envHook.count(), orgHook.count())
	}

	// B reads back its own settings, masked; the default organization reads
	// the environment's.
	got, _ := a.ConfigFor(ctx, orgB)
	if strings.Contains(got.URL, "b-secret-token") {
		t.Errorf("B's webhook token came back: %s", got.URL)
	}
	if env, _ := a.ConfigFor(ctx, storage.DefaultOrgID); env.URL != maskURL(envSrv.URL) {
		t.Errorf("the default organization sees %q, want the environment's webhook", env.URL)
	}
}

// One organization's alert storm must not debounce another's first alert.
func TestDebounceIsPerOrganization(t *testing.T) {
	cap := &capture{}
	srv := cap.server(t)
	a := New(Config{}, nil)
	ctx := context.Background()
	for _, org := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"} {
		a.SetConfigFor(ctx, org, Config{URL: srv.URL, Format: FormatJSON, DebounceSeconds: 600})
		e := blockEvent() // same key id and rule in both
		e.OrgID = org
		a.deliver(ctx, e)
	}
	if cap.count() != 2 {
		t.Errorf("delivered %d, want one per organization", cap.count())
	}
}
