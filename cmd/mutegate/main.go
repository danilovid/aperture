package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mutegate/mutegate/internal/alerter"
	"github.com/mutegate/mutegate/internal/config"
	"github.com/mutegate/mutegate/internal/inspector"
	"github.com/mutegate/mutegate/internal/limits"
	"github.com/mutegate/mutegate/internal/metrics"
	"github.com/mutegate/mutegate/internal/ner"
	"github.com/mutegate/mutegate/internal/oauth"
	"github.com/mutegate/mutegate/internal/secrets"
	"github.com/mutegate/mutegate/internal/server"
	"github.com/mutegate/mutegate/internal/storage"
	"github.com/mutegate/mutegate/internal/storage/postgres"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("mutegate starting", "version", version)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	for _, name := range cfg.LegacyEnv {
		slog.Warn("reading an environment variable by its name from before the rename; it still works, but rename it",
			"variable", name, "rename_to", "MUTEGATE_"+strings.TrimPrefix(name, "APERTURE_"))
	}

	if cfg.AdminAPIKey == "" {
		cfg.AdminAPIKey = config.GenerateKey("admin")
		slog.Warn("ADMIN_API_KEY not set — generated a key for this run; set the env var to make it stable",
			"admin_api_key", cfg.AdminAPIKey)
	}

	var ks storage.KeyStore
	var accounts storage.AccountStore
	var auditLog storage.AuditStore
	var ls storage.LogStore
	var ps storage.PolicyStore
	var ds storage.DLPStore
	var lims storage.LimitStore
	var providers storage.ProviderStore
	var alertStore storage.AlertStore
	var readyCheck func(ctx context.Context) error

	if cfg.DatabaseURL != "" {
		var cipher *secrets.Cipher
		if cfg.EncryptionKey != "" {
			var err error
			cipher, err = secrets.NewCipher(cfg.EncryptionKey)
			if err != nil {
				slog.Error("invalid MUTEGATE_ENCRYPTION_KEY", "err", err)
				os.Exit(1)
			}
			slog.Info("provider keys encrypted at rest (AES-256-GCM)")
		} else {
			slog.Warn("MUTEGATE_ENCRYPTION_KEY not set — provider keys are stored in plaintext")
		}

		pool, err := postgres.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Warn("postgres unavailable, falling back to in-memory store", "err", err)
		} else {
			pgStore, err := postgres.NewKeyStore(context.Background(), pool, cipher)
			if err != nil {
				slog.Warn("key store init failed, falling back to in-memory store", "err", err)
				pool.Close()
			} else {
				ks = pgStore
				readyCheck = pool.Ping
				slog.Info("using PostgreSQL")
				// People, organizations and sessions. Without them the gateway
				// still serves agents; only the console's sign-in is unavailable.
				pgAccounts, err := postgres.NewAccountStore(context.Background(), pool)
				if err != nil {
					slog.Warn("account store init failed, sign-in disabled", "err", err)
				} else {
					accounts = pgAccounts
					// Who changed what. It lives beside the people it names,
					// so it needs their schema first.
					pgAudit, err := postgres.NewAuditStore(context.Background(), pool)
					if err != nil {
						slog.Warn("audit log init failed, changes will not be journaled", "err", err)
					} else {
						auditLog = pgAudit
					}
				}
				// Each organization's upstreams. Without them requests fall back
				// to the environment's defaults, as with no database at all.
				pgProviders, err := postgres.NewProviderStore(context.Background(), pool, cipher)
				if err != nil {
					slog.Warn("provider store init failed, upstreams come from the environment only", "err", err)
				} else {
					providers = pgProviders
					seedDefaultProviders(context.Background(), pgProviders, cfg)
				}
				// Each organization's own alert webhook.
				pgAlerts, err := postgres.NewAlertStore(context.Background(), pool, cipher)
				if err != nil {
					slog.Warn("alert settings store init failed, organizations cannot set their own webhooks", "err", err)
				} else {
					alertStore = pgAlerts
				}
				pgLog, err := postgres.NewLogStore(context.Background(), pool)
				if err != nil {
					slog.Warn("log store init failed, monitoring disabled", "err", err)
				} else {
					ls = pgLog
				}
				if cfg.DLPEnabled {
					pgPol, err := postgres.NewPolicyStore(context.Background(), pool, cfg.DLPPolicy)
					if err != nil {
						slog.Warn("policy store init failed, policies won't persist", "err", err)
					} else {
						ps = pgPol
					}
					pgDLP, err := postgres.NewDLPStore(context.Background(), pool)
					if err != nil {
						slog.Warn("dlp store init failed, events won't persist", "err", err)
					} else {
						ds = pgDLP
					}
					pgLim, err := postgres.NewLimitStore(context.Background(), pool, cfg.Limits)
					if err != nil {
						slog.Warn("limit store init failed, limits won't persist", "err", err)
					} else {
						lims = pgLim
					}
				}
			}
		}
	}

	if ks == nil {
		mutegateKey := cfg.MutegateAPIKey
		if mutegateKey == "" {
			mutegateKey = config.GenerateKey("ap")
			slog.Warn("MUTEGATE_API_KEY not set — generated a key for this run; set the env var to make it stable",
				"mutegate_api_key", mutegateKey)
		}
		slog.Info("using in-memory store — provider keys are kept for the lifetime of the process")
		ks = config.NewRuntimeStore(mutegateKey).KeyStore()

		if len(cfg.ProviderKeys) > 0 {
			if err := ks.SetProviderKeys(context.Background(), storage.DefaultOrgID, cfg.ProviderKeys); err != nil {
				slog.Error("seeding provider keys from env failed", "err", err)
			} else {
				for llm := range cfg.ProviderKeys {
					slog.Info("provider key loaded from env", "provider", llm)
				}
			}
		}
	}

	// Without a database the environment's custom providers are routed to
	// directly; their keys live on the runtime key like the built-ins'.
	if len(cfg.CustomProviders) > 0 && providers == nil {
		customKeys := map[string]string{}
		for _, cp := range cfg.CustomProviders {
			if cp.APIKey != "" {
				customKeys[cp.Name] = cp.APIKey
			}
			slog.Info("custom provider registered", "name", cp.Name, "base_url", cp.BaseURL, "prefixes", cp.Prefixes)
		}
		if len(customKeys) > 0 {
			if err := ks.SetProviderKeys(context.Background(), storage.DefaultOrgID, customKeys); err != nil {
				slog.Error("seeding custom provider keys failed", "err", err)
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := metrics.New()

	var ins *inspector.Inspector
	var alrt *alerter.Alerter
	if cfg.DLPEnabled {
		ins = inspector.New()
		// The model stage for free-form PII runs outside the gateway; without
		// a URL it stays off no matter what the policies say.
		if cfg.NER.URL != "" {
			cfg.NER.Observe = reg.ObserveNER
			det, err := ner.New(cfg.NER)
			if err != nil {
				slog.Error("NER config rejected", "err", err)
				os.Exit(1)
			}
			ins = ins.WithDetector(det, cfg.NER.FailClosed)
			slog.Info("NER stage enabled", "url", cfg.NER.URL,
				"timeout", cfg.NER.Timeout, "min_score", cfg.NER.MinScore,
				"fail_closed", cfg.NER.FailClosed)
		}
		if ds == nil {
			ds = storage.NewMemDLPStore(1000)
		}
		if ps == nil {
			ps = storage.NewMemPolicyStore(cfg.DLPPolicy)
		}
		alrt = alerter.New(cfg.Alert, logger)
		if alertStore != nil {
			alrt.WithStore(alertStore)
		}
		go alrt.Run(ctx)
		if cfg.Alert.URL != "" {
			slog.Info("DLP webhook alerts enabled", "format", cfg.Alert.Format)
		}
		slog.Info("DLP scanning enabled",
			"secrets", cfg.DLPPolicy.Secrets, "pii", cfg.DLPPolicy.PII, "custom", cfg.DLPPolicy.Custom)
	} else {
		slog.Warn("DLP scanning disabled (DLP_ENABLED=false)")
	}

	// Budgets and rate limits. Today's spend is recovered from the request log
	// so a restart does not hand every key a fresh budget.
	if lims == nil {
		lims = storage.NewMemLimitStore(cfg.Limits)
	}
	var seed limits.SpendSeeder
	if ls != nil {
		seed = ls.CostSince
	}
	tracker := limits.NewTracker(seed)
	if !cfg.Limits.Empty() {
		slog.Info("default per-key limits",
			"budget_daily_usd", cfg.Limits.BudgetDailyUSD,
			"requests_per_minute", cfg.Limits.RequestsPerMinute)
	}

	// OAuth sign-in state is signed with a key derived from an installation
	// secret, so a sign-in started before a restart still completes after it.
	// The encryption key is preferred when there is one: it is the secret
	// meant for protecting things; the admin key is the fallback that always
	// exists.
	stateSecret := cfg.EncryptionKey
	if stateSecret == "" {
		stateSecret = cfg.AdminAPIKey
	}
	if len(cfg.OAuth) > 0 {
		names := make([]string, 0, len(cfg.OAuth))
		for _, p := range cfg.OAuth {
			names = append(names, p.ID)
		}
		switch {
		case accounts == nil:
			slog.Warn("OAuth providers are configured but there is no database, so no accounts to sign in to; ignoring them",
				"providers", names)
			cfg.OAuth = nil
		case cfg.PublicURL == "":
			slog.Warn("OAuth sign-in is on without PUBLIC_URL: redirects will be built from each request's host, "+
				"which only works if that host is exactly the one registered with the provider",
				"providers", names)
		default:
			slog.Info("OAuth sign-in on", "providers", names, "redirects_to", cfg.PublicURL+"/api/auth/oauth/<provider>/callback")
		}
	}

	addr := net.JoinHostPort("", strconv.Itoa(cfg.Port))
	handler := server.Routes(server.Options{
		KeyStore:         ks,
		AccountStore:     accounts,
		AuditStore:       auditLog,
		ProviderStore:    providers,
		LogStore:         ls,
		DLPStore:         ds,
		PolicyStore:      ps,
		LimitStore:       lims,
		Tracker:          tracker,
		Metrics:          reg,
		Inspector:        ins,
		DLPPolicy:        cfg.DLPPolicy,
		Alerter:          alrt,
		CustomProviders:  cfg.CustomProviders,
		OpenAIBaseURL:    cfg.OpenAIBaseURL,
		AnthropicBaseURL: cfg.AnthropicBaseURL,
		JevBaseURL:       cfg.JevBaseURL,
		AdminAPIKey:      cfg.AdminAPIKey,
		AllowedOrigins:   cfg.AllowedOrigins,
		OAuthProviders:   cfg.OAuth,
		OAuthStateKey:    oauth.DeriveKey(stateSecret),
		PublicURL:        cfg.PublicURL,
		RegistrationOpen: cfg.RegistrationOpen,
		ReadyCheck:       readyCheck,
		Logger:           logger,
	})
	srv := server.New(addr, handler, logger)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	stop() // restore default signal handling; a second signal now aborts

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("server stopped")
}

// seedDefaultProviders gives the default organization — the one a
// single-tenant installation works in — the providers the environment
// describes: OPENAI_API_KEY and friends, and CUSTOM_PROVIDERS. It fills in
// what is missing and nothing else: once somebody has set a provider up in the
// console, the console is where it lives, and a restart must not undo them.
//
// Other organizations get none of it. An operator's OpenAI key being spent by
// every tenant is a billing decision, not a default.
func seedDefaultProviders(ctx context.Context, store storage.ProviderStore, cfg *config.Config) {
	seed := func(p storage.ProviderConfig) {
		if _, err := store.GetProvider(ctx, storage.DefaultOrgID, p.Name); err == nil {
			return
		}
		if err := store.PutProvider(ctx, storage.DefaultOrgID, p); err != nil {
			slog.Error("seeding a provider from the environment failed", "provider", p.Name, "err", err)
			return
		}
		slog.Info("provider set up from the environment for the default organization", "provider", p.Name)
	}
	for name, key := range cfg.ProviderKeys {
		if key == "" || !storage.ProviderKind(name).Builtin() {
			continue
		}
		seed(storage.ProviderConfig{Name: name, Kind: storage.ProviderKind(name), APIKey: key, Enabled: true})
	}
	for _, cp := range cfg.CustomProviders {
		seed(storage.ProviderConfig{
			Name: cp.Name, Kind: storage.KindCompatible, BaseURL: cp.BaseURL,
			APIKey: cp.APIKey, Prefixes: cp.Prefixes, Enabled: true,
		})
	}
}
