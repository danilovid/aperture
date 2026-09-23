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
	"strings"
	"sync"
	"time"

	"github.com/danilovid/mutegate/internal/storage"
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

// SettingsStore is where organizations' own alert settings live.
// storage.AlertStore satisfies it; the settings are this package's JSON.
type SettingsStore interface {
	GetAlertSettings(ctx context.Context, orgID string) ([]byte, bool, error)
	SetAlertSettings(ctx context.Context, orgID string, raw []byte) error
}

// Alerter dispatches events to each organization's webhook. Safe for
// concurrent use.
//
// Every organization has its own destination, or none. The environment's
// webhook (DLP_WEBHOOK_URL) belongs to the default organization — the one a
// single-tenant installation works in — and to no other: an operator's Slack
// channel is not a place for another tenant's incidents, masked or not.
type Alerter struct {
	mu         sync.RWMutex
	defaultOrg string
	envCfg     Config
	store      SettingsStore     // nil: settings live in memory for the process
	mem        map[string]Config // settings saved without a store
	cache      map[string]cachedConfig
	lastSent   map[string]time.Time

	client *http.Client
	logger *slog.Logger
	ch     chan storage.DLPEvent
	now    func() time.Time // injectable for tests
}

type cachedConfig struct {
	cfg     Config
	expires time.Time
}

// settingsTTL is how long an organization's settings are trusted from
// memory. Saving through this process drops them at once; only another
// instance can lag, by at most this long.
const settingsTTL = 10 * time.Second

// New creates an Alerter whose environment config belongs to the default
// organization.
func New(cfg Config, logger *slog.Logger) *Alerter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Alerter{
		defaultOrg: storage.DefaultOrgID,
		envCfg:     cfg,
		mem:        map[string]Config{},
		cache:      map[string]cachedConfig{},
		lastSent:   make(map[string]time.Time),
		client:     &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
		ch:         make(chan storage.DLPEvent, 256),
		now:        time.Now,
	}
}

// WithStore keeps organizations' settings in a store rather than in memory.
func (a *Alerter) WithStore(s SettingsStore) *Alerter {
	a.store = s
	return a
}

func (a *Alerter) org(orgID string) string {
	// A blank organization is the single-tenant one, as everywhere else.
	if orgID == "" {
		return a.defaultOrg
	}
	return orgID
}

// fallback is what an organization gets with nothing of its own.
func (a *Alerter) fallback(orgID string) Config {
	if orgID == a.defaultOrg {
		return a.envCfg
	}
	return Config{}
}

// resolve returns an organization's settings, reading the store at most once
// per settingsTTL.
func (a *Alerter) resolve(ctx context.Context, orgID string) (Config, error) {
	orgID = a.org(orgID)
	a.mu.RLock()
	if a.store == nil {
		cfg, ok := a.mem[orgID]
		a.mu.RUnlock()
		if !ok {
			cfg = a.fallback(orgID)
		}
		return cfg, nil
	}
	if c, ok := a.cache[orgID]; ok && a.now().Before(c.expires) {
		a.mu.RUnlock()
		return c.cfg, nil
	}
	a.mu.RUnlock()

	raw, ok, err := a.store.GetAlertSettings(ctx, orgID)
	if err != nil {
		return Config{}, err
	}
	cfg := a.fallback(orgID)
	if ok {
		cfg = Config{}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("unreadable alert settings: %w", err)
		}
	}
	a.mu.Lock()
	a.cache[orgID] = cachedConfig{cfg: cfg, expires: a.now().Add(settingsTTL)}
	a.mu.Unlock()
	return cfg, nil
}

// ConfigFor returns an organization's settings with the URL masked for
// display.
func (a *Alerter) ConfigFor(ctx context.Context, orgID string) (Config, error) {
	cfg, err := a.resolve(ctx, orgID)
	cfg.URL = maskURL(cfg.URL)
	return cfg, err
}

