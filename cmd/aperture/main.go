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
	"github.com/danilovid/aperture/internal/provider"
	"github.com/danilovid/aperture/internal/provider/openai"
	"github.com/danilovid/aperture/internal/server"
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

	if cfg.OpenAIAPIKey == "" {
		slog.Error("OPENAI_API_KEY is required")
		os.Exit(1)
	}

	openaiClient := openai.New(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey)
	var p provider.Provider = openaiClient

	addr := net.JoinHostPort("", strconv.Itoa(cfg.Port))
	handler := server.Routes(p, logger)
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
