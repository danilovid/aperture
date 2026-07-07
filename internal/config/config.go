package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/inspector"
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
	// DLPEnabled turns outbound content scanning on (default true).
	DLPEnabled bool
	// DLPPolicy maps detector groups to actions.
	DLPPolicy inspector.Policy
	// Alert is the initial webhook alerting config (empty URL = disabled).
	Alert alerter.Config
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

	dlpEnabled := true
	if v := os.Getenv("DLP_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid DLP_ENABLED: %w", err)
		}
		dlpEnabled = b
	}

	policy := inspector.DefaultPolicy()
	for envName, target := range map[string]*inspector.Action{
		"DLP_SECRETS_ACTION": &policy.Secrets,
		"DLP_PII_ACTION":     &policy.PII,
		"DLP_CUSTOM_ACTION":  &policy.Custom,
	} {
		if v := os.Getenv(envName); v != "" {
			if !inspector.ValidAction(v) {
				return nil, fmt.Errorf("invalid %s: %q (want off|alert|redact|block)", envName, v)
			}
			*target = inspector.Action(v)
		}
	}

	alert := alerter.Config{
		URL:    os.Getenv("DLP_WEBHOOK_URL"),
		Format: alerter.Format(os.Getenv("DLP_WEBHOOK_FORMAT")),
		ChatID: os.Getenv("DLP_WEBHOOK_CHAT_ID"),
	}
	if alert.Format == "" {
		alert.Format = alerter.FormatJSON
	}
	if !alerter.ValidFormat(string(alert.Format)) {
		return nil, fmt.Errorf("invalid DLP_WEBHOOK_FORMAT: %q (want json|slack|telegram)", alert.Format)
	}
	if v := os.Getenv("DLP_WEBHOOK_ACTIONS"); v != "" {
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				alert.Actions = append(alert.Actions, a)
			}
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
		DLPEnabled:     dlpEnabled,
		DLPPolicy:      policy,
		Alert:          alert,
	}, nil
}
