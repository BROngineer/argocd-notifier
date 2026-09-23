package slack

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

// TemplateWatcher polls a template file on an interval (the same
// ticker-based pattern internal/slackbackend.Registrar already uses for its
// heartbeat, rather than a filesystem-event watcher — one less dependency,
// and this project already favors that shape) and holds the latest
// successfully parsed TemplateRenderer. A bad reload is logged and
// discarded — it never replaces a working template with a broken one, and
// before any reload has ever succeeded, Render falls back to
// DefaultRenderer so the backend is never left without a renderer.
//
// fallbackToDefault controls a different case: a template that parses fine
// but fails to render (or produces invalid JSON for) one specific
// notification. true falls back to DefaultRenderer for that message, so it
// still gets sent; false (the default) logs and returns the error, letting
// the caller's existing failure handling skip just that notification — "log
// and discard" instead of silently changing the message's look underneath
// a template author who didn't ask for that.
type TemplateWatcher struct {
	path              string
	fallbackToDefault bool
	logger            *slog.Logger

	// modTime is only ever touched from Reload, which Run calls from a
	// single goroutine — no lock needed for it. current is read from
	// Render, which can run concurrently with Reload, hence the atomic.
	modTime time.Time
	current atomic.Pointer[TemplateRenderer]
}

var _ Renderer = (*TemplateWatcher)(nil)

func NewTemplateWatcher(path string, fallbackToDefault bool, logger *slog.Logger) *TemplateWatcher {
	return &TemplateWatcher{path: path, fallbackToDefault: fallbackToDefault, logger: logger}
}

// Run loads the template immediately, then reloads on every tick until ctx
// is done — mirrors Registrar.Run's shape.
func (w *TemplateWatcher) Run(ctx context.Context, interval time.Duration) {
	w.Reload()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Reload()
		}
	}
}

// Reload re-reads and re-parses the template file if its mtime changed
// since the last successful load. Exported (rather than only reachable
// through Run's ticker) so callers — tests included — can trigger a reload
// deterministically instead of racing a timer.
func (w *TemplateWatcher) Reload() {
	info, err := os.Stat(w.path)
	if err != nil {
		w.logger.Error("stat message template", "path", w.path, "error", err)
		return
	}
	if w.current.Load() != nil && info.ModTime().Equal(w.modTime) {
		return
	}

	src, err := os.ReadFile(w.path)
	if err != nil {
		w.logger.Error("read message template", "path", w.path, "error", err)
		return
	}

	renderer, err := ParseTemplateRenderer(string(src))
	if err != nil {
		w.logger.Error("parse message template, keeping previous", "path", w.path, "error", err)
		return
	}

	w.modTime = info.ModTime()
	w.current.Store(renderer)
	w.logger.Info("message template (re)loaded", "path", w.path)
}

func (w *TemplateWatcher) Render(n notification.Notification) (string, json.RawMessage, error) {
	renderer := w.current.Load()
	if renderer == nil {
		return DefaultRenderer{}.Render(n)
	}

	text, attachments, err := renderer.Render(n)
	if err == nil {
		return text, attachments, nil
	}

	w.logger.Error("message template render failed", "error", err, "fallbackToDefault", w.fallbackToDefault)
	if !w.fallbackToDefault {
		return "", nil, err
	}
	return DefaultRenderer{}.Render(n)
}
