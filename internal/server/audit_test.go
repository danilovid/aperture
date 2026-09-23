package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/storage"
)

// memKeys is a key store that can create and delete, per organization — the
// in-memory runtime store cannot, and the journal needs keys to talk about.
type memKeys struct {
	mu   sync.Mutex
	next int
	keys []storage.Key
}

func (s *memKeys) GetByApertureKey(_ context.Context, token string) (*storage.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.keys {
		if k.ApertureKey == token {
			return &k, nil
		}
	}
	return nil, storage.ErrKeyNotFound
}

func (s *memKeys) Create(_ context.Context, orgID, token, name string, providers map[string]string) (*storage.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	k := storage.Key{ID: "key-" + strconv.Itoa(s.next), OrgID: orgID, ApertureKey: token, Name: name, Providers: providers}
	s.keys = append(s.keys, k)
	return &k, nil
}

func (s *memKeys) List(_ context.Context, orgID string) ([]storage.Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.Key
	for _, k := range s.keys {
		if k.OrgID == orgID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *memKeys) Delete(_ context.Context, orgID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, k := range s.keys {
		if k.ID == id && k.OrgID == orgID {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			return nil
		}
	}
	return storage.ErrKeyNotFound
}

func (s *memKeys) SetProviderKeys(context.Context, string, map[string]string) error { return nil }
func (s *memKeys) GetProviderKeys(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (s *memKeys) ClearProviderKeys(context.Context, string) error { return nil }

// auditRouter is a gateway with everything a person can change, and a journal.
func auditRouter(t *testing.T) (http.Handler, *storage.MemAuditStore) {
	t.Helper()
	journal := storage.NewMemAuditStore()
	h := Routes(Options{
		KeyStore:      &memKeys{},
		AccountStore:  storage.NewMemAccountStore(),
		AuditStore:    journal,
		ProviderStore: storage.NewMemProviderStore(),
		PolicyStore:   storage.NewMemPolicyStore(inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionRedact}),
		LimitStore:    storage.NewMemLimitStore(limits.Limits{}),
		Alerter:       alerter.New(alerter.Config{}, nil).WithStore(storage.NewMemAlertStore()),
		AdminAPIKey:   "instance-admin",
		Logger:        slog.Default(),
	})
	return h, journal
}

// journal reads the audit log the way the console does.
func journal(t *testing.T, c *client, query string) []storage.AuditEntry {
	t.Helper()
	rec := c.do(http.MethodGet, "/admin/audit"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read the audit log = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Entries []storage.AuditEntry `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("audit log response: %s", rec.Body.String())
	}
	return out.Entries
}

// find returns the newest entry with the action, or fails.
func find(t *testing.T, entries []storage.AuditEntry, action string) storage.AuditEntry {
	t.Helper()
	for _, e := range entries {
		if e.Action == action {
			return e
		}
	}
	t.Fatalf("no %s entry in the journal: %+v", action, entries)
	return storage.AuditEntry{}
}

func changes(e storage.AuditEntry) string {
	list, _ := e.Meta["changes"].([]any)
	parts := make([]string, 0, len(list))
	for _, c := range list {
		parts = append(parts, c.(string))
	}
	return strings.Join(parts, "; ")
}

func mustDo(t *testing.T, c *client, want int, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := c.do(method, path, body)
	if rec.Code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	return rec
}

// The journal's reason to exist: somebody made the gateway let more through,
// and the entry says who, to what, and that it was a weakening.
func TestTheJournalSaysWhoDidWhat(t *testing.T) {
	h, _ := auditRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")

	rec := mustDo(t, owner, http.StatusCreated, http.MethodPost, "/admin/keys", `{"name":"ci-bot"}`)
	var key storage.Key
	json.Unmarshal(rec.Body.Bytes(), &key)

	mustDo(t, owner, http.StatusOK, http.MethodPut, "/admin/policies/default",
		`{"secrets":"alert","pii":"redact","custom":"alert"}`)
	mustDo(t, owner, http.StatusOK, http.MethodPost, "/admin/policies/keys/"+key.ID+"/mute", `{"rule":"aws-access-key"}`)
	mustDo(t, owner, http.StatusOK, http.MethodPost, "/admin/policies/keys/"+key.ID+"/unmute", `{"rule":"aws-access-key"}`)
	mustDo(t, owner, http.StatusCreated, http.MethodPost, "/api/invitations", `{"email":"ann@acme.test","role":"admin"}`)
	mustDo(t, owner, http.StatusNoContent, http.MethodDelete, "/admin/keys/"+key.ID, "")

	entries := journal(t, owner, "")
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	// Oldest last: the operator creating the organization and inviting its
	// owner, the owner joining, then everything the owner did.
	want := "key.delete member.invite policy.unmute policy.mute policy.update key.create " +
		"member.join member.invite organization.create"
	if strings.Join(actions, " ") != want {
		t.Fatalf("journal, newest first:\n got %v\nwant %v", actions, want)
	}
	for _, e := range entries[:7] {
		if e.ActorKind != storage.ActorUser || e.ActorLabel != "owner@acme.test" {
			t.Errorf("%s: actor = %s %q, want the owner", e.Action, e.ActorKind, e.ActorLabel)
		}
	}
	for _, e := range entries[7:] {
		if e.ActorKind != storage.ActorOperator {
			t.Errorf("%s: actor = %s %q, want the operator", e.Action, e.ActorKind, e.ActorLabel)
		}
	}

	policy := find(t, entries, "policy.update")
	if policy.Target != defaultTarget || policy.Meta["weakened"] != true ||
		!strings.Contains(changes(policy), "secrets: block → alert") {
		t.Errorf("a weakened default policy reads as %+v", policy)
	}
	mute := find(t, entries, "policy.mute")
	if mute.Target != "ci-bot" || mute.Meta["rule"] != "aws-access-key" || mute.Meta["weakened"] != true {
		t.Errorf("a mute reads as %+v", mute)
	}
	if unmute := find(t, entries, "policy.unmute"); unmute.Meta["weakened"] != nil {
		t.Errorf("an unmute is not a weakening: %+v", unmute)
	}
	// Deleted, the key is still called by the name people knew it by.
	if del := find(t, entries, "key.delete"); del.Target != "ci-bot" {
		t.Errorf("a deleted key is named %q, want ci-bot", del.Target)
	}
	if inv := find(t, entries, "member.invite"); inv.Target != "ann@acme.test" || inv.Meta["role"] != "admin" {
		t.Errorf("an invitation reads as %+v", inv)
	}
}

// A token and the operator change things too, and are named as what they are.
func TestTokensAndTheOperatorAreNamed(t *testing.T) {
	h, _ := auditRouter(t)
	owner, orgID := signedInOwner(t, h, "Acme", "owner@acme.test")
	token := mintToken(t, owner, "deploy-bot", "keys:write")

	if rec := asToken(t, h, token, http.MethodPost, "/admin/keys", `{"name":"from-ci"}`); rec.Code != http.StatusCreated {
		t.Fatalf("token creates a key = %d: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodPut, "/admin/limits/default", strings.NewReader(`{"requests_per_minute":60}`))
	req.Header.Set("Authorization", "Bearer instance-admin")
	req.Header.Set("X-Aperture-Org", orgID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator sets limits = %d: %s", rec.Code, rec.Body.String())
	}

	entries := journal(t, owner, "")
	if e := find(t, entries, "key.create"); e.ActorKind != storage.ActorToken || e.ActorLabel != "deploy-bot" || e.ActorID == "" {
		t.Errorf("a token's change reads as %s %q", e.ActorKind, e.ActorLabel)
	}
	if e := find(t, entries, "limits.update"); e.ActorKind != storage.ActorOperator ||
		!strings.Contains(changes(e), "requests per minute: none → 60") {
		t.Errorf("the operator's change reads as %+v", e)
	}
	if e := find(t, entries, "token.create"); e.Target != "deploy-bot" || e.ActorLabel != "owner@acme.test" {
		t.Errorf("minting a token reads as %+v", e)
	}
}

// Owners and admins read the journal; members, viewers and machines do not,
// and nobody reads another organization's.
func TestTheJournalIsForAdmins(t *testing.T) {
	h, _ := auditRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")
	other, _ := signedInOwner(t, h, "Globex", "owner@globex.test")
	mustDo(t, owner, http.StatusCreated, http.MethodPost, "/admin/keys", `{"name":"acme-secret-project"}`)

	// A member of Acme.
	rec := mustDo(t, owner, http.StatusCreated, http.MethodPost, "/api/invitations", `{"email":"m@acme.test","role":"member"}`)
	var inv inviteResponse
	json.Unmarshal(rec.Body.Bytes(), &inv)
	member := newClient(t, h)
	mustDo(t, member, http.StatusOK, http.MethodPost, "/api/auth/register",
		`{"token":"`+inv.Token+`","email":"m@acme.test","name":"M","password":"a good long password"}`)
	if rec := member.do(http.MethodGet, "/admin/audit", ""); rec.Code != http.StatusForbidden {
		t.Errorf("a member reads the journal: %d", rec.Code)
	}

	// Not even a token that may change everything: the journal is about
	// people, and naming them is what a leaked token must not get.
	token := mintToken(t, owner, "everything", "events:read", "keys:read", "keys:write", "policies:write")
	if rec := asToken(t, h, token, http.MethodGet, "/admin/audit", ""); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "not available to service tokens") {
		t.Errorf("a service token reads the journal: %d %s", rec.Code, rec.Body.String())
	}

	for _, e := range journal(t, other, "") {
		if strings.Contains(e.Target, "acme") {
			t.Errorf("Globex reads Acme's journal: %+v", e)
		}
	}
}

// Only what happened goes in: a refused request, a failed one and a save that
// changed nothing leave no entry.
func TestOnlyChangesAreJournaled(t *testing.T) {
	h, store := auditRouter(t)
	owner, orgID := signedInOwner(t, h, "Acme", "owner@acme.test")
	before, _ := store.List(context.Background(), orgID, storage.AuditFilter{})

	owner.do(http.MethodPut, "/admin/policies/default", `{"secrets":"sometimes"}`)        // invalid
	owner.do(http.MethodDelete, "/admin/keys/no-such-key", "")                            // not found
	owner.do(http.MethodPut, "/admin/providers/openai", `{"kind":"openai","base_url":1}`) // not JSON it accepts
	owner.do(http.MethodPut, "/admin/policies/default", `{"secrets":"block","pii":"redact"}`)
	owner.do(http.MethodPut, "/admin/limits/default", `{}`)
	owner.do(http.MethodPut, "/admin/providers/groq", `{"kind":"groq"}`) // a new one: this is a change
	owner.do(http.MethodPut, "/admin/providers/groq", `{"kind":"groq"}`) // the same again: this is not

	after, _ := store.List(context.Background(), orgID, storage.AuditFilter{})
	if len(after) != len(before)+1 || after[0].Action != "provider.create" || after[0].Target != "groq" {
		t.Errorf("want only the new provider journaled, got %d new: %+v", len(after)-len(before), after[:len(after)-len(before)])
	}
}

// Secrets are why the journal exists; they must not be what fills it. A key,
// a proxy password and a webhook's path are recorded as having changed, never
// as what they are.
func TestTheJournalHoldsNoSecrets(t *testing.T) {
	h, _ := auditRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")

	mustDo(t, owner, http.StatusCreated, http.MethodPost, "/admin/keys",
		`{"name":"own-creds","openai_api_key":"sk-SECRET-own-key"}`)
	mustDo(t, owner, http.StatusOK, http.MethodPut, "/admin/providers/openai",
		`{"kind":"openai","api_key":"sk-SECRET-org-key","proxy_url":"http://user:SECRET-pass@proxy.corp:3128"}`)
	mustDo(t, owner, http.StatusOK, http.MethodPut, "/admin/providers/deepseek",
		`{"kind":"openai-compatible","base_url":"https://api.deepseek.com/v1?key=SECRET-in-query","prefixes":["deepseek-"]}`)
	mustDo(t, owner, http.StatusOK, http.MethodPost, "/admin/config", `{"anthropic_api_key":"sk-ant-SECRET"}`)
	mustDo(t, owner, http.StatusOK, http.MethodPut, "/admin/alerts",
		`{"url":"https://hooks.slack.com/services/T0/B0/SECRET-hook","format":"slack"}`)
	token := mintToken(t, owner, "ci", "events:read")

	rec := mustDo(t, owner, http.StatusOK, http.MethodGet, "/admin/audit", "")
	body := rec.Body.String()
	for _, secret := range []string{"SECRET", token, strings.TrimPrefix(token, serviceTokenPrefix)} {
		if strings.Contains(body, secret) {
			t.Errorf("the journal carries %q:\n%s", secret, body)
		}
	}

	entries := journal(t, owner, "")
	proxy := find(t, entries, "provider.create")
	for _, e := range entries {
		if e.Action == "provider.create" && e.Target == "openai" {
			proxy = e
		}
	}
	if c := changes(proxy); !strings.Contains(c, "API key set") || !strings.Contains(c, "proxy set") {
		t.Errorf("a provider with a key and a proxy reads as %q", c)
	}
	if e := find(t, entries, "key.create"); e.Meta["own_provider_keys"] == nil {
		t.Errorf("a key with its own credentials does not say so: %+v", e)
	}
	if e := find(t, entries, "alerts.update"); !strings.Contains(changes(e), "hooks.slack.com") {
		t.Errorf("an alert destination does not say where it points: %+v", e)
	}
}

// People coming and going, and their roles, are the other half of who may do
// what.
func TestMembershipIsJournaled(t *testing.T) {
	h, _ := auditRouter(t)
	owner, _ := signedInOwner(t, h, "Acme", "owner@acme.test")
	rec := mustDo(t, owner, http.StatusCreated, http.MethodPost, "/api/invitations", `{"email":"ann@acme.test","role":"member"}`)
	var inv inviteResponse
	json.Unmarshal(rec.Body.Bytes(), &inv)
	ann := newClient(t, h)
	rec = mustDo(t, ann, http.StatusOK, http.MethodPost, "/api/auth/register",
		`{"token":"`+inv.Token+`","email":"ann@acme.test","name":"Ann","password":"a good long password"}`)
	annID := ann.me(rec).User.ID

	mustDo(t, owner, http.StatusOK, http.MethodPut, "/api/members/"+annID+"/role", `{"role":"admin"}`)
	mustDo(t, owner, http.StatusNoContent, http.MethodDelete, "/api/members/"+annID, "")

	entries := journal(t, owner, "?group=member")
	for _, e := range entries {
		if !strings.HasPrefix(e.Action, "member.") {
			t.Errorf("group=member returned %s", e.Action)
		}
	}
	if e := find(t, entries, "member.join"); e.ActorLabel != "ann@acme.test" || e.Target != "ann@acme.test" {
		t.Errorf("joining reads as %+v", e)
	}
	if e := find(t, entries, "member.role"); e.Target != "ann@acme.test" || changes(e) != "role: member → admin" {
		t.Errorf("a role change reads as %+v", e)
	}
	if e := find(t, entries, "member.remove"); e.Target != "ann@acme.test" || e.ActorLabel != "owner@acme.test" {
		t.Errorf("a removal reads as %+v", e)
	}
}

func TestPolicyChangesSayWhetherTheyWeaken(t *testing.T) {
	strict := inspector.Policy{Secrets: inspector.ActionBlock, PII: inspector.ActionRedact, Custom: inspector.ActionAlert,
		CustomRules: []inspector.CustomRule{{Name: "project", Pattern: "falcon"}}}
	with := func(f func(p *inspector.Policy)) inspector.Policy {
		p := strict
		p.CustomRules = append([]inspector.CustomRule(nil), strict.CustomRules...)
		f(&p)
		return p
	}
	cases := []struct {
		name     string
		after    inspector.Policy
		weakened bool
		says     string
	}{
		{"secrets only alerted", with(func(p *inspector.Policy) { p.Secrets = inspector.ActionAlert }), true, "secrets: block → alert"},
		{"pii switched off", with(func(p *inspector.Policy) { p.PII = "" }), true, "pii: redact → off"},
		{"pii now blocked", with(func(p *inspector.Policy) { p.PII = inspector.ActionBlock }), false, "pii: redact → block"},
		{"a rule removed", with(func(p *inspector.Policy) { p.CustomRules = nil }), true, "custom rule removed: project"},
		{"a rule added", with(func(p *inspector.Policy) {
			p.CustomRules = append(p.CustomRules, inspector.CustomRule{Name: "x", Pattern: "y"})
		}), false, "custom rule added: x"},
		{"a rule rewritten", with(func(p *inspector.Policy) { p.CustomRules[0].Pattern = "nothing" }), false, "custom rule changed: project"},
		{"an exemption", with(func(p *inspector.Policy) { p.Allowlist = []string{"AKIA.*"} }), true, "allowlisted: AKIA.*"},
		{"a mute", with(func(p *inspector.Policy) { p.MutedRules = []string{"Email"} }), true, "muted: email"},
		{"responses scanned", with(func(p *inspector.Policy) { p.ScanResponses = true }), false, "response scanning on"},
	}
	for _, c := range cases {
		meta := policyChange(strict, c.after)
		got := meta["weakened"] == true
		if got != c.weakened {
			t.Errorf("%s: weakened = %v, want %v (%v)", c.name, got, c.weakened, meta)
		}
		list, _ := meta["changes"].([]string)
		if !strings.Contains(strings.Join(list, "; "), c.says) {
			t.Errorf("%s: changes = %v, want it to say %q", c.name, list, c.says)
		}
	}
	if meta := policyChange(strict, strict); len(meta) != 0 {
		t.Errorf("the same policy is a change: %v", meta)
	}
}

func TestRaisingALimitIsAWeakening(t *testing.T) {
	cases := []struct {
		before, after limits.Limits
		weakened      bool
	}{
		{limits.Limits{BudgetDailyUSD: 10}, limits.Limits{BudgetDailyUSD: 50}, true},
		{limits.Limits{BudgetDailyUSD: 10}, limits.Limits{}, true}, // no budget at all
		{limits.Limits{BudgetDailyUSD: 50}, limits.Limits{BudgetDailyUSD: 10}, false},
		{limits.Limits{}, limits.Limits{RequestsPerMinute: 60}, false},
		{limits.Limits{RequestsPerMinute: 60}, limits.Limits{RequestsPerMinute: 600}, true},
	}
	for _, c := range cases {
		if got := limitsChange(c.before, c.after)["weakened"] == true; got != c.weakened {
			t.Errorf("%+v → %+v: weakened = %v, want %v", c.before, c.after, got, c.weakened)
		}
	}
}
