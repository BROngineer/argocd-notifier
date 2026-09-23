package slackbackend

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/internal/notification"
)

const maxBodyBytes = 1 << 20

var ErrThreadReplyNotSupported = errors.New("ThreadReplyNotSupported")

// Handler implements backendapi.ServerInterface against a
// notification.Backend — accepted as an interface, not *slack.Client
// directly, so tests can supply a fake.
type Handler struct {
	backend notification.Backend
	logger  *slog.Logger
}

func NewHandler(backend notification.Backend, logger *slog.Logger) *Handler {
	return &Handler{backend: backend, logger: logger}
}

var _ backendapi.ServerInterface = (*Handler)(nil)

func (h *Handler) Notify(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req backendapi.NotifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("notify payload malformed", "error", err)
		http.Error(w, "invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	n := fromWireNotification(req.Notification)
	ctx := r.Context()

	ref := req.Ref
	if ref != "" {
		if updater, ok := h.backend.(notification.Updater); ok {
			if err := updater.Update(ctx, req.Recipient, ref, n); err != nil {
				h.logger.Error("update failed", "recipient", req.Recipient, "error", err)
				http.Error(w, "update failed", http.StatusInternalServerError)
				return
			}
			h.logger.Debug("pushed notification content", "recipient", req.Recipient, "action", "update", "notification", n)
			h.logger.Info("notification pushed", "recipient", req.Recipient, "action", "update", "ref", ref, "items", len(n.Items))
			h.respondNotifyResult(w, ref)
			return
		}
	}

	newRef, err := h.backend.Post(ctx, req.Recipient, n)
	if err != nil {
		h.logger.Error("post failed", "recipient", req.Recipient, "error", err)
		http.Error(w, "post failed", http.StatusInternalServerError)
		return
	}
	h.logger.Debug("pushed notification content", "recipient", req.Recipient, "action", "post", "notification", n)
	h.logger.Info("notification pushed", "recipient", req.Recipient, "action", "post", "ref", newRef, "items", len(n.Items))
	h.respondNotifyResult(w, newRef)
}

func (h *Handler) respondNotifyResult(w http.ResponseWriter, ref string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(backendapi.NotifyResult{Ref: ref})
}

func (h *Handler) ThreadReply(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req backendapi.ThreadReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Warn("thread reply payload malformed", "error", err)
		http.Error(w, "invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	threader, ok := h.backend.(notification.ThreadReplier)
	if !ok {
		h.logger.Error("thread reply requested but backend does not support it", "error", ErrThreadReplyNotSupported)
		http.Error(w, ErrThreadReplyNotSupported.Error(), http.StatusInternalServerError)
		return
	}

	if err := threader.PostThreadReply(r.Context(), req.Recipient, req.Ref, req.Text); err != nil {
		h.logger.Error("thread reply failed", "recipient", req.Recipient, "error", err)
		http.Error(w, "thread reply failed", http.StatusInternalServerError)
		return
	}

	h.logger.Info("thread reply pushed", "recipient", req.Recipient, "ref", req.Ref)
	w.WriteHeader(http.StatusNoContent)
}
