package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/danilovid/mutegate/internal/storage"
)

// fakeModels is a provider's models endpoint: it answers at path with body,
// counts the calls, and remembers the headers of the last one.
type fakeModels struct {
	srv    *httptest.Server
	calls  atomic.Int32
	header atomic.Value // http.Header
}

func newFakeModels(t *testing.T, path string, status int, body string) *fakeModels {
	t.Helper()
	f := &fakeModels{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		f.calls.Add(1)
		f.header.Store(r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeModels) lastHeader() http.Header {
	h, _ := f.header.Load().(http.Header)
	return h
}

type modelsAnswer struct {
	Data        []modelEntry        `json:"data"`
	Unavailable []map[string]string `json:"unavailable"`
	Error       string              `json:"error"`
}

func (e *providerEnv) models(token string) (int, modelsAnswer) {
	e.t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	var out modelsAnswer
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func ownerOf(list []modelEntry) map[string]string {
	out := map[string]string{}
	for _, m := range list {
		out[m.ID] = m.OwnedBy
	}
	return out
}

// Every provider the organization has a key for is asked, and the answer is
// what they serve today — with the models the gateway could not route back to
// them left out.
func TestModelsComeFromTheProviders(t *testing.T) {
	e := newProviderEnv(t)
	openaiUp := newFakeModels(t, "/v1/models", 200, `{"object":"list","data":[
		{"id":"gpt-4o-mini","created":100,"owned_by":"system"},
		{"id":"gpt-5","created":300,"owned_by":"system"},
		{"id":"llama-openai-copy","created":200,"owned_by":"system"}]}`)
	anthropicUp := newFakeModels(t, "/v1/models", 200, `{"data":[
		{"type":"model","id":"claude-opus-4-1","display_name":"Claude Opus 4.1","created_at":"2025-08-05T00:00:00Z"},
		{"type":"model","id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5","created_at":"2025-09-29T00:00:00Z"}],
		"has_more":false}`)
	groqUp := newFakeModels(t, "/models", 200, `{"object":"list","data":[
		{"id":"llama-3.3-70b-versatile","created":10},
		{"id":"openai/gpt-oss-120b","created":20},
		{"id":"qwen/qwen3-32b","created":30}]}`)
	deepseekUp := newFakeModels(t, "/v1/models", 200, `{"object":"list","data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`)

	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: openaiUp.srv.URL, APIKey: "sk-org", Enabled: true})
	e.put(provOrgA, storage.ProviderConfig{Name: "anthropic", Kind: storage.KindAnthropic, BaseURL: anthropicUp.srv.URL, APIKey: "sk-ant-org", Enabled: true})
	e.put(provOrgA, storage.ProviderConfig{Name: "groq", Kind: storage.KindGroq, BaseURL: groqUp.srv.URL, APIKey: "gsk-org", Enabled: true})
	e.put(provOrgA, storage.ProviderConfig{Name: "deepseek", Kind: storage.KindCompatible, BaseURL: deepseekUp.srv.URL + "/v1",
		APIKey: "sk-deep", Prefixes: []string{"deepseek-"}, Enabled: true})

	code, got := e.models("ap-a")
	if code != http.StatusOK {
		t.Fatalf("models = %d: %+v", code, got)
	}
	owners := ownerOf(got.Data)
	want := map[string]string{
		"gpt-5": "openai", "gpt-4o-mini": "openai",
		"claude-sonnet-4-5": "anthropic", "claude-opus-4-1": "anthropic",
		"llama-3.3-70b-versatile": "groq",
		"deepseek-chat":           "deepseek", "deepseek-reasoner": "deepseek",
	}
	for id, owner := range want {
		if owners[id] != owner {
			t.Errorf("%s: owned by %q, want %q (got %v)", id, owners[id], owner, owners)
		}
	}
	// Listed by one provider, routed to another: offering them would be
	// offering an error.
	for _, id := range []string{"openai/gpt-oss-120b", "qwen/qwen3-32b", "llama-openai-copy"} {
		if _, ok := owners[id]; ok {
			t.Errorf("%s is offered, but a request for it would not reach the provider that listed it", id)
		}
	}
	if len(got.Data) != len(want) {
		t.Errorf("got %d models, want %d: %v", len(got.Data), len(want), owners)
	}

	// Newest first within a provider.
	var order []string
	for _, m := range got.Data {
		order = append(order, m.ID)
	}
	joined := strings.Join(order, " ")
	if !strings.Contains(joined, "gpt-5 gpt-4o-mini") || !strings.Contains(joined, "claude-sonnet-4-5 claude-opus-4-1") {
		t.Errorf("not newest first: %v", order)
	}

	// Anthropic is asked its own way, and its answer arrives in OpenAI's shape.
	if h := anthropicUp.lastHeader(); h.Get("x-api-key") != "sk-ant-org" || h.Get("anthropic-version") == "" {
		t.Errorf("anthropic asked with headers %v", h)
	}
	for _, m := range got.Data {
		if m.ID == "claude-sonnet-4-5" && (m.Created == 0 || m.DisplayName != "Claude Sonnet 4.5" || m.Object != "model") {
			t.Errorf("anthropic model came back as %+v", m)
		}
	}
	if h := openaiUp.lastHeader(); h.Get("Authorization") != "Bearer sk-org" {
		t.Errorf("openai asked with %q", h.Get("Authorization"))
	}
}

// A key's own credential is what its list is asked with, and a provider with
// no credential for this key is not asked at all.
func TestModelsAreAskedWithTheKeysOwnCredential(t *testing.T) {
	e := newProviderEnv(t)
	openaiUp := newFakeModels(t, "/v1/models", 200, `{"data":[{"id":"gpt-5"}]}`)
	anthropicUp := newFakeModels(t, "/v1/models", 200, `{"data":[]}`)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: openaiUp.srv.URL, Enabled: true})
	e.put(provOrgA, storage.ProviderConfig{Name: "anthropic", Kind: storage.KindAnthropic, BaseURL: anthropicUp.srv.URL, Enabled: true})

	if code, got := e.models("ap-a-own"); code != http.StatusOK || len(got.Data) != 1 {
		t.Fatalf("models = %d: %+v", code, got)
	}
	if h := openaiUp.lastHeader(); h.Get("Authorization") != "Bearer sk-key-own" {
		t.Errorf("openai asked with %q, want the key's own credential", h.Get("Authorization"))
	}
	if n := anthropicUp.calls.Load(); n != 0 {
		t.Errorf("a provider with no credential was asked %d times", n)
	}

	// No credential anywhere: say so, as before.
	if code, got := e.models("ap-b"); code != http.StatusBadRequest || !strings.Contains(got.Error, "no API key") {
		t.Errorf("a key with nothing configured = %d %+v", code, got)
	}
}

