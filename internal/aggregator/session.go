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

// RefKey identifies one tracked message: a session can route different
// apps to different (backend, recipient) pairs, and two different backends
// may legitimately use the same recipient string to mean different things
// (e.g. a Slack channel name vs. some other backend's queue name) — so the
// ref cache must be keyed by the pair, not recipient alone.
type RefKey struct {
	Backend   string
	Recipient string
}

// MessageRefs maps a (backend, recipient) pair to that backend's own
// message reference (e.g. Slack's ts).
type MessageRefs map[RefKey]string

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
	resolver BackendResolver
	logger   *slog.Logger
}

// NewSessionPublisher resolves each routed event's backend by name via
// resolver, rather than binding to one fixed backend — see
// aggregator.ValidateStaticBackend for the equivalent of the old
// construction-time thread-reply fail-fast, which only makes sense for the
// one compiled-in backend known at startup.
func NewSessionPublisher(cfg PublisherConfig, resolver BackendResolver, logger *slog.Logger) *SessionPublisher {
	return newSessionPublisherWithClock(cfg, notification.Build, resolver, logger, realClock{})
}

func newSessionPublisherWithClock(
	cfg PublisherConfig,
	build func(map[string]event.Event) (notification.Notification, error),
	resolver BackendResolver,
	logger *slog.Logger,
	clock Clock,
) *SessionPublisher {
	return &SessionPublisher{
		sessions: make(map[SessionKey]*sessionState),
		cfg:      cfg,
		clock:    clock,
		build:    build,
		resolver: resolver,
		logger:   logger,
	}
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

	var errs []error

	if changed {
		// Recipient is a per-app (really per-trigger, on the ArgoCD side)
		// routing decision, not "who to CC on one shared message" — group
		// by (backend, recipient) first, so each destination only sees the
		// apps actually addressed to it, instead of every destination
		// getting whatever the first event in the batch happened to route
		// to. Two different backends can share a recipient string without
		// colliding, since the key carries both.
		newRefs := make(MessageRefs)
		for rk, apps := range groupAppsByBackendRecipient(perAppSnapshot) {
			n, err := sp.build(apps)
			if err != nil {
				errs = append(errs, fmt.Errorf("build notification for %s/%s: %w", rk.Backend, rk.Recipient, err))
				continue
			}

			backend, ok := sp.resolver.Resolve(rk.Backend)
			if !ok {
				errs = append(errs, fmt.Errorf("no backend registered for %q", rk.Backend))
				continue
			}

			// A backend implementing Notifier decides post-vs-update
			// itself and may hand back a changed ref even on what looks
			// like an edit — always trust whatever it returns.
			if notifier, ok := backend.(notification.Notifier); ok {
				newRef, err := notifier.Notify(ctx, rk.Recipient, refsSnapshot[rk], n)
				if err != nil {
					errs = append(errs, fmt.Errorf("notify %s/%s: %w", rk.Backend, rk.Recipient, err))
					continue
				}
				newRefs[rk] = newRef
				continue
			}

			b, ok := backend.(notification.Backend)
			if !ok {
				errs = append(errs, fmt.Errorf("backend %q implements neither Notifier nor Backend", rk.Backend))
				continue
			}

			// A backend that can't edit a previous message just gets a
			// fresh Post every flush instead — no ref is ever tracked.
			updater, canUpdate := b.(notification.Updater)
			if canUpdate {
				if ref, ok := refsSnapshot[rk]; ok {
					if err := updater.Update(ctx, rk.Recipient, ref, n); err != nil {
						errs = append(errs, fmt.Errorf("update %s/%s: %w", rk.Backend, rk.Recipient, err))
					}
					continue
				}
			}
			ref, err := b.Post(ctx, rk.Recipient, n)
			if err != nil {
				errs = append(errs, fmt.Errorf("post %s/%s: %w", rk.Backend, rk.Recipient, err))
				continue
			}
			if canUpdate {
				newRefs[rk] = ref
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
		for _, ev := range duplicates {
			backend, ok := sp.resolver.Resolve(ev.Backend)
			if !ok {
				errs = append(errs, fmt.Errorf("no backend registered for %q", ev.Backend))
				continue
			}
			threader, ok := backend.(notification.ThreadReplier)
			if !ok {
				continue
			}
			note := fmt.Sprintf("%s reported %s again (unchanged) at %s", ev.AppName, ev.Trigger, now.Format(time.RFC3339))
			for _, recipient := range splitRecipients(ev.Recipient) {
				rk := RefKey{Backend: ev.Backend, Recipient: recipient}
				ref, ok := refsSnapshot[rk]
				if !ok {
					continue
				}
				if err := threader.PostThreadReply(ctx, recipient, ref, note); err != nil {
					errs = append(errs, fmt.Errorf("thread reply %s/%s: %w", ev.Backend, recipient, err))
				}
			}
		}
	}

	return errors.Join(errs...)
}

// groupAppsByBackendRecipient fans an app out to every (backend, recipient)
// pair its own latest event names (semicolon-joined recipients split
// independently per app, all under that event's single Backend), so a
// session with mixed routing (different clusters/triggers/backends
// addressed to different destinations) renders a distinct,
// correctly-scoped notification per destination instead of one shared view.
func groupAppsByBackendRecipient(perApp map[string]event.Event) map[RefKey]map[string]event.Event {
	out := make(map[RefKey]map[string]event.Event)
	for appName, ev := range perApp {
		for _, recipient := range splitRecipients(ev.Recipient) {
			rk := RefKey{Backend: ev.Backend, Recipient: recipient}
			if out[rk] == nil {
				out[rk] = make(map[string]event.Event)
			}
			out[rk][appName] = ev
		}
	}
	return out
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
