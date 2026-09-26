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

	"github.com/mutegate/mutegate/internal/inspector"
	"github.com/mutegate/mutegate/internal/storage"
)

// recordingUpstream stands in for an LLM provider and remembers which key
// each request arrived with, and where.
type recordingUpstream struct {
	mu   sync.Mutex
	hits []string // "path auth"
	srv  *httptest.Server
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	u := &recordingUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		auth := r.Header.Get("Authorization")
		if auth == "" {
			auth = "x-api-key " + r.Header.Get("x-api-key")
		}
		u.mu.Lock()
		u.hits = append(u.hits, r.URL.Path+" "+auth)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},` +
			`"content":[{"type":"text","text":"ok"}],"type":"message","role":"assistant"}`))
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *recordingUpstream) last() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.hits) == 0 {
		return ""
	}
	return u.hits[len(u.hits)-1]
}

type providerEnv struct {
	t         *testing.T
	h         http.Handler
	providers *storage.MemProviderStore
}

const (
	provOrgA = "11111111-1111-1111-1111-111111111111"
	provOrgB = "22222222-2222-2222-2222-222222222222"
)

// newProviderEnv builds a gateway with a provider store and three mutegate
// keys: two in organization A (one carrying its own OpenAI key) and one in B.
func newProviderEnv(t *testing.T) *providerEnv {
	t.Helper()
	keys := &twoOrgKeyStore{byToken: map[string]*storage.Key{
		"ap-a":     {ID: "key-a", OrgID: provOrgA, Name: "a", Providers: map[string]string{}},
		"ap-a-own": {ID: "key-a-own", OrgID: provOrgA, Name: "a-own", Providers: map[string]string{"openai": "sk-key-own"}},
		"ap-b":     {ID: "key-b", OrgID: provOrgB, Name: "b", Providers: map[string]string{}},
	}}
	providers := storage.NewMemProviderStore()
	h := Routes(Options{
		KeyStore:      keys,
		ProviderStore: providers,
		PolicyStore:   storage.NewMemPolicyStore(inspector.Policy{Secrets: inspector.ActionAlert}),
		Inspector:     inspector.New(),
		AdminAPIKey:   "instance-admin",
		Logger:        slog.Default(),
	})
	return &providerEnv{t: t, h: h, providers: providers}
}

func (e *providerEnv) put(org string, p storage.ProviderConfig) {
	e.t.Helper()
	if err := e.providers.PutProvider(context.Background(), org, p); err != nil {
		e.t.Fatal(err)
	}
}

func (e *providerEnv) chat(token, model string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *providerEnv) admin(org, method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer instance-admin")
	r.Header.Set("X-Mutegate-Org", org)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

// The open question from slice 3: the organization's provider key used to
// reach no traffic at all. Now it is the default every Mutegate key inherits.
func TestAKeyWithoutItsOwnUsesTheOrganizationsProvider(t *testing.T) {
	up := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: up.srv.URL, APIKey: "sk-org-a", Enabled: true})

	if rec := e.chat("ap-a", "gpt-4o-mini"); rec.Code != http.StatusOK {
		t.Fatalf("chat = %d: %s", rec.Code, rec.Body.String())
	}
	if got := up.last(); got != "/v1/chat/completions Bearer sk-org-a" {
		t.Errorf("upstream saw %q, want the organization's key at the provider's address", got)
	}
}

func TestAKeysOwnCredentialWins(t *testing.T) {
	up := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: up.srv.URL, APIKey: "sk-org-a", Enabled: true})

	e.chat("ap-a-own", "gpt-4o-mini")
	if got := up.last(); got != "/v1/chat/completions Bearer sk-key-own" {
		t.Errorf("upstream saw %q, want the Mutegate key's own credential", got)
	}
}

// Organizations do not lend each other their keys.
func TestAnotherOrganizationsProviderIsNeverUsed(t *testing.T) {
	up := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: up.srv.URL, APIKey: "sk-org-a", Enabled: true})

	rec := e.chat("ap-b", "gpt-4o-mini")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no API key configured for openai") {
		t.Fatalf("B's request = %d %s, want a missing-key refusal", rec.Code, rec.Body.String())
	}
	if up.last() != "" {
		t.Errorf("B's request reached A's provider: %q", up.last())
	}
}

func TestADisabledProviderRefuses(t *testing.T) {
	up := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: up.srv.URL, APIKey: "sk-org-a", Enabled: false})

	rec := e.chat("ap-a", "gpt-4o-mini")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "disabled") {
		t.Fatalf("a disabled provider = %d %s", rec.Code, rec.Body.String())
	}
	// Even for a key with its own credential: disabled means disabled.
	if rec := e.chat("ap-a-own", "gpt-4o-mini"); rec.Code != http.StatusBadRequest {
		t.Errorf("a key's own credential bypassed a disabled provider: %d", rec.Code)
	}
	if up.last() != "" {
		t.Error("traffic reached a disabled provider")
	}
}

// An OpenAI-compatible provider claims models by prefix; the longest wins.
func TestCompatibleProvidersRouteByLongestPrefix(t *testing.T) {
	general := newRecordingUpstream(t)
	coder := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "deepseek", Kind: storage.KindCompatible, BaseURL: general.srv.URL + "/v1",
		APIKey: "sk-ds", Prefixes: []string{"deepseek"}, Enabled: true})
	e.put(provOrgA, storage.ProviderConfig{Name: "deepseek-coder-eu", Kind: storage.KindCompatible, BaseURL: coder.srv.URL + "/v1",
		APIKey: "sk-ds-eu", Prefixes: []string{"deepseek-coder"}, Enabled: true})

	e.chat("ap-a", "deepseek-chat")
	e.chat("ap-a", "deepseek-coder-v2")
	if got := general.last(); got != "/v1/chat/completions Bearer sk-ds" {
		t.Errorf("deepseek-chat went to %q", got)
	}
	if got := coder.last(); got != "/v1/chat/completions Bearer sk-ds-eu" {
		t.Errorf("deepseek-coder-v2 went to %q, want the longer prefix", got)
	}
}

// The reason for this slice: a provider's traffic leaves through its proxy.
func TestProviderTrafficGoesThroughItsProxy(t *testing.T) {
	up := newRecordingUpstream(t)
	var mu sync.Mutex
	var relayed []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		relayed = append(relayed, r.URL.String())
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		req, _ := http.NewRequest(r.Method, r.URL.String(), strings.NewReader(string(body)))
		req.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: up.srv.URL,
		APIKey: "sk-org-a", ProxyURL: proxy.URL, Enabled: true})

	if rec := e.chat("ap-a", "gpt-4o-mini"); rec.Code != http.StatusOK {
		t.Fatalf("chat through the proxy = %d: %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(relayed) != 1 || relayed[0] != up.srv.URL+"/v1/chat/completions" {
		t.Errorf("the proxy relayed %v, want the one chat request", relayed)
	}
}

// The native Messages path resolves through the same place: the
// organization's Anthropic provider, its address, its key.
func TestNativeMessagesUseTheOrganizationsProvider(t *testing.T) {
	up := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "anthropic", Kind: storage.KindAnthropic, BaseURL: up.srv.URL, APIKey: "sk-ant-org", Enabled: true})

	r := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-3-5-sonnet","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("x-api-key", "ap-a")
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("messages = %d: %s", rec.Code, rec.Body.String())
	}
	if got := up.last(); got != "/v1/messages x-api-key sk-ant-org" {
		t.Errorf("upstream saw %q", got)
	}
}

func TestProviderSavesAreChecked(t *testing.T) {
	e := newProviderEnv(t)
	for name, c := range map[string]struct{ path, body string }{
		"built-in under another name":  {"/admin/providers/my-openai", `{"kind":"openai"}`},
		"compatible without address":   {"/admin/providers/deepseek", `{"kind":"openai-compatible","prefixes":["deepseek"]}`},
		"compatible without prefix":    {"/admin/providers/deepseek", `{"kind":"openai-compatible","base_url":"https://api.deepseek.com/v1"}`},
		"custom named like a built-in": {"/admin/providers/groq", `{"kind":"openai-compatible","base_url":"https://x.example/v1","prefixes":["x"]}`},
		"unusable proxy":               {"/admin/providers/openai", `{"kind":"openai","proxy_url":"ftp://proxy"}`},
		"credentials in the address":   {"/admin/providers/openai", `{"kind":"openai","base_url":"https://user:sk@api.openai.com"}`},
		"silly timeout":                {"/admin/providers/openai", `{"kind":"openai","timeout_ms":5}`},
	} {
		if rec := e.admin(provOrgA, http.MethodPut, c.path, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: saved with %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// What is saved is readable only as "set" and a hint — never the key, never
// the proxy's password.
func TestProviderSecretsNeverComeBack(t *testing.T) {
	e := newProviderEnv(t)
	rec := e.admin(provOrgA, http.MethodPut, "/admin/providers/openai",
		`{"kind":"openai","api_key":"sk-proj-secret-value-1234","proxy_url":"http://svc:hunter2@proxy.corp:3128"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	list := e.admin(provOrgA, http.MethodGet, "/admin/providers", "")
	body := list.Body.String() + rec.Body.String()
	if strings.Contains(body, "sk-proj-secret-value") || strings.Contains(body, "hunter2") {
		t.Fatalf("a secret came back: %s", body)
	}
	if !strings.Contains(body, `"key_set":true`) || !strings.Contains(body, "svc@proxy.corp:3128") {
		t.Errorf("the view does not say what is set: %s", body)
	}
}