// One provider failing does not hide the others, and says why it is missing.
func TestAFailingProviderIsNamedNotFatal(t *testing.T) {
	e := newProviderEnv(t)
	openaiUp := newFakeModels(t, "/v1/models", 200, `{"data":[{"id":"gpt-5"}]}`)
	anthropicUp := newFakeModels(t, "/v1/models", 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: openaiUp.srv.URL, APIKey: "sk-org", Enabled: true})
	e.put(provOrgA, storage.ProviderConfig{Name: "anthropic", Kind: storage.KindAnthropic, BaseURL: anthropicUp.srv.URL, APIKey: "sk-ant-wrong", Enabled: true})

	code, got := e.models("ap-a")
	if code != http.StatusOK || len(got.Data) != 1 || got.Data[0].ID != "gpt-5" {
		t.Fatalf("models = %d: %+v", code, got)
	}
	if len(got.Unavailable) != 1 || got.Unavailable[0]["provider"] != "anthropic" ||
		!strings.Contains(got.Unavailable[0]["error"], "401") || !strings.Contains(got.Unavailable[0]["error"], "API key") {
		t.Errorf("the failure is not named: %+v", got.Unavailable)
	}

	// All of them failing is a gateway error, not an empty list. (Saved
	// through the API, which is what drops the organization's cached
	// provider list.)
	if rec := e.admin(provOrgA, http.MethodPut, "/admin/providers/openai", `{"base_url":"`+anthropicUp.srv.URL+`","api_key":"sk-org-2"}`); rec.Code != http.StatusOK {
		t.Fatalf("repoint openai = %d: %s", rec.Code, rec.Body.String())
	}
	if code, got := e.models("ap-a"); code != http.StatusBadGateway || len(got.Unavailable) != 2 {
		t.Errorf("every provider failing = %d %+v, want 502 naming both", code, got)
	}
}

// A disabled provider's models are not offered: a request for them is refused.
func TestADisabledProvidersModelsAreNotOffered(t *testing.T) {
	e := newProviderEnv(t)
	openaiUp := newFakeModels(t, "/v1/models", 200, `{"data":[{"id":"gpt-5"}]}`)
	groqUp := newFakeModels(t, "/models", 200, `{"data":[{"id":"llama-3.3-70b-versatile"}]}`)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: openaiUp.srv.URL, APIKey: "sk-org", Enabled: true})
	e.put(provOrgA, storage.ProviderConfig{Name: "groq", Kind: storage.KindGroq, BaseURL: groqUp.srv.URL, APIKey: "gsk", Enabled: false})

	_, got := e.models("ap-a")
	if owners := ownerOf(got.Data); owners["llama-3.3-70b-versatile"] != "" {
		t.Errorf("a disabled provider's model is offered: %v", owners)
	}
	if n := groqUp.calls.Load(); n != 0 {
		t.Errorf("a disabled provider was asked %d times", n)
	}
}

// Lists change on the scale of weeks and the playground asks every time it
// opens, so an answer is reused for a while — per credential, so one key's
// list is never another's.
func TestModelListsAreCachedPerCredential(t *testing.T) {
	e := newProviderEnv(t)
	openaiUp := newFakeModels(t, "/v1/models", 200, `{"data":[{"id":"gpt-5"}]}`)
	e.put(provOrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, BaseURL: openaiUp.srv.URL, APIKey: "sk-org", Enabled: true})

	e.models("ap-a")
	e.models("ap-a")
	if n := openaiUp.calls.Load(); n != 1 {
		t.Errorf("the same credential asked %d times, want 1", n)
	}
	e.models("ap-a-own") // its own OpenAI key: a different list
	if n := openaiUp.calls.Load(); n != 2 {
		t.Errorf("a different credential reused another's list (%d calls)", n)
	}
}
