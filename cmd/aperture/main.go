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
	var runtimeStore *config.RuntimeStore

	if cfg.DatabaseURL != "" {
		pgStore, err := postgres.NewKeyStore(context.Background(), cfg.DatabaseURL)
		if err != nil {
			slog.Error("postgres init failed", "err", err)
			os.Exit(1)
		}
		ks = pgStore
		slog.Info("using PostgreSQL for API keys")
	} else {
		runtimeStore = config.NewRuntimeStore(cfg.OpenAIAPIKey)
		ks = runtimeStore.KeyStore()
		if runtimeStore.IsConfigured() {
			slog.Info("using runtime config (seeded from OPENAI_API_KEY)")
		} else {
			slog.Info("using runtime config — set key via Admin panel")
		}
	}

	addr := net.JoinHostPort("", strconv.Itoa(cfg.Port))
	var rc server.RuntimeConfig = runtimeStore
	handler := server.Routes(ks, cfg.OpenAIBaseURL, rc, logger)
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
