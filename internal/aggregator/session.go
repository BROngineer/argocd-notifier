package aggregator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/event"
	"github.com/BROngineer/argocd-notifier/internal/notification"
)

type DuplicateAction string

const (
	DuplicateActionDrop   DuplicateAction = "drop"
	DuplicateActionThread DuplicateAction = "thread"
)

var ErrBackendDoesNotSupportThreadReply = errors.New("BackendDoesNotSupportThreadReply")

// MessageRefs maps recipient -> backend-specific message reference (e.g.
// Slack's ts), since a session's recipient can be several channels/targets
// (the existing per-app slack.channels list).
type MessageRefs map[string]string

type sessionState struct {
	perApp    map[string]event.Event
	refs      MessageRefs
	lastFlush time.Time
}

type PublisherConfig struct {
	SessionTTL      time.Duration
	DuplicateAction DuplicateAction
}

type SessionPublisher struct {
	mu       sync.Mutex
	sessions map[SessionKey]*sessionState
	cfg      PublisherConfig
	clock    Clock
	build    func(map[string]event.Event) (notification.Notification, error)
	backend  notification.Backend
	logger   *slog.Logger
}

// NewSessionPublisher fails fast if DuplicateAction is "thread" but backend
// doesn't implement notification.ThreadReplier — better to refuse to start
// than to silently drop thread-reply requests at runtime.
func NewSessionPublisher(cfg PublisherConfig, backend notification.Backend, logger *slog.Logger) (*SessionPublisher, error) {
	return newSessionPublisherWithClock(cfg, notification.Build, backend, logger, realClock{})
}

func newSessionPublisherWithClock(
	cfg PublisherConfig,
	build func(map[string]event.Event) (notification.Notification, error),
	backend notification.Backend,
	logger *slog.Logger,
	clock Clock,
) (*SessionPublisher, error) {
	if cfg.DuplicateAction == DuplicateActionThread {
		if _, ok := backend.(notification.ThreadReplier); !ok {
			return nil, ErrBackendDoesNotSupportThreadReply
		}
	}
	return &SessionPublisher{
		sessions: make(map[SessionKey]*sessionState),
		cfg:      cfg,
		clock:    clock,
		build:    build,
		backend:  backend,
		logger:   logger,
	}, nil
}

func (sp *SessionPublisher) Upsert(ctx context.Context, key SessionKey, events []event.Event) error {
	if len(events) == 0 {
		return nil
	}

	sp.mu.Lock()
	now := sp.clock.Now()
	st, ok := sp.sessions[key]
	if !ok || now.Sub(st.lastFlush) > sp.cfg.SessionTTL {
		st = &sessionState{perApp: make(map[string]event.Event), refs: MessageRefs{}}
		sp.sessions[key] = st
	}

	var duplicates []event.Event
	changed := false
	for _, ev := range events {
		if prior, exists := st.perApp[ev.AppName]; exists && contentHash(prior) == contentHash(ev) {
			duplicates = append(duplicates, ev)
			continue
		}
		st.perApp[ev.AppName] = ev
		changed = true
	}

	perAppSnapshot := make(map[string]event.Event, len(st.perApp))
	maps.Copy(perAppSnapshot, st.perApp)
	refsSnapshot := make(MessageRefs, len(st.refs))
	maps.Copy(refsSnapshot, st.refs)
	st.lastFlush = now
	sp.mu.Unlock()

	recipients := splitRecipients(events[0].Recipient)
	var errs []error

	if changed {
		n, err := sp.build(perAppSnapshot)
		if err != nil {
			return fmt.Errorf("build notification: %w", err)
		}

		// A backend that can't edit a previous message just gets a fresh
		// Post every flush instead — no ref is ever tracked for it.
		updater, canUpdate := sp.backend.(notification.Updater)
		newRefs := make(MessageRefs)
		for _, recipient := range recipients {
			if canUpdate {
				if ref, ok := refsSnapshot[recipient]; ok {
					if err := updater.Update(ctx, recipient, ref, n); err != nil {
						errs = append(errs, fmt.Errorf("update %s: %w", recipient, err))
					}
					continue
				}
			}
			ref, err := sp.backend.Post(ctx, recipient, n)
			if err != nil {
				errs = append(errs, fmt.Errorf("post %s: %w", recipient, err))
				continue
			}
			if canUpdate {
				newRefs[recipient] = ref
			}
		}

		if len(newRefs) > 0 {
			sp.mu.Lock()
			if cur, ok := sp.sessions[key]; ok && cur == st {
				maps.Copy(cur.refs, newRefs)
			}
			sp.mu.Unlock()
		}
	}

	if sp.cfg.DuplicateAction == DuplicateActionThread {
		threader, _ := sp.backend.(notification.ThreadReplier) // guaranteed by NewSessionPublisher validation
		for _, ev := range duplicates {
			note := fmt.Sprintf("%s reported %s again (unchanged) at %s", ev.AppName, ev.Trigger, now.Format(time.RFC3339))
			for _, recipient := range recipients {
				ref, ok := refsSnapshot[recipient]
				if !ok {
					continue
				}
				if err := threader.PostThreadReply(ctx, recipient, ref, note); err != nil {
					errs = append(errs, fmt.Errorf("thread reply %s: %w", recipient, err))
				}
			}
		}
	}

	return errors.Join(errs...)
}

func contentHash(ev event.Event) string {
	h := sha256.Sum256([]byte(ev.Trigger + "|" + ev.Revision + "|" + ev.SyncPhase + "|" + ev.HealthStatus + "|" + ev.OperationMsg))
	return hex.EncodeToString(h[:])
}

func splitRecipients(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
