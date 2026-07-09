package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application configuration.
type Config struct {
	Port            int
	Env             string
	ApertureAPIKey  string
	OpenAIBaseURL   string
	OpenAIAPIKey    string
	AnthropicAPIKey string
	GroqAPIKey      string
	DatabaseURL     string
	AdminAPIKey     string
}

const defaultOpenAIBaseURL = "https://api.openai.com"

// Load reads configuration from environment.
func Load() (*Config, error) {
	port := 8080
	if v := os.Getenv("PORT"); v != "" {
		var err error
		port, err = strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}

	apertureAPIKey := os.Getenv("APERTURE_API_KEY")
	if apertureAPIKey == "" {
		apertureAPIKey = "dev"
	}

	return &Config{
		Port:            port,
		Env:             env,
		ApertureAPIKey:  apertureAPIKey,
		OpenAIBaseURL:   baseURL,
		OpenAIAPIKey:    os.Getenv("OPENAI_API_KEY"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		GroqAPIKey:      os.Getenv("GROQ_API_KEY"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		AdminAPIKey:     os.Getenv("ADMIN_API_KEY"),
	}, nil
}

func (c *Config) ProviderKeys() map[string]string {
	return map[string]string{
		"openai":    c.OpenAIAPIKey,
		"anthropic": c.AnthropicAPIKey,
		"groq":      c.GroqAPIKey,
	}
}
