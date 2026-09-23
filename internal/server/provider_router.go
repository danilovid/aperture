package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danilovid/mutegate/internal/config"
	"github.com/danilovid/mutegate/internal/interceptor"
	"github.com/danilovid/mutegate/internal/provider"
	"github.com/danilovid/mutegate/internal/provider/anthropic"
	"github.com/danilovid/mutegate/internal/provider/groq"
	"github.com/danilovid/mutegate/internal/provider/openai"
	"github.com/danilovid/mutegate/internal/storage"
)

// Where a request goes, and how.
//
// With a database, an organization's providers are the source of truth: each
// says where the upstream is, which key it uses, and which proxy the traffic
// leaves through. Without one, the environment says it for everybody, as it
// always has. Either way every path that reaches an upstream — chat
// completions, the native Anthropic and Responses APIs, Jev — asks this file,
// so the answer cannot differ between them.

// modelToLLM routes a model name to a built-in provider by prefix.
func modelToLLM(model string) string {
	model = strings.ToLower(model)
	switch {
	case strings.HasPrefix(model, "claude"):
		return "anthropic"
	case strings.HasPrefix(model, "llama"), strings.HasPrefix(model, "mixtral"):
		return "groq"
	default:
		return "openai"
	}
}

// upstream is one provider as resolved for one request.
type upstream struct {
	// Name is what incidents, usage rows and metrics call it.
	Name string
	Kind storage.ProviderKind
	// BaseURL empty means the client's documented default.
	BaseURL string
	APIKey  string
	Client  *http.Client
}

var (
	errNoProviderKey    = errors.New("no API key configured")
	errProviderDisabled = errors.New("provider disabled")
	// errProviderIncomplete is an OpenAI-compatible provider with no
	// address: saving one refuses that, so this is a store edited by hand.
	errProviderIncomplete = errors.New("provider has no address")
)

// providerKeyEnv is where the operator can put a built-in provider's key for
// the default organization, for the error that says a key is missing.
var providerKeyEnv = map[string]string{
	"openai":    "OPENAI_API_KEY",
	"anthropic": "ANTHROPIC_API_KEY",
	"groq":      "GROQ_API_KEY",
	"jev":       "JEV_API_KEY",
}

// upstreamErrorText says why a request could not be sent, in words an agent's
// operator can act on.
func upstreamErrorText(name string, err error) string {
	switch {
	case errors.Is(err, errProviderDisabled):
		return "the " + name + " provider is disabled for this organization"
	case errors.Is(err, errProviderIncomplete):
		return "the " + name + " provider has no address configured"
	case errors.Is(err, errNoProviderKey):
		msg := "no API key configured for " + name + ". Add it under Providers in the console, or on this Mutegate key"
		if env := providerKeyEnv[name]; env != "" {
			msg += ", or set " + env
		}
		return msg + "."
	default:
		return "the " + name + " provider is misconfigured: " + err.Error()
	}
}

// providerCacheTTL is how stale an organization's provider list may be. The
// list is read on every request; a few seconds of caching turns that into a
// read every few seconds, and a change made here is dropped from the cache at
// once, so only another instance can lag — by at most this long.
const providerCacheTTL = 5 * time.Second

type providerCache struct {
	mu      sync.Mutex
	entries map[string]providerCacheEntry
}

type providerCacheEntry struct {
	list    []storage.ProviderConfig
	expires time.Time
}

func (h *Handlers) orgProviders(ctx context.Context, orgID string) []storage.ProviderConfig {
	if h.ProviderStore == nil {
		return nil
	}
	h.providers.mu.Lock()
	if e, ok := h.providers.entries[orgID]; ok && time.Now().Before(e.expires) {
		h.providers.mu.Unlock()
		return e.list
	}
	h.providers.mu.Unlock()

	list, err := h.ProviderStore.ListProviders(ctx, orgID)
	if err != nil {
		// Serving with no providers would send every request to the
		// defaults with no key; better to say so and let the next request
		// try again.
		h.Logger.Error("provider list failed", "err", err, "org", orgID)
		return nil
	}
	h.providers.mu.Lock()
	if h.providers.entries == nil {
		h.providers.entries = map[string]providerCacheEntry{}
	}
	h.providers.entries[orgID] = providerCacheEntry{list: list, expires: time.Now().Add(providerCacheTTL)}
	h.providers.mu.Unlock()
	return list
}

func (h *Handlers) forgetProviders(orgID string) {
	h.providers.mu.Lock()
	delete(h.providers.entries, orgID)
	h.providers.mu.Unlock()
}

// route names the provider a model goes to in an organization, and returns
// its configuration when the organization has one.
func (h *Handlers) route(ctx context.Context, orgID, model string) (string, storage.ProviderKind, *storage.ProviderConfig) {
	m := strings.ToLower(model)
	list := h.orgProviders(ctx, orgID)

	// An OpenAI-compatible provider claims models by prefix. The longest
	// match wins, so "deepseek-coder" can go somewhere other than "deepseek".
	var best *storage.ProviderConfig
	for i := range list {
		p := &list[i]
		if p.Kind != storage.KindCompatible {
			continue
		}
		for _, pfx := range p.Prefixes {
			pfx = strings.ToLower(pfx)
			if pfx != "" && strings.HasPrefix(m, pfx) && (best == nil || len(pfx) > longestPrefix(best, m)) {
				best = p
			}
		}
	}
	if best != nil {
		return best.Name, storage.KindCompatible, best
	}

	// Without a database the environment's custom providers do the same job.
	if h.ProviderStore == nil {
		for _, cp := range h.CustomProviders {
			for _, pfx := range cp.Prefixes {
				if strings.HasPrefix(m, strings.ToLower(pfx)) {
					return cp.Name, storage.KindCompatible, nil
				}
			}
		}
	}

	kind := storage.ProviderKind(modelToLLM(model))
	return string(kind), kind, builtin(list, kind)
}