// SetConfigFor replaces an organization's settings.
func (a *Alerter) SetConfigFor(ctx context.Context, orgID string, cfg Config) error {
	orgID = a.org(orgID)
	current, err := a.resolve(ctx, orgID)
	if err != nil {
		return err
	}
	// ConfigFor hands out a masked URL, so a read-modify-write round trip
	// (the console's save button, or curl piping GET into PUT) sends the
	// mask back. Treat that as "leave the URL alone" instead of destroying
	// the webhook.
	if cfg.URL != "" && cfg.URL == maskURL(current.URL) {
		cfg.URL = current.URL
	}
	if a.store != nil {
		raw, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := a.store.SetAlertSettings(ctx, orgID, raw); err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		a.mem[orgID] = cfg
	}
	delete(a.cache, orgID)
	// A new destination starts with a clean debounce — this organization's.
	for k := range a.lastSent {
		if strings.HasPrefix(k, orgID+":") {
			delete(a.lastSent, k)
		}
	}
	return nil
}

// Config, SetConfig and SendTest act on the default organization: the
// single-tenant installation's view of the alerter.
func (a *Alerter) Config() Config {
	cfg, _ := a.ConfigFor(context.Background(), a.defaultOrg)
	return cfg
}

func (a *Alerter) SetConfig(cfg Config) {
	if err := a.SetConfigFor(context.Background(), a.defaultOrg, cfg); err != nil {
		a.logger.Error("saving alert settings failed", "err", err)
	}
}

func (a *Alerter) SendTest(ctx context.Context) error {
	return a.SendTestFor(ctx, a.defaultOrg)
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

// Notify enqueues an event for delivery. It never blocks and never touches a
// database: an organization known not to want this alert is filtered here,
// anything else is decided off the request path. If the buffer is full the
// event is dropped — delivery is best-effort, the DLP log is the source of
// truth.
func (a *Alerter) Notify(e storage.DLPEvent) {
	orgID := a.org(e.OrgID)
	a.mu.RLock()
	var cfg Config
	known := true
	switch {
	case a.store == nil:
		var ok bool
		if cfg, ok = a.mem[orgID]; !ok {
			cfg = a.fallback(orgID)
		}
	default:
		c, ok := a.cache[orgID]
		cfg, known = c.cfg, ok && a.now().Before(c.expires)
	}
	a.mu.RUnlock()
	if known && (!cfg.enabled() || !cfg.triggersOn(e.Action)) {
		return
	}
	select {
	case a.ch <- e:
	default:
		a.logger.Warn("alert dropped: queue full", "rule", e.Rule)
	}
}

// shouldSend applies the debounce window for the event's organization, key
// and rule.
func (a *Alerter) shouldSend(e storage.DLPEvent, window time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := a.org(e.OrgID) + ":" + e.KeyID + ":" + e.Rule
	now := a.now()
	if last, ok := a.lastSent[k]; ok && now.Sub(last) < window {
		return false
	}
	a.lastSent[k] = now
	return true
}

func (a *Alerter) deliver(ctx context.Context, e storage.DLPEvent) {
	cfg, err := a.resolve(ctx, e.OrgID)
	if err != nil {
		a.logger.Error("alert settings unavailable", "err", err, "org", e.OrgID)
		return
	}
	if !cfg.enabled() || !cfg.triggersOn(e.Action) {
		return
	}
	if !a.shouldSend(e, cfg.debounce()) {
		return
	}
	if err := a.post(ctx, cfg, e); err != nil {
		a.logger.Error("alert delivery failed", "err", err, "rule", e.Rule, "org", e.OrgID)
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

// SendTestFor delivers a synthetic event to an organization's webhook now,
// bypassing debounce, so the console can verify the destination.
func (a *Alerter) SendTestFor(ctx context.Context, orgID string) error {
	cfg, err := a.resolve(ctx, orgID)
	if err != nil {
		return err
	}
	if !cfg.enabled() {
		return fmt.Errorf("no webhook URL configured")
	}
	return a.post(ctx, cfg, storage.DLPEvent{
		OrgID:        a.org(orgID),
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
			"source":        "mutegate",
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
	return fmt.Sprintf("%s — Mutegate DLP\nRule: %s (%s)\nKey: %s · Model: %s\nSample: %s",
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
