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

	"github.com/danilovid/aperture/internal/config"
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

	var ks storage.KeyStore
	if cfg.DatabaseURL != "" {
		pgStore, err := postgres.NewKeyStore(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("postgres init failed", "err", err)
			os.Exit(1)
		}
		ks = pgStore
		slog.Info("using PostgreSQL for API keys")
	} else {
		if cfg.OpenAIAPIKey == "" {
			slog.Error("either DATABASE_URL or OPENAI_API_KEY is required")
			os.Exit(1)
		}
		ks = &storage.EnvKeyStore{OpenAIAPIKey: cfg.OpenAIAPIKey}
		slog.Info("using env OPENAI_API_KEY (no database)")
	}

	addr := net.JoinHostPort("", strconv.Itoa(cfg.Port))
	handler := server.Routes(ks, cfg.OpenAIBaseURL, cfg.AdminAPIKey, logger)
	srv := server.New(addr, handler, logger)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}

	slog.Info("server stopped")
}
