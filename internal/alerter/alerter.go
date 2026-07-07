// Package alerter delivers DLP events to an outbound webhook (generic JSON,
// Slack, or Telegram). Delivery is asynchronous and never blocks the request
// path; a per-key+rule debounce window absorbs storms from looping agents.
package alerter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/danilovid/aperture/internal/storage"
)

// Format selects how the event is rendered for the destination.
type Format string

const (
	FormatJSON     Format = "json"     // generic: POST the event object
	FormatSlack    Format = "slack"    // Slack incoming webhook: {"text": ...}
	FormatTelegram Format = "telegram" // Telegram sendMessage: {"chat_id", "text"}
)

// ValidFormat reports whether s is a supported format.
func ValidFormat(s string) bool {
	switch Format(s) {
	case FormatJSON, FormatSlack, FormatTelegram:
		return true
	}
	return false
}

// Config controls webhook delivery. An empty URL disables alerting.
type Config struct {
	URL    string `json:"url"`
	Format Format `json:"format"`
	// Actions that trigger an alert, e.g. ["blocked"]. Empty means blocked only.
	Actions []string `json:"actions"`
	// ChatID is required for the Telegram format.
	ChatID string `json:"chat_id,omitempty"`
	// DebounceSeconds suppresses repeat alerts for the same key+rule. 0 → 60s.
	DebounceSeconds int `json:"debounce_seconds"`
}

func (c Config) enabled() bool { return c.URL != "" }

func (c Config) triggersOn(action string) bool {
	if len(c.Actions) == 0 {
		return action == "blocked"
	}
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func (c Config) debounce() time.Duration {
	if c.DebounceSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.DebounceSeconds) * time.Second
}

// Alerter dispatches events to a webhook. Safe for concurrent use.
type Alerter struct {
	mu       sync.RWMutex
	cfg      Config
	lastSent map[string]time.Time

	client *http.Client
	logger *slog.Logger
	ch     chan storage.DLPEvent
	now    func() time.Time // injectable for tests
}

// New creates an Alerter with the given initial config.
func New(cfg Config, logger *slog.Logger) *Alerter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Alerter{
		cfg:      cfg,
		lastSent: make(map[string]time.Time),
		client:   &http.Client{Timeout: 10 * time.Second},
		logger:   logger,
		ch:       make(chan storage.DLPEvent, 256),
		now:      time.Now,
	}
}

// Config returns the current config with the URL masked for display.
func (a *Alerter) Config() Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c := a.cfg
	c.URL = maskURL(c.URL)
	return c
}

// SetConfig replaces the delivery config at runtime.
func (a *Alerter) SetConfig(cfg Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = cfg
	a.lastSent = make(map[string]time.Time) // reset debounce on reconfigure
}

// Run consumes queued events until ctx is cancelled. Call once in a goroutine.
func (a *Alerter) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-a.ch:
			a.deliver(ctx, e)
		}
	}
}

// Notify enqueues an event for delivery. It never blocks: if the buffer is
// full the event is dropped (delivery is best-effort, the DLP log is the
// source of truth).
func (a *Alerter) Notify(e storage.DLPEvent) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if !cfg.enabled() || !cfg.triggersOn(e.Action) {
		return
	}
	select {
	case a.ch <- e:
	default:
		a.logger.Warn("alert dropped: queue full", "rule", e.Rule)
	}
}

// shouldSend applies the debounce window for the event's key+rule.
func (a *Alerter) shouldSend(e storage.DLPEvent, window time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := e.KeyID + ":" + e.Rule
	now := a.now()
	if last, ok := a.lastSent[k]; ok && now.Sub(last) < window {
		return false
	}
	a.lastSent[k] = now
	return true
}

func (a *Alerter) deliver(ctx context.Context, e storage.DLPEvent) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if !cfg.enabled() || !cfg.triggersOn(e.Action) {
		return
	}
	if !a.shouldSend(e, cfg.debounce()) {
		return
	}
	if err := a.post(ctx, cfg, e); err != nil {
		a.logger.Error("alert delivery failed", "err", err, "rule", e.Rule)
	}
}

func (a *Alerter) post(ctx context.Context, cfg Config, e storage.DLPEvent) error {
	body, err := renderPayload(cfg, e)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// SendTest delivers a synthetic event immediately, bypassing debounce, so the
// admin UI can verify the destination. Returns any delivery error.
func (a *Alerter) SendTest(ctx context.Context) error {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if !cfg.enabled() {
		return fmt.Errorf("no webhook URL configured")
	}
	return a.post(ctx, cfg, storage.DLPEvent{
		Ts:           time.Now(),
		KeyID:        "test",
		Model:        "gpt-4o-mini",
		Provider:     "openai",
		Rule:         "aws-access-key",
		Group:        "secrets",
		Action:       "blocked",
		MaskedSample: "AKIA****************",
	})
}

func renderPayload(cfg Config, e storage.DLPEvent) ([]byte, error) {
	switch cfg.Format {
	case FormatSlack:
		return json.Marshal(map[string]string{"text": textMessage(e)})
	case FormatTelegram:
		return json.Marshal(map[string]string{"chat_id": cfg.ChatID, "text": textMessage(e)})
	default: // FormatJSON
		return json.Marshal(map[string]any{
			"source":        "aperture",
			"event":         "dlp." + e.Action,
			"rule":          e.Rule,
			"group":         e.Group,
			"action":        e.Action,
			"key_id":        e.KeyID,
			"model":         e.Model,
			"provider":      e.Provider,
			"masked_sample": e.MaskedSample,
			"ts":            e.Ts.Format(time.RFC3339),
		})
	}
}

func textMessage(e storage.DLPEvent) string {
	verb := map[string]string{"blocked": "🚫 Blocked", "redacted": "✂️ Redacted", "alerted": "⚠️ Alert"}[e.Action]
	if verb == "" {
		verb = e.Action
	}
	return fmt.Sprintf("%s — Aperture DLP\nRule: %s (%s)\nKey: %s · Model: %s\nSample: %s",
		verb, e.Rule, e.Group, e.KeyID, e.Model, e.MaskedSample)
}

// maskURL hides the path/query of a webhook so secrets in it aren't echoed back.
func maskURL(u string) string {
	if u == "" {
		return ""
	}
	for i := 0; i < len(u); i++ {
		if i > 8 && (u[i] == '/' || u[i] == '?') {
			return u[:i] + "/****"
		}
	}
	return u
}
