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
	var ls storage.LogStore

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
				slog.Info("using PostgreSQL")
				pgLog, err := postgres.NewLogStore(context.Background(), pool)
				if err != nil {
					slog.Warn("log store init failed, monitoring disabled", "err", err)
				} else {
					ls = pgLog
				}
			}
		}
	}

	if ks == nil {
		slog.Info("using in-memory store — set key via Admin panel (keys will be lost on restart)")
		ks = config.NewRuntimeStore().KeyStore()
	}

	addr := net.JoinHostPort("", strconv.Itoa(cfg.Port))
	handler := server.Routes(ks, ls, cfg.OpenAIBaseURL, cfg.AdminAPIKey, logger)
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
