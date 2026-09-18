package aggregator

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

type SessionKey struct {
	GroupKey string
	Revision string
	Trigger  string
}

type Publisher interface {
	Upsert(ctx context.Context, key SessionKey, events []event.Event) error
}

type Config struct {
	IdleWindow      time.Duration
	MaxWait         time.Duration
	CombineTriggers bool
}

type Engine struct {
	mu     sync.Mutex
	groups map[string]*groupState
	cfg    Config
	clock  Clock
	pub    Publisher
	logger *slog.Logger
}

// groupState paces flushes for one groupKey. It's never removed from
// Engine.groups once created: generation increments monotonically across
// flushes so a timer callback captured before a flush can never match the
// generation of a batch started after it, even though the fake-timer path
// in tests can't otherwise reproduce the real time.Timer Stop-vs-fire race
// this guards against.
type groupState struct {
	buckets    map[string][]event.Event
	idleTimer  Timer
	hardTimer  Timer
	generation int
}

func NewEngine(cfg Config, pub Publisher, logger *slog.Logger) *Engine {
	return newEngineWithClock(cfg, pub, logger, realClock{})
}

func newEngineWithClock(cfg Config, pub Publisher, logger *slog.Logger, clock Clock) *Engine {
	return &Engine{
		groups: make(map[string]*groupState),
		cfg:    cfg,
		clock:  clock,
		pub:    pub,
		logger: logger,
	}
}

func (e *Engine) Ingest(ev event.Event) {
	e.mu.Lock()

	gs, ok := e.groups[ev.GroupKey]
	if !ok {
		gs = &groupState{buckets: make(map[string][]event.Event)}
		e.groups[ev.GroupKey] = gs
	}
	freshBatch := len(gs.buckets) == 0
	gs.buckets[ev.Trigger] = append(gs.buckets[ev.Trigger], ev)

	groupKey := ev.GroupKey
	gen := gs.generation

	if freshBatch {
		gs.hardTimer = e.clock.AfterFunc(e.cfg.MaxWait, func() { e.onTimerFire(groupKey, gen) })
	}
	if gs.idleTimer != nil {
		gs.idleTimer.Stop()
	}
	gs.idleTimer = e.clock.AfterFunc(e.cfg.IdleWindow, func() { e.onTimerFire(groupKey, gen) })

	e.mu.Unlock()
}

func (e *Engine) onTimerFire(groupKey string, gen int) {
	e.mu.Lock()

	gs, ok := e.groups[groupKey]
	if !ok || gs.generation != gen || len(gs.buckets) == 0 {
		e.mu.Unlock()
		return
	}

	buckets := gs.buckets
	gs.buckets = make(map[string][]event.Event)
	if gs.idleTimer != nil {
		gs.idleTimer.Stop()
		gs.idleTimer = nil
	}
	if gs.hardTimer != nil {
		gs.hardTimer.Stop()
		gs.hardTimer = nil
	}
	gs.generation++

	e.mu.Unlock()

	e.flush(groupKey, buckets)
}

func (e *Engine) flush(groupKey string, buckets map[string][]event.Event) {
	sessioned := make(map[SessionKey][]event.Event)
	for trigger, evs := range buckets {
		for _, ev := range evs {
			key := SessionKey{GroupKey: groupKey, Revision: ev.Revision}
			if !e.cfg.CombineTriggers {
				key.Trigger = trigger
			}
			sessioned[key] = append(sessioned[key], ev)
		}
	}

	for key, evs := range sessioned {
		if err := e.pub.Upsert(context.Background(), key, evs); err != nil {
			e.logger.Error("publish failed",
				"groupKey", key.GroupKey, "revision", key.Revision, "trigger", key.Trigger, "error", err)
		}
	}
}
