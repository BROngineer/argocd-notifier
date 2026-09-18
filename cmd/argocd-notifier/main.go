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

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/BROngineer/argocd-notifier/internal/aggregator"
	"github.com/BROngineer/argocd-notifier/internal/config"
	"github.com/BROngineer/argocd-notifier/internal/httpx"
	"github.com/BROngineer/argocd-notifier/internal/leader"
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

	var isReady func() bool
	if cfg.LeaderElectionEnabled {
		isReady = startLeaderElection(ctx, cfg, logger)
	}

	mux := http.NewServeMux()
	mux.Handle(cfg.EventsPath, handler)
	mux.HandleFunc("/healthz", httpx.HealthzHandler())
	mux.HandleFunc("/readyz", httpx.ReadyzHandler(isReady))

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

// startLeaderElection runs the elector in the background and returns its
// IsLeader as the /readyz predicate — only the current leader reports ready,
// so the k8s Service routes traffic exclusively to it.
func startLeaderElection(ctx context.Context, cfg *config.Config, logger *slog.Logger) func() bool {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("failed to load in-cluster config for leader election", "error", err)
		os.Exit(1)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		logger.Error("failed to build kubernetes client for leader election", "error", err)
		os.Exit(1)
	}

	elector, err := leader.New(client, leader.Config{
		Namespace:     cfg.LeaderElectionNamespace,
		LeaseName:     cfg.LeaseName,
		Identity:      cfg.PodName,
		LeaseDuration: cfg.LeaseDuration,
		RenewDeadline: cfg.RenewDeadline,
		RetryPeriod:   cfg.RetryPeriod,
	}, logger)
	if err != nil {
		logger.Error("failed to build leader elector", "error", err)
		os.Exit(1)
	}

	go elector.Run(ctx)

	return elector.IsLeader
}
