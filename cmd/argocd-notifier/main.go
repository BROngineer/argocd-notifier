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

	"github.com/BROngineer/argocd-notifier/internal/aggregator"
	"github.com/BROngineer/argocd-notifier/internal/config"
	"github.com/BROngineer/argocd-notifier/internal/httpx"
	"github.com/BROngineer/argocd-notifier/internal/logging"
	"github.com/BROngineer/argocd-notifier/internal/receiver"
	"github.com/BROngineer/argocd-notifier/internal/render"
	"github.com/BROngineer/argocd-notifier/internal/slack"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat)

	slackClient := slack.NewClient(cfg.SlackBotToken, cfg.SlackRequestTimeout, cfg.SlackMaxRetries)

	publisher := aggregator.NewSessionPublisher(
		aggregator.PublisherConfig{
			SessionTTL:      cfg.SessionTTL,
			DuplicateAction: aggregator.DuplicateAction(cfg.DuplicateAction),
		},
		aggregator.MessageBuilderFunc(render.BuildMessage),
		slackClient,
		logger,
	)

	engine := aggregator.NewEngine(
		aggregator.Config{
			IdleWindow:      cfg.IdleWindow,
			MaxWait:         cfg.MaxWait,
			CombineTriggers: cfg.CombineTriggers,
		},
		publisher,
		logger,
	)

	handler := receiver.NewHandler(cfg.IngestQueueSize, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	receiver.RunWorkers(ctx, handler.Events(), cfg.WorkerCount, engine.Ingest)

	mux := http.NewServeMux()
	mux.Handle(cfg.EventsPath, handler)
	mux.HandleFunc("/healthz", httpx.HealthzHandler())
	mux.HandleFunc("/readyz", httpx.ReadyzHandler(nil))

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
