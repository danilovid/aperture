package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CustomProvider is a user-defined OpenAI-compatible upstream (DeepSeek, Qwen,
// Moonshot, GLM, a local Ollama/vLLM, or any private endpoint). Requests whose
// model matches one of Prefixes are routed here.
type CustomProvider struct {
	Name     string   `json:"name"`
	BaseURL  string   `json:"base_url"`
	Prefixes []string `json:"prefixes"`
	// APIKey is the upstream credential. Local endpoints (Ollama, vLLM) usually
	// ignore auth — set any placeholder there so the provider stays configured.
	APIKey string `json:"api_key"`
}

// builtinProviderNames are reserved and cannot be reused by a custom provider.
var builtinProviderNames = map[string]bool{"openai": true, "anthropic": true, "groq": true}

// parseCustomProviders reads the CUSTOM_PROVIDERS env value: a JSON array of
// CustomProvider objects. Empty input yields no providers.
func parseCustomProviders(raw string) ([]CustomProvider, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var providers []CustomProvider
	if err := json.Unmarshal([]byte(raw), &providers); err != nil {
		return nil, fmt.Errorf("CUSTOM_PROVIDERS is not valid JSON: %w", err)
	}

	seen := map[string]bool{}
	for i := range providers {
		p := &providers[i]
		p.Name = strings.ToLower(strings.TrimSpace(p.Name))
		p.BaseURL = strings.TrimSpace(p.BaseURL)
		switch {
		case p.Name == "":
			return nil, fmt.Errorf("custom provider #%d: name is required", i+1)
		case builtinProviderNames[p.Name]:
			return nil, fmt.Errorf("custom provider %q: name collides with a built-in provider", p.Name)
		case seen[p.Name]:
			return nil, fmt.Errorf("custom provider %q: duplicate name", p.Name)
		case p.BaseURL == "":
			return nil, fmt.Errorf("custom provider %q: base_url is required", p.Name)
		case len(p.Prefixes) == 0:
			return nil, fmt.Errorf("custom provider %q: at least one model prefix is required", p.Name)
		}
		seen[p.Name] = true
	}
	return providers, nil
}
