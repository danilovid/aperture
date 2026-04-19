package server

import (
	"strings"

	"github.com/danilovid/aperture/internal/interceptor"
	"github.com/danilovid/aperture/internal/provider"
	"github.com/danilovid/aperture/internal/provider/anthropic"
	"github.com/danilovid/aperture/internal/provider/groq"
	"github.com/danilovid/aperture/internal/provider/openai"
	"github.com/danilovid/aperture/internal/storage"
)

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

func (h *Handlers) resolveProviderForKey(key *storage.Key, model string) (provider.Provider, bool) {
	llm := modelToLLM(model)
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
		return nil, false
	}

	if h.LogStore != nil {
		return interceptor.New(inner, h.LogStore, model, llm, key.ID), true
	}
	return inner, true
}
