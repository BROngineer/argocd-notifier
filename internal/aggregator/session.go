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
	"github.com/BROngineer/argocd-notifier/internal/render"
)

type DuplicateAction string

const (
	DuplicateActionDrop   DuplicateAction = "drop"
	DuplicateActionThread DuplicateAction = "thread"
)

// SlackRef maps channel -> message ts, since a session's recipient can be
// several channels (the existing per-app slack.channels list).
type SlackRef map[string]string

type sessionState struct {
	perApp    map[string]event.Event
	slackRef  SlackRef
	lastFlush time.Time
}

type PublisherConfig struct {
	SessionTTL      time.Duration
	DuplicateAction DuplicateAction
}

type MessageBuilder interface {
	Build(perApp map[string]event.Event) (render.Message, error)
}

type MessagePoster interface {
	Post(ctx context.Context, channel string, msg render.Message) (ts string, err error)
	Update(ctx context.Context, channel, ts string, msg render.Message) error
	PostThreadReply(ctx context.Context, channel, threadTs, text string) error
}

type SessionPublisher struct {
	mu       sync.Mutex
	sessions map[SessionKey]*sessionState
	cfg      PublisherConfig
	clock    Clock
	builder  MessageBuilder
	poster   MessagePoster
	logger   *slog.Logger
}

func NewSessionPublisher(cfg PublisherConfig, builder MessageBuilder, poster MessagePoster, logger *slog.Logger) *SessionPublisher {
	return newSessionPublisherWithClock(cfg, builder, poster, logger, realClock{})
}

func newSessionPublisherWithClock(cfg PublisherConfig, builder MessageBuilder, poster MessagePoster, logger *slog.Logger, clock Clock) *SessionPublisher {
	return &SessionPublisher{
		sessions: make(map[SessionKey]*sessionState),
		cfg:      cfg,
		clock:    clock,
		builder:  builder,
		poster:   poster,
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
		st = &sessionState{perApp: make(map[string]event.Event), slackRef: SlackRef{}}
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
	slackRefSnapshot := make(SlackRef, len(st.slackRef))
	maps.Copy(slackRefSnapshot, st.slackRef)
	st.lastFlush = now
	sp.mu.Unlock()

	recipients := splitRecipients(events[0].Recipient)
	var errs []error

	if changed {
		msg, err := sp.builder.Build(perAppSnapshot)
		if err != nil {
			return fmt.Errorf("render message: %w", err)
		}

		newRefs := make(SlackRef)
		for _, channel := range recipients {
			if ts, ok := slackRefSnapshot[channel]; ok {
				if err := sp.poster.Update(ctx, channel, ts, msg); err != nil {
					errs = append(errs, fmt.Errorf("update %s: %w", channel, err))
				}
				continue
			}
			ts, err := sp.poster.Post(ctx, channel, msg)
			if err != nil {
				errs = append(errs, fmt.Errorf("post %s: %w", channel, err))
				continue
			}
			newRefs[channel] = ts
		}

		if len(newRefs) > 0 {
			sp.mu.Lock()
			if cur, ok := sp.sessions[key]; ok && cur == st {
				maps.Copy(cur.slackRef, newRefs)
			}
			sp.mu.Unlock()
		}
	}

	if sp.cfg.DuplicateAction == DuplicateActionThread {
		for _, ev := range duplicates {
			note := fmt.Sprintf("%s reported %s again (unchanged) at %s", ev.AppName, ev.Trigger, now.Format(time.RFC3339))
			for _, channel := range recipients {
				ts, ok := slackRefSnapshot[channel]
				if !ok {
					continue
				}
				if err := sp.poster.PostThreadReply(ctx, channel, ts, note); err != nil {
					errs = append(errs, fmt.Errorf("thread reply %s: %w", channel, err))
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
