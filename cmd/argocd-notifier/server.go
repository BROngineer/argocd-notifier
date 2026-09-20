package main

import (
	"net/http"

	"github.com/BROngineer/argocd-notifier/api/core"
	"github.com/BROngineer/argocd-notifier/internal/httpx"
	"github.com/BROngineer/argocd-notifier/internal/registry"
)

// server implements core.ServerInterface, delegating every operation to
// its existing hand-rolled handler — this is pure routing glue, not new
// logic, so it has no tests of its own; each delegate is already tested.
type server struct {
	events   http.Handler
	registry *registry.Handler
	isReady  func() bool
}

var _ core.ServerInterface = (*server)(nil)

func (s *server) SubmitEvent(w http.ResponseWriter, r *http.Request) {
	s.events.ServeHTTP(w, r)
}

func (s *server) Healthz(w http.ResponseWriter, r *http.Request) {
	httpx.HealthzHandler()(w, r)
}

func (s *server) Readyz(w http.ResponseWriter, r *http.Request) {
	httpx.ReadyzHandler(s.isReady)(w, r)
}

func (s *server) RegisterBackend(w http.ResponseWriter, r *http.Request) {
	s.registry.RegisterBackend(w, r)
}
