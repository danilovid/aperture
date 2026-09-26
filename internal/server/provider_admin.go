package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/mutegate/mutegate/internal/provider"
	"github.com/mutegate/mutegate/internal/secrets"
	"github.com/mutegate/mutegate/internal/storage"
)

// The providers screen: where an organization says which upstreams it uses,
// with which keys, through which proxies.

// providerView is a provider as the console is shown it. The key and the
// proxy are secrets, so the view says whether they are set and shows just
// enough to tell one from another — never the value.
type providerView struct {
	Name    string               `json:"name"`
	Kind    storage.ProviderKind `json:"kind"`
	BaseURL string               `json:"base_url,omitempty"`
	// EffectiveURL is where requests actually go: the provider's own
	// address, else the installation's default, else the documented one.
	EffectiveURL string   `json:"effective_url"`
	KeySet       bool     `json:"key_set"`
	KeyHint      string   `json:"key_hint,omitempty"`
	Proxy        string   `json:"proxy,omitempty"`
	Prefixes     []string `json:"prefixes,omitempty"`
	TimeoutMS    int      `json:"timeout_ms,omitempty"`
	Enabled      bool     `json:"enabled"`
	UpdatedAt    string   `json:"updated_at"`
}

var documentedURLs = map[storage.ProviderKind]string{
	storage.KindOpenAI:    "https://api.openai.com",
	storage.KindAnthropic: "https://api.anthropic.com",
	storage.KindGroq:      "https://api.groq.com/openai/v1",
	storage.KindJev:       "https://www.jevai.org",
}

// defaultURL is the installation's address for a built-in kind: the
// environment's override, else the documented one.
func (h *Handlers) defaultURL(kind storage.ProviderKind) string {
	env := map[storage.ProviderKind]string{
		storage.KindOpenAI:    h.OpenAIBaseURL,
		storage.KindAnthropic: h.AnthropicBaseURL,
		storage.KindJev:       h.JevBaseURL,
	}[kind]
	if env != "" {
		return env
	}
	return documentedURLs[kind]
}

func (h *Handlers) viewOf(p storage.ProviderConfig) providerView {
	v := providerView{
		Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, KeySet: p.APIKey != "",
		Prefixes: p.Prefixes, TimeoutMS: p.TimeoutMS, Enabled: p.Enabled,
		UpdatedAt:    p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		EffectiveURL: p.BaseURL,
	}
	if v.EffectiveURL == "" {
		v.EffectiveURL = h.defaultURL(p.Kind)
	}
	if p.APIKey != "" {
		v.KeyHint = secrets.Hint(p.APIKey)
	}
	if p.ProxyURL != "" {
		v.Proxy = provider.RedactProxy(p.ProxyURL)
	}
	return v
}

func (h *Handlers) requireProviderStore(w http.ResponseWriter, r *http.Request, min storage.Role) (string, bool) {
	orgID, ok := h.adminOrg(w, r, min)
	if !ok {
		return "", false
	}
	if h.ProviderStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "providers are configured through the environment on a gateway without a database",
		})
		return "", false
	}
	return orgID, true
}

