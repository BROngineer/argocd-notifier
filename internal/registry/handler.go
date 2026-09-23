package registry

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/BROngineer/argocd-notifier/api/core"
)

const maxBodyBytes = 1 << 20

type Handler struct {
	reg    *Registry
	logger *slog.Logger
}

func NewHandler(reg *Registry, logger *slog.Logger) *Handler {
	return &Handler{reg: reg, logger: logger}
}

// RegisterBackend is one of core.ServerInterface's methods — the full
// interface is implemented by cmd/argocd-notifier's server type, which
// composes this along with the other core endpoints' handlers.
func (h *Handler) RegisterBackend(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req core.RegisterBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("registration request malformed", "error", err)
		http.Error(w, "invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	isNew, err := h.reg.Register(req.Name, req.BaseURL, req.SupportsThreadReply)
	if err != nil {
		h.logger.Warn("registration rejected", "name", req.Name, "baseURL", req.BaseURL, "error", err)
		http.Error(w, "invalid registration: "+err.Error(), http.StatusBadRequest)
		return
	}

	if isNew {
		h.logger.Info("backend registered", "name", req.Name, "baseURL", req.BaseURL, "supportsThreadReply", req.SupportsThreadReply)
	} else {
		h.logger.Debug("backend heartbeat", "name", req.Name, "baseURL", req.BaseURL)
	}
	w.WriteHeader(http.StatusNoContent)
}
