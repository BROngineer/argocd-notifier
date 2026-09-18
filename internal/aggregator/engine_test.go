package aggregator

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

type upsertCall struct {
	key    SessionKey
	events []event.Event
}

type fakePublisher struct {
	mu    sync.Mutex
	calls []upsertCall
}

func (p *fakePublisher) Upsert(_ context.Context, key SessionKey, events []event.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, upsertCall{key: key, events: events})
	return nil
}

func (p *fakePublisher) Calls() []upsertCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]upsertCall(nil), p.calls...)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func baseEvent() event.Event {
	return event.Event{
		GroupKey:  "tatooine",
		AppName:   "tatooine-dev-empire",
		Trigger:   "on-deployed",
		Revision:  "rev-1",
		Recipient: "test1234asdf",
	}
}

func TestEngine_FlushesAfterIdleWindow(t *testing.T) {
	clock := newFakeClock()
	pub := &fakePublisher{}
	eng := newEngineWithClock(Config{IdleWindow: time.Second, MaxWait: 10 * time.Second}, pub, testLogger(), clock)

	eng.Ingest(baseEvent())
	if len(pub.Calls()) != 0 {
		t.Fatalf("expected no flush before idle window elapses, got %d calls", len(pub.Calls()))
	}

	clock.Advance(time.Second)
	calls := pub.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 flush after idle window, got %d", len(calls))
	}
	if len(calls[0].events) != 1 {
		t.Fatalf("expected 1 event in flush, got %d", len(calls[0].events))
	}
}

func TestEngine_IdleResetsButHardCapStillFires(t *testing.T) {
	clock := newFakeClock()
	pub := &fakePublisher{}
	eng := newEngineWithClock(Config{IdleWindow: time.Second, MaxWait: 3 * time.Second}, pub, testLogger(), clock)

	eng.Ingest(baseEvent())
	for i := range 5 {
		clock.Advance(500 * time.Millisecond)
		eng.Ingest(baseEvent())
		if len(pub.Calls()) != 0 {
			t.Fatalf("expected no flush before hard cap, got %d calls at iteration %d", len(pub.Calls()), i)
		}
	}

	clock.Advance(500 * time.Millisecond)
	calls := pub.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 flush at hard cap despite continuous events, got %d", len(calls))
	}
	if len(calls[0].events) != 6 {
		t.Fatalf("expected 6 batched events, got %d", len(calls[0].events))
	}
}

func TestEngine_CombineTriggers(t *testing.T) {
	tests := []struct {
		name            string
		combineTriggers bool
		wantCalls       int
	}{
		{name: "false splits by trigger", combineTriggers: false, wantCalls: 2},
		{name: "true merges triggers", combineTriggers: true, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock()
			pub := &fakePublisher{}
			eng := newEngineWithClock(Config{IdleWindow: time.Second, MaxWait: 10 * time.Second, CombineTriggers: tt.combineTriggers}, pub, testLogger(), clock)

			deployed := baseEvent()
			degraded := baseEvent()
			degraded.Trigger = "on-health-degraded"

			eng.Ingest(deployed)
			eng.Ingest(degraded)
			clock.Advance(time.Second)

			calls := pub.Calls()
			if len(calls) != tt.wantCalls {
				t.Fatalf("expected %d calls, got %d", tt.wantCalls, len(calls))
			}
		})
	}
}

func TestEngine_DifferentRevisionsSplitIntoSeparateSessions(t *testing.T) {
	clock := newFakeClock()
	pub := &fakePublisher{}
	eng := newEngineWithClock(Config{IdleWindow: time.Second, MaxWait: 10 * time.Second}, pub, testLogger(), clock)

	first := baseEvent()
	second := baseEvent()
	second.Revision = "rev-2"

	eng.Ingest(first)
	eng.Ingest(second)
	clock.Advance(time.Second)

	calls := pub.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls for 2 revisions, got %d", len(calls))
	}
	if calls[0].key.Revision == calls[1].key.Revision {
		t.Fatalf("expected distinct revisions in session keys, got %q twice", calls[0].key.Revision)
	}
}

func TestEngine_StaleTimerFireIsNoOp(t *testing.T) {
	clock := newFakeClock()
	pub := &fakePublisher{}
	eng := newEngineWithClock(Config{IdleWindow: time.Second, MaxWait: 10 * time.Second}, pub, testLogger(), clock)

	eng.Ingest(baseEvent())
	clock.Advance(time.Second)
	if len(pub.Calls()) != 1 {
		t.Fatalf("expected 1 call after first flush, got %d", len(pub.Calls()))
	}

	eng.onTimerFire("tatooine", 0)

	if len(pub.Calls()) != 1 {
		t.Fatalf("expected stale timer fire to be a no-op, got %d calls", len(pub.Calls()))
	}
}