// GET /admin/providers — the organization's providers, and the built-ins it
// has not set up yet, with where each would send requests.
func (h *Handlers) handleProvidersList(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireProviderStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	list, err := h.ProviderStore.ListProviders(r.Context(), orgID)
	if err != nil {
		h.Logger.Error("list providers failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load providers"})
		return
	}
	views := make([]providerView, 0, len(list))
	have := map[string]bool{}
	for _, p := range list {
		views = append(views, h.viewOf(p))
		have[p.Name] = true
	}
	type available struct {
		Kind         storage.ProviderKind `json:"kind"`
		EffectiveURL string               `json:"effective_url"`
	}
	var more []available
	for _, k := range storage.BuiltinKinds {
		if !have[string(k)] {
			more = append(more, available{Kind: k, EffectiveURL: h.defaultURL(k)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": views, "available": more})
}

var providerNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

// providerRequest is a save, and every field is optional: what is sent
// changes, what is not stays as stored. So turning a provider off is
// {"enabled": false}, not a resend of everything else — which would be one
// forgotten field away from wiping an address. The key and the proxy in
// particular can only ever be sent, never read back, so "absent" must mean
// "keep"; an empty string clears them.
type providerRequest struct {
	Kind      *string   `json:"kind"`
	BaseURL   *string   `json:"base_url"`
	APIKey    *string   `json:"api_key"`
	ProxyURL  *string   `json:"proxy_url"`
	Prefixes  *[]string `json:"prefixes"`
	TimeoutMS *int      `json:"timeout_ms"`
	Enabled   *bool     `json:"enabled"`
}

// buildProvider lays a save over what is stored, and checks the result is
// something requests could actually be sent to.
func buildProvider(name string, req providerRequest, stored *storage.ProviderConfig) (storage.ProviderConfig, error) {
	p := storage.ProviderConfig{Name: name, Enabled: true}
	if stored != nil {
		p = *stored
		p.Name = name
	}
	if req.Kind != nil {
		p.Kind = storage.ProviderKind(strings.TrimSpace(*req.Kind))
	}
	if !storage.ValidKind(string(p.Kind)) {
		return storage.ProviderConfig{}, errors.New("unknown kind (want openai, anthropic, groq, jev or openai-compatible)")
	}
	// A built-in is named after its kind: that is the name a Mutegate key's
	// own provider keys are filed under, and there is one of each.
	if p.Kind.Builtin() && name != string(p.Kind) {
		return storage.ProviderConfig{}, errors.New("a built-in provider is named after its kind: " + string(p.Kind))
	}
	if !p.Kind.Builtin() {
		if !providerNamePattern.MatchString(name) {
			return storage.ProviderConfig{}, errors.New("name must be lowercase letters, digits and dashes, 1–40 characters")
		}
		if storage.ProviderKind(name).Builtin() {
			return storage.ProviderConfig{}, errors.New(name + " is reserved for the built-in provider")
		}
	}

	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if req.APIKey != nil {
		p.APIKey = strings.TrimSpace(*req.APIKey)
	}
	if req.ProxyURL != nil {
		p.ProxyURL = strings.TrimSpace(*req.ProxyURL)
	}
	if p.ProxyURL != "" {
		if _, err := provider.ParseProxyURL(p.ProxyURL); err != nil {
			return storage.ProviderConfig{}, err
		}
	}
	if req.BaseURL != nil {
		p.BaseURL = strings.TrimRight(strings.TrimSpace(*req.BaseURL), "/")
	}
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return storage.ProviderConfig{}, errors.New("base_url must be an http or https address")
		}
		if u.User != nil {
			// A key in an address is a key in every log line that prints it.
			return storage.ProviderConfig{}, errors.New("put credentials in the API key, not in base_url")
		}
	}
	if req.TimeoutMS != nil {
		p.TimeoutMS = *req.TimeoutMS
	}
	if p.TimeoutMS != 0 && (p.TimeoutMS < 1000 || p.TimeoutMS > 600000) {
		return storage.ProviderConfig{}, errors.New("timeout_ms must be between 1000 and 600000, or 0 for the default")
	}

	if req.Prefixes != nil {
		p.Prefixes = nil
		for _, pfx := range *req.Prefixes {
			if pfx = strings.ToLower(strings.TrimSpace(pfx)); pfx != "" {
				p.Prefixes = append(p.Prefixes, pfx)
			}
		}
	}
	if p.Kind == storage.KindCompatible {
		if p.BaseURL == "" {
			return storage.ProviderConfig{}, errors.New("an OpenAI-compatible provider needs its base_url, including any version path, e.g. https://api.deepseek.com/v1")
		}
		if len(p.Prefixes) == 0 {
			return storage.ProviderConfig{}, errors.New("an OpenAI-compatible provider needs at least one model prefix, e.g. deepseek-")
		}
	} else {
		// Built-ins route by their own rules; prefixes would only mislead.
		p.Prefixes = nil
	}
	return p, nil
}

// PUT /admin/providers/{name}
func (h *Handlers) handleProviderPut(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireProviderStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	stored, err := h.ProviderStore.GetProvider(r.Context(), orgID, name)
	if err != nil && !errors.Is(err, storage.ErrProviderNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load the provider"})
		return
	}
	if errors.Is(err, storage.ErrProviderNotFound) {
		stored = nil
	}
	p, err := buildProvider(name, req, stored)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := h.checkPrefixes(r.Context(), orgID, p); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if err := h.ProviderStore.PutProvider(r.Context(), orgID, p); err != nil {
		h.Logger.Error("save provider failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not save the provider"})
		return
	}
	h.forgetProviders(orgID)
	h.Logger.Info("provider saved", "org", orgID, "provider", p.Name, "kind", p.Kind,
		"proxy", provider.RedactProxy(p.ProxyURL), "enabled", p.Enabled)
	// Adding a provider is always an event, even with nothing but defaults;
	// saving one unchanged is not.
	if stored == nil {
		h.audit(r, orgID, "provider.create", p.Name, providerChange(nil, p))
	} else {
		h.auditChange(r, orgID, "provider.update", p.Name, providerChange(stored, p))
	}
	saved, _ := h.ProviderStore.GetProvider(r.Context(), orgID, p.Name)
	if saved == nil {
		saved = &p
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": h.viewOf(*saved)})
}

// checkPrefixes refuses two providers claiming the same prefix. Longer
// prefixes may overlap shorter ones — the longest match wins — but an exact
// tie has no winner, and a request would go wherever the list happened to be
// read from first.
func (h *Handlers) checkPrefixes(ctx context.Context, orgID string, p storage.ProviderConfig) error {
	if p.Kind != storage.KindCompatible {
		return nil
	}
	list, err := h.ProviderStore.ListProviders(ctx, orgID)
	if err != nil {
		return nil
	}
	for _, other := range list {
		if other.Name == p.Name || other.Kind != storage.KindCompatible {
			continue
		}
		for _, a := range p.Prefixes {
			for _, b := range other.Prefixes {
				if a == strings.ToLower(b) {
					return errors.New("the prefix " + a + " already routes to " + other.Name)
				}
			}
		}
	}
	return nil
}

// DELETE /admin/providers/{name} — a built-in falls back to the installation's
// defaults, with no key of its own.
func (h *Handlers) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireProviderStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := h.ProviderStore.DeleteProvider(r.Context(), orgID, name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such provider"})
		return
	}
	h.forgetProviders(orgID)
	h.audit(r, orgID, "provider.delete", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/providers/test — the connectivity check. It tests whatever the
// console is showing: the saved provider, with any unsaved edits laid over
// it, so a person can try a proxy before committing to it.
func (h *Handlers) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.requireProviderStore(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		providerRequest
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if name == "" && req.Kind != nil {
		name = *req.Kind
	}
	stored, err := h.ProviderStore.GetProvider(r.Context(), orgID, name)
	if err != nil {
		stored = nil
	}
	p, err := buildProvider(name, req.providerRequest, stored)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	base := p.BaseURL
	if base == "" {
		base = h.defaultURL(p.Kind)
	}
	res := provider.Probe(r.Context(), provider.ProbeRequest{
		Kind: string(p.Kind), BaseURL: base, APIKey: p.APIKey, ProxyURL: p.ProxyURL,
	})
	if p.APIKey == "" && res.Stage == provider.StageAuth {
		// A refused empty key is not news; the connection is.
		res.Message = "reached " + res.Target + "; there is no key yet to check"
	}
	h.Logger.Info("provider connectivity check", "org", orgID, "provider", p.Name,
		"stage", res.Stage, "status", res.Status, "latency_ms", res.LatencyMS, "via", res.Via)
	writeJSON(w, http.StatusOK, res)
}
