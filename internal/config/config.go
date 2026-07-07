package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds application configuration.
type Config struct {
	Port           int
	Env            string
	OpenAIBaseURL  string
	DatabaseURL    string
	AdminAPIKey    string
	ApertureAPIKey string
	AllowedOrigins []string
	// ProviderKeys holds provider API keys from env (fallback when no DB):
	// "openai", "anthropic", "groq".
	ProviderKeys map[string]string
}

const defaultOpenAIBaseURL = "https://api.openai.com"

// defaultAllowedOrigins covers local development of the web UI (vite dev + preview).
var defaultAllowedOrigins = []string{"http://localhost:5173", "http://localhost:4173"}

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

	origins := defaultAllowedOrigins
	if v := os.Getenv("ALLOWED_ORIGINS"); v != "" {
		origins = nil
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
	}

	providerKeys := map[string]string{}
	for llm, envName := range map[string]string{
		"openai":    "OPENAI_API_KEY",
		"anthropic": "ANTHROPIC_API_KEY",
		"groq":      "GROQ_API_KEY",
	} {
		if v := os.Getenv(envName); v != "" {
			providerKeys[llm] = v
		}
	}

	return &Config{
		Port:           port,
		Env:            env,
		OpenAIBaseURL:  baseURL,
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AdminAPIKey:    os.Getenv("ADMIN_API_KEY"),
		ApertureAPIKey: os.Getenv("APERTURE_API_KEY"),
		AllowedOrigins: origins,
		ProviderKeys:   providerKeys,
	}, nil
}
