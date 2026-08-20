package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/ner"
)

// Config holds application configuration.
type Config struct {
	Port          int
	Env           string
	OpenAIBaseURL string
	// AnthropicBaseURL overrides the upstream for the native Messages API.
	AnthropicBaseURL string
	DatabaseURL      string
	AdminAPIKey      string
	ApertureAPIKey   string
	AllowedOrigins   []string
	// ProviderKeys holds provider API keys from env (fallback when no DB):
	// "openai", "anthropic", "groq".
	ProviderKeys map[string]string
	// CustomProviders are user-defined OpenAI-compatible upstreams from
	// CUSTOM_PROVIDERS (DeepSeek, Qwen, Ollama, private endpoints, …).
	CustomProviders []CustomProvider
	// DLPEnabled turns outbound content scanning on (default true).
	DLPEnabled bool
	// DLPPolicy maps detector groups to actions.
	DLPPolicy inspector.Policy
	// Alert is the initial webhook alerting config (empty URL = disabled).
	Alert alerter.Config
	// Limits are the default per-key ceilings; zero values mean "no limit".
	Limits limits.Limits
	// NER configures the local model service for free-form PII. An empty URL
	// leaves the stage off, whatever the policies say.
	NER ner.Config
	// EncryptionKey (64 hex chars) enables AES-GCM encryption of provider
	// keys at rest in PostgreSQL. Empty = plaintext (with a startup warning).
	EncryptionKey string
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

	if v := os.Getenv("DLP_NER"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid DLP_NER: %w", err)
		}
		policy.NER = b
	}

	if v := os.Getenv("DLP_SCAN_RESPONSES"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid DLP_SCAN_RESPONSES: %w", err)
		}
		policy.ScanResponses = b
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

	customProviders, err := parseCustomProviders(os.Getenv("CUSTOM_PROVIDERS"))
	if err != nil {
		return nil, err
	}

	var lim limits.Limits
	if v := os.Getenv("LIMIT_BUDGET_DAILY_USD"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return nil, fmt.Errorf("invalid LIMIT_BUDGET_DAILY_USD: %q", v)
		}
		lim.BudgetDailyUSD = f
	}
	if v := os.Getenv("LIMIT_REQUESTS_PER_MINUTE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid LIMIT_REQUESTS_PER_MINUTE: %q", v)
		}
		lim.RequestsPerMinute = n
	}

	nerCfg, err := loadNER()
	if err != nil {
		return nil, err
	}

	return &Config{
		Port:             port,
		Env:              env,
		OpenAIBaseURL:    baseURL,
		AnthropicBaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		AdminAPIKey:      os.Getenv("ADMIN_API_KEY"),
		ApertureAPIKey:   os.Getenv("APERTURE_API_KEY"),
		CustomProviders:  customProviders,
		AllowedOrigins:   origins,
		ProviderKeys:     providerKeys,
		DLPEnabled:       dlpEnabled,
		DLPPolicy:        policy,
		Alert:            alert,
		Limits:           lim,
		NER:              nerCfg,
		EncryptionKey:    os.Getenv("APERTURE_ENCRYPTION_KEY"),
	}, nil
}

// loadNER reads the model-service settings. Everything but the URL has a
// working default, so turning the stage on is one variable.
func loadNER() (ner.Config, error) {
	cfg := ner.Config{
		URL:   os.Getenv("NER_URL"),
		Token: os.Getenv("NER_TOKEN"),
		// Long prompts cost the model hundreds of milliseconds. A tight
		// timeout would quietly skip exactly the traffic worth scanning, so
		// the default is generous and the operator can tighten it.
		Timeout: time.Second,
		// Below this the model is guessing, and a wrong redaction is worse
		// than a missed one for names.
		MinScore: 0.5,
	}
	if cfg.URL == "" {
		return cfg, nil
	}
	if v := os.Getenv("NER_TIMEOUT_MS"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			return cfg, fmt.Errorf("invalid NER_TIMEOUT_MS: %q (want a positive number)", v)
		}
		cfg.Timeout = time.Duration(ms) * time.Millisecond
	}
	if v := os.Getenv("NER_MIN_SCORE"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			return cfg, fmt.Errorf("invalid NER_MIN_SCORE: %q (want 0..1)", v)
		}
		cfg.MinScore = f
	}
	if v := os.Getenv("NER_LABELS"); v != "" {
		for _, l := range strings.Split(v, ",") {
			if l = strings.TrimSpace(l); l != "" {
				cfg.Labels = append(cfg.Labels, l)
			}
		}
	}
	for envName, target := range map[string]*bool{
		"NER_FAIL_CLOSED":  &cfg.FailClosed,
		"NER_ALLOW_REMOTE": &cfg.AllowRemote,
	} {
		if v := os.Getenv(envName); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return cfg, fmt.Errorf("invalid %s: %w", envName, err)
			}
			*target = b
		}
	}
	return cfg, nil
}
