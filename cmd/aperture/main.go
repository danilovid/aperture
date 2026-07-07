package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/danilovid/aperture/internal/alerter"
	"github.com/danilovid/aperture/internal/config"
	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/server"
	"github.com/danilovid/aperture/internal/storage"
	"github.com/danilovid/aperture/internal/storage/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	if cfg.AdminAPIKey == "" {
		cfg.AdminAPIKey = config.GenerateKey("admin")
		slog.Warn("ADMIN_API_KEY not set — generated a key for this run; set the env var to make it stable",
			"admin_api_key", cfg.AdminAPIKey)
	}

	var ks storage.KeyStore
	var ls storage.LogStore
	var ps storage.PolicyStore
	var readyCheck func(ctx context.Context) error

	if cfg.DatabaseURL != "" {
		pool, err := postgres.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Warn("postgres unavailable, falling back to in-memory store", "err", err)
		} else {
			pgStore, err := postgres.NewKeyStore(context.Background(), pool)
			if err != nil {
				slog.Warn("key store init failed, falling back to in-memory store", "err", err)
				pool.Close()
			} else {
				ks = pgStore
				readyCheck = pool.Ping
				slog.Info("using PostgreSQL")
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
				}
			}
		}
	}

	if ks == nil {
		apertureKey := cfg.ApertureAPIKey
		if apertureKey == "" {
			apertureKey = config.GenerateKey("ap")
			slog.Warn("APERTURE_API_KEY not set — generated a key for this run; set the env var to make it stable",
				"aperture_api_key", apertureKey)
		}
		slog.Info("using in-memory store — provider keys are kept for the lifetime of the process")
		ks = config.NewRuntimeStore(apertureKey).KeyStore()

		if len(cfg.ProviderKeys) > 0 {
			if err := ks.SetProviderKeys(context.Background(), cfg.ProviderKeys); err != nil {
				slog.Error("seeding provider keys from env failed", "err", err)
			} else {
				for llm := range cfg.ProviderKeys {
					slog.Info("provider key loaded from env", "provider", llm)
				}
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var ins *inspector.Inspector
	var dlpStore storage.DLPStore
	var alrt *alerter.Alerter
	if cfg.DLPEnabled {
		ins = inspector.New()
		dlpStore = storage.NewMemDLPStore(1000)
		if ps == nil {
			ps = storage.NewMemPolicyStore(cfg.DLPPolicy)
		}
		alrt = alerter.New(cfg.Alert, logger)
		go alrt.Run(ctx)
		if cfg.Alert.URL != "" {
			slog.Info("DLP webhook alerts enabled", "format", cfg.Alert.Format)
		}
		slog.Info("DLP scanning enabled",
			"secrets", cfg.DLPPolicy.Secrets, "pii", cfg.DLPPolicy.PII, "custom", cfg.DLPPolicy.Custom)
	} else {
		slog.Warn("DLP scanning disabled (DLP_ENABLED=false)")
	}

	addr := net.JoinHostPort("", strconv.Itoa(cfg.Port))
	handler := server.Routes(server.Options{
		KeyStore:       ks,
		LogStore:       ls,
		DLPStore:       dlpStore,
		PolicyStore:    ps,
		Inspector:      ins,
		DLPPolicy:      cfg.DLPPolicy,
		Alerter:        alrt,
		OpenAIBaseURL:  cfg.OpenAIBaseURL,
		AdminAPIKey:    cfg.AdminAPIKey,
		AllowedOrigins: cfg.AllowedOrigins,
		ReadyCheck:     readyCheck,
		Logger:         logger,
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
