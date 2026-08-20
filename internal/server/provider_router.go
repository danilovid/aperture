package server

import (
	"strings"

	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/interceptor"
	"github.com/danilovid/aperture/internal/provider"
	"github.com/danilovid/aperture/internal/provider/anthropic"
	"github.com/danilovid/aperture/internal/provider/groq"
	"github.com/danilovid/aperture/internal/provider/openai"
	"github.com/danilovid/aperture/internal/storage"
)

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

// resolveLLM returns the provider name for a model: custom providers are
// matched first (by prefix, in configured order), then the built-ins.
func (h *Handlers) resolveLLM(model string) string {
	m := strings.ToLower(model)
	for _, cp := range h.CustomProviders {
		for _, pfx := range cp.Prefixes {
			if strings.HasPrefix(m, strings.ToLower(pfx)) {
				return cp.Name
			}
		}
	}
	return modelToLLM(model)
}

func (h *Handlers) customByName(name string) *config.CustomProvider {
	for i := range h.CustomProviders {
		if h.CustomProviders[i].Name == name {
			return &h.CustomProviders[i]
		}
	}
	return nil
}

func (h *Handlers) resolveProviderForKey(key *storage.Key, m reqMeta) (provider.Provider, bool) {
	llm := h.resolveLLM(m.model)
	apiKey := key.Providers[llm]
	if apiKey == "" {
		return nil, false
	}

	var inner provider.Provider
	switch llm {
	case "openai":
		inner = openai.New(h.OpenAIBaseURL, apiKey)
	case "anthropic":
		inner = anthropic.New("", apiKey)
	case "groq":
		inner = groq.New("", apiKey)
	default:
		cp := h.customByName(llm)
		if cp == nil {
			return nil, false
		}
		inner = openai.NewCompat(cp.BaseURL, apiKey)
	}

	// The interceptor meters tokens and feeds the budget tracker; it is worth
	// wrapping even without a LogStore, since budgets still need the cost.
	if h.LogStore != nil || h.Tracker != nil {
		var onCost func(string, float64)
		if h.Tracker != nil {
			onCost = h.Tracker.AddSpend
		}
		return interceptor.New(inner, h.LogStore, storage.LogEntry{
			Model:    m.model,
			Provider: llm,
			KeyID:    key.ID,
			Agent:    m.agent,
			Session:  m.session,
		}, onCost), true
	}
	return inner, true
}
