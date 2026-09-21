package main

import (
	"net/http"

	"github.com/BROngineer/argocd-notifier/api/core"
	"github.com/BROngineer/argocd-notifier/internal/httpx"
)

// server implements core.ServerInterface, delegating every operation to
// its existing hand-rolled handler — this is pure routing glue, not new
// logic, so it has no tests of its own; each delegate is already tested.
//
// events/registerBackend are generic http.Handlers, not the concrete
// receiver.Handler/registry.Handler, because with leader election enabled
// each is wrapped in a leaderproxy.Handler first (see main.go) — Readyz is
// always unconditionally ready (never gated on leadership): a non-leader
// forwards requests instead of refusing traffic, so every replica can be a
// normal Service endpoint and the Deployment rollout completes normally.
type server struct {
	events          http.Handler
	registerBackend http.Handler
}

var _ core.ServerInterface = (*server)(nil)

func (s *server) SubmitEvent(w http.ResponseWriter, r *http.Request) {
	s.events.ServeHTTP(w, r)
}

func (s *server) Healthz(w http.ResponseWriter, r *http.Request) {
	httpx.HealthzHandler()(w, r)
}

func (s *server) Readyz(w http.ResponseWriter, r *http.Request) {
	httpx.ReadyzHandler(nil)(w, r)
}

func (s *server) RegisterBackend(w http.ResponseWriter, r *http.Request) {
	s.registerBackend.ServeHTTP(w, r)
}
