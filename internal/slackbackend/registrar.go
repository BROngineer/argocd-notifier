package slackbackend

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/BROngineer/argocd-notifier/api/core"
)

// Registrar registers this backend with the core and keeps re-registering
// on an interval — the heartbeat that lets a new leader (which starts with
// zero registered backends) notice this one again after failover. It never
// gives up on a failed attempt: the core may simply not be up yet at
// startup, and the next tick will retry.
type Registrar struct {
	client              *core.ClientWithResponses
	name                string
	baseURL             string
	supportsThreadReply bool
	interval            time.Duration
	logger              *slog.Logger
	registered          atomic.Bool
}

func NewRegistrar(coreURL, name, baseURL string, supportsThreadReply bool, interval time.Duration, httpClient *http.Client, logger *slog.Logger) (*Registrar, error) {
	client, err := core.NewClientWithResponses(coreURL, core.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("build core client: %w", err)
	}
	return &Registrar{
		client:              client,
		name:                name,
		baseURL:             baseURL,
		supportsThreadReply: supportsThreadReply,
		interval:            interval,
		logger:              logger,
	}, nil
}

// HasRegistered reports whether at least one registration attempt has
// succeeded — used to gate readiness, since there's no point reporting
// ready before the core can possibly know this backend exists.
func (r *Registrar) HasRegistered() bool {
	return r.registered.Load()
}

func (r *Registrar) Run(ctx context.Context) {
	r.register(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.register(ctx)
		}
	}
}

func (r *Registrar) register(ctx context.Context) {
	resp, err := r.client.RegisterBackendWithResponse(ctx, core.RegisterBackendRequest{
		Name:                r.name,
		BaseURL:             r.baseURL,
		SupportsThreadReply: r.supportsThreadReply,
	})
	if err != nil {
		r.logger.Error("registration failed", "name", r.name, "error", err)
		return
	}
	if resp.StatusCode() != http.StatusNoContent {
		r.logger.Error("registration rejected", "name", r.name, "status", resp.StatusCode(), "body", string(resp.Body))
		return
	}

	if r.registered.CompareAndSwap(false, true) {
		r.logger.Info("registered", "name", r.name, "baseURL", r.baseURL, "supportsThreadReply", r.supportsThreadReply)
		return
	}
	r.logger.Debug("heartbeat refreshed", "name", r.name)
}
