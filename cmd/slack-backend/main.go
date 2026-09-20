package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/internal/httpx"
	"github.com/BROngineer/argocd-notifier/internal/logging"
	"github.com/BROngineer/argocd-notifier/internal/slack"
	"github.com/BROngineer/argocd-notifier/internal/slackbackend"
)

// supportsThreadReply is not a config knob: internal/slack.Client always
// implements notification.ThreadReplier, so this is a fact about this
// binary, not an operator choice.
const supportsThreadReply = true

func main() {
	cfg, err := slackbackend.Load()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat)

	slackClient := slack.NewClient(cfg.SlackBotToken, cfg.SlackRequestTimeout, cfg.SlackMaxRetries)
	handler := slackbackend.NewHandler(slackClient, logger)

	registrar, err := slackbackend.NewRegistrar(
		cfg.CoreURL,
		cfg.BackendName,
		cfg.PublicBaseURL,
		supportsThreadReply,
		cfg.RegisterInterval,
		http.DefaultClient,
		logger,
	)
	if err != nil {
		logger.Error("failed to build registrar", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go registrar.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", httpx.HealthzHandler())
	mux.HandleFunc("/readyz", httpx.ReadyzHandler(registrar.HasRegistered))
	backendapi.HandlerFromMux(handler, mux)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpx.Middleware(logger)(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("starting server", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
