package receiver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

const maxBodyBytes = 1 << 20

type Handler struct {
	ch     chan event.Event
	logger *slog.Logger
}

func NewHandler(queueSize int, logger *slog.Logger) *Handler {
	return &Handler{ch: make(chan event.Event, queueSize), logger: logger}
}

func (h *Handler) Events() <-chan event.Event {
	return h.ch
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var ev event.Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		h.logger.Warn("event payload malformed", "error", err)
		http.Error(w, "invalid payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := ev.Validate(); err != nil {
		h.logger.Warn("event invalid", "groupKey", ev.GroupKey, "appName", ev.AppName, "error", err)
		http.Error(w, "invalid event: "+err.Error(), http.StatusBadRequest)
		return
	}

	select {
	case h.ch <- ev:
		h.logger.Debug("event received from argocd", "event", ev)
		h.logger.Info("event accepted", "groupKey", ev.GroupKey, "appName", ev.AppName, "trigger", ev.Trigger, "backend", ev.Backend)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	default:
		h.logger.Warn("event queue full", "groupKey", ev.GroupKey, "appName", ev.AppName)
		http.Error(w, "queue full", http.StatusServiceUnavailable)
	}
}

// RunWorkers spawns n goroutines draining events into ingest until ctx is
// canceled. It never blocks Handler.ServeHTTP: the channel is the only
// coupling between the fast HTTP path and however slow ingest turns out to be.
func RunWorkers(ctx context.Context, events <-chan event.Event, n int, ingest func(event.Event)) {
	for range n {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-events:
					if !ok {
						return
					}
					ingest(ev)
				}
			}
		}()
	}
}