// Turning a provider off must not cost it its address or its key.
func TestAPartialSaveKeepsTheRest(t *testing.T) {
	e := newProviderEnv(t)
	e.admin(provOrgA, http.MethodPut, "/admin/providers/deepseek",
		`{"kind":"openai-compatible","base_url":"https://api.deepseek.com/v1","prefixes":["deepseek"],"api_key":"sk-ds","timeout_ms":90000}`)
	if rec := e.admin(provOrgA, http.MethodPut, "/admin/providers/deepseek", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("toggle = %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := e.providers.GetProvider(context.Background(), provOrgA, "deepseek")
	if got.Enabled || got.BaseURL != "https://api.deepseek.com/v1" || got.APIKey != "sk-ds" ||
		len(got.Prefixes) != 1 || got.TimeoutMS != 90000 {
		t.Errorf("turning it off changed more than that: %+v", got)
	}
	// And an empty key clears it, which absent does not.
	e.admin(provOrgA, http.MethodPut, "/admin/providers/deepseek", `{"api_key":""}`)
	if got, _ := e.providers.GetProvider(context.Background(), provOrgA, "deepseek"); got.APIKey != "" {
		t.Error("an empty key did not clear it")
	}
}

func TestTwoProvidersCannotClaimTheSamePrefix(t *testing.T) {
	e := newProviderEnv(t)
	e.admin(provOrgA, http.MethodPut, "/admin/providers/one",
		`{"kind":"openai-compatible","base_url":"https://one.example/v1","prefixes":["qwen"]}`)
	rec := e.admin(provOrgA, http.MethodPut, "/admin/providers/two",
		`{"kind":"openai-compatible","base_url":"https://two.example/v1","prefixes":["Qwen"]}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("a tied prefix was saved: %d %s", rec.Code, rec.Body.String())
	}
}

// Settings' old key API now writes the organization's providers, so what it
// saves reaches traffic.
func TestTheConfigAPIWritesProviders(t *testing.T) {
	up := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: up.srv.URL, Enabled: true})

	if rec := e.admin(provOrgA, http.MethodPost, "/admin/config", `{"openai_api_key":"sk-via-settings"}`); rec.Code != http.StatusOK {
		t.Fatalf("config = %d: %s", rec.Code, rec.Body.String())
	}
	e.chat("ap-a", "gpt-4o-mini")
	if got := up.last(); got != "/v1/chat/completions Bearer sk-via-settings" {
		t.Errorf("a key saved through Settings did not reach traffic: %q", got)
	}
	var cfg struct {
		Configured []string `json:"configured_providers"`
	}
	json.Unmarshal(e.admin(provOrgA, http.MethodGet, "/admin/config", "").Body.Bytes(), &cfg)
	if len(cfg.Configured) != 1 || cfg.Configured[0] != "openai" {
		t.Errorf("configured = %v", cfg.Configured)
	}
}

func TestConnectivityCheckTestsWhatIsOnScreen(t *testing.T) {
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer models.Close()
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: models.URL, APIKey: "sk-good", Enabled: true})

	check := func(body string) map[string]any {
		rec := e.admin(provOrgA, http.MethodPost, "/admin/providers/test", body)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	// The saved provider, with its stored key.
	if got := check(`{"name":"openai"}`); got["ok"] != true || got["stage"] != "ok" {
		t.Errorf("the saved provider: %v", got)
	}
	// An unsaved edit laid over it: a different key, not yet saved.
	if got := check(`{"name":"openai","api_key":"sk-typo"}`); got["ok"] != false || got["stage"] != "auth" {
		t.Errorf("an unsaved wrong key: %v", got)
	}
	// And the unsaved edit was not saved by testing it.
	if p, _ := e.providers.GetProvider(context.Background(), provOrgA, "openai"); p.APIKey != "sk-good" {
		t.Error("testing an edit saved it")
	}
}

// The provider list is cached for a few seconds. The cache is per
// organization, or it would hand one organization's key to the next request
// from another — so A asks first, and B, inside the window, must still see
// only its own.
func TestProviderCacheIsPerOrganization(t *testing.T) {
	upA := newRecordingUpstream(t)
	upB := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: upA.srv.URL, APIKey: "sk-org-a", Enabled: true})
	e.put(provOrgB, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: upB.srv.URL, APIKey: "sk-org-b", Enabled: true})

	e.chat("ap-a", "gpt-4o-mini")
	e.chat("ap-b", "gpt-4o-mini")
	if got := upA.last(); got != "/v1/chat/completions Bearer sk-org-a" {
		t.Errorf("A's request: %q", got)
	}
	if got := upB.last(); got != "/v1/chat/completions Bearer sk-org-b" {
		t.Errorf("B's request went with %q, want B's own provider", got)
	}
}

// A change made in the console applies to the next request, not the next
// cache expiry.
func TestASavedProviderAppliesAtOnce(t *testing.T) {
	up := newRecordingUpstream(t)
	e := newProviderEnv(t)
	e.admin(provOrgA, http.MethodPut, "/admin/providers/openai",
		`{"kind":"openai","base_url":"`+up.srv.URL+`","api_key":"sk-first"}`)
	e.chat("ap-a", "gpt-4o-mini")
	e.admin(provOrgA, http.MethodPut, "/admin/providers/openai", `{"api_key":"sk-second"}`)
	e.chat("ap-a", "gpt-4o-mini")
	if got := up.last(); got != "/v1/chat/completions Bearer sk-second" {
		t.Errorf("after saving a new key the request still used %q", got)
	}
}