func longestPrefix(p *storage.ProviderConfig, model string) int {
	n := 0
	for _, pfx := range p.Prefixes {
		pfx = strings.ToLower(pfx)
		if strings.HasPrefix(model, pfx) && len(pfx) > n {
			n = len(pfx)
		}
	}
	return n
}

// builtin finds the organization's row for a built-in kind: named after it.
func builtin(list []storage.ProviderConfig, kind storage.ProviderKind) *storage.ProviderConfig {
	for i := range list {
		if list[i].Name == string(kind) && list[i].Kind == kind {
			return &list[i]
		}
	}
	return nil
}

// providerName is what a model's provider is called, for the records a
// request leaves behind before it has been routed anywhere.
func (h *Handlers) providerName(ctx context.Context, orgID, model string) string {
	name, _, _ := h.route(ctx, orgID, model)
	return name
}

// resolveLLM is providerName without an organization — the environment's
// answer, for the few places that have no request to hand.
func (h *Handlers) resolveLLM(model string) string {
	return h.providerName(context.Background(), storage.DefaultOrgID, model)
}

func (h *Handlers) customByName(name string) *config.CustomProvider {
	for i := range h.CustomProviders {
		if h.CustomProviders[i].Name == name {
			return &h.CustomProviders[i]
		}
	}
	return nil
}

// upstreamFor resolves the provider a model goes to. kind is set by the paths
// that speak one provider's native API; the chat path leaves it empty and
// routes by model.
func (h *Handlers) upstreamFor(ctx context.Context, key *storage.Key, model string, kind storage.ProviderKind) (*upstream, error) {
	var name string
	var cfg *storage.ProviderConfig
	if kind == "" {
		name, kind, cfg = h.route(ctx, key.OrgID, model)
	} else {
		name = string(kind)
		cfg = builtin(h.orgProviders(ctx, key.OrgID), kind)
	}
	return h.buildUpstream(key, name, kind, cfg)
}

// buildUpstream is the second half of upstreamFor: with the provider decided,
// which credential, which address and which way out.
func (h *Handlers) buildUpstream(key *storage.Key, name string, kind storage.ProviderKind, cfg *storage.ProviderConfig) (*upstream, error) {
	if cfg != nil && !cfg.Enabled {
		return nil, errProviderDisabled
	}

	u := &upstream{Name: name, Kind: kind}

	// The Mutegate key's own credential wins: a key can be issued with its
	// own provider account. Otherwise the organization's.
	u.APIKey = key.Providers[name]
	if u.APIKey == "" && cfg != nil {
		u.APIKey = cfg.APIKey
	}
	if u.APIKey == "" {
		return nil, errNoProviderKey
	}

	// Where: the organization's address, else the installation's default for
	// the kind, else the client's own.
	if cfg != nil && cfg.BaseURL != "" {
		u.BaseURL = cfg.BaseURL
	} else {
		switch kind {
		case storage.KindOpenAI:
			u.BaseURL = h.OpenAIBaseURL
		case storage.KindAnthropic:
			u.BaseURL = h.AnthropicBaseURL
		case storage.KindJev:
			u.BaseURL = h.JevBaseURL
		case storage.KindCompatible:
			if cp := h.customByName(name); cp != nil {
				u.BaseURL = cp.BaseURL
			}
		}
	}
	if kind == storage.KindCompatible && u.BaseURL == "" {
		return nil, errProviderIncomplete
	}

	// How: through the provider's own proxy and timeout, or the defaults.
	proxy, timeout := "", time.Duration(0)
	if cfg != nil {
		proxy = cfg.ProxyURL
		timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	}
	client, err := h.transports.Client(proxy, timeout)
	if err != nil {
		return nil, err
	}
	u.Client = client
	return u, nil
}

// chatProvider builds the chat-completions client for an upstream.
func chatProvider(u *upstream) provider.Provider {
	switch u.Kind {
	case storage.KindOpenAI:
		return openai.New(u.BaseURL, u.APIKey).WithHTTPClient(u.Client)
	case storage.KindAnthropic:
		return anthropic.New(u.BaseURL, u.APIKey).WithHTTPClient(u.Client)
	case storage.KindGroq:
		return groq.New(u.BaseURL, u.APIKey).WithHTTPClient(u.Client)
	default:
		return openai.NewCompat(u.BaseURL, u.APIKey).WithHTTPClient(u.Client)
	}
}

// resolveProviderForKey is the chat-completions path's provider, metered.
func (h *Handlers) resolveProviderForKey(ctx context.Context, key *storage.Key, m reqMeta) (provider.Provider, error) {
	u, err := h.upstreamFor(ctx, key, m.model, "")
	if err != nil {
		return nil, err
	}
	inner := chatProvider(u)

	// The interceptor meters tokens and feeds budgets and metrics; it is worth
	// wrapping even without a LogStore, since those still need the numbers.
	if h.LogStore != nil || h.Tracker != nil || h.Metrics != nil {
		return interceptor.New(inner, h.LogStore, storage.LogEntry{
			OrgID:    m.orgID,
			Model:    m.model,
			Provider: u.Name,
			KeyID:    key.ID,
			Agent:    m.agent,
			Session:  m.session,
		}, h.observeUsage), nil
	}
	return inner, nil
}
