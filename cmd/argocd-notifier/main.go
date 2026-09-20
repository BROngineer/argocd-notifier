package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/BROngineer/argocd-notifier/api/core"
	"github.com/BROngineer/argocd-notifier/internal/aggregator"
	"github.com/BROngineer/argocd-notifier/internal/config"
	"github.com/BROngineer/argocd-notifier/internal/httpx"
	"github.com/BROngineer/argocd-notifier/internal/leader"
	"github.com/BROngineer/argocd-notifier/internal/logging"
	"github.com/BROngineer/argocd-notifier/internal/notification"
	"github.com/BROngineer/argocd-notifier/internal/receiver"
	"github.com/BROngineer/argocd-notifier/internal/registry"
	"github.com/BROngineer/argocd-notifier/internal/remotebackend"
	"github.com/BROngineer/argocd-notifier/internal/slack"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat)

	var notificationBackend notification.Backend

	// Add case-branch here to wire new backend
	switch cfg.Backend {
	case "slack":
		notificationBackend = slack.NewClient(cfg.SlackBotToken, cfg.SlackRequestTimeout, cfg.SlackMaxRetries)
	default:
		logger.Error("failed to setup notification backend", "backend", cfg.Backend, "error", config.ErrBackendNotSupported)
		os.Exit(1)
	}

	duplicateAction := aggregator.DuplicateAction(cfg.DuplicateAction)
	if err := aggregator.ValidateStaticBackend(duplicateAction, notificationBackend); err != nil {
		logger.Error("configured backend does not support duplicate_action=thread", "backend", cfg.Backend, "error", err)
		os.Exit(1)
	}

	backendRegistry := registry.NewRegistry(cfg.BackendRegistryTTL)
	remoteResolver := remotebackend.NewResolver(backendRegistry, &http.Client{Timeout: cfg.RemoteBackendRequestTimeout})
	resolver := aggregator.NewStaticResolver(cfg.Backend, notificationBackend, remoteResolver)

	publisher := aggregator.NewSessionPublisher(
		aggregator.PublisherConfig{
			SessionTTL:      cfg.SessionTTL,
			DuplicateAction: duplicateAction,
		},
		resolver,
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
	registryHandler := registry.NewHandler(backendRegistry, logger)

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
	core.HandlerFromMux(registryHandler, mux)

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

	var pprofSrv *http.Server
	if cfg.PprofEnabled {
		pprofSrv = startPprof(cfg.PprofAddr, logger)
	}

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	if pprofSrv != nil {
		if err := pprofSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("pprof graceful shutdown failed", "error", err)
		}
	}
}

// startPprof serves pprof handlers on their own mux and listener, separate
// from the main server and from http.DefaultServeMux — it must never be
// reachable through whatever k8s Service routes ArgoCD's webhook traffic in.
func startPprof(addr string, logger *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("starting pprof server", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("pprof server error", "error", err)
		}
	}()
	return srv
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
