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

var _ core.ServerInterface = (*Handler)(nil)

func (h *Handler) RegisterBackend(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req core.RegisterBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.reg.Register(req.Name, req.BaseURL, req.SupportsThreadReply); err != nil {
		http.Error(w, "invalid registration: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
