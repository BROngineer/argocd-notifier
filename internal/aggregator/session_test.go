package aggregator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/event"
	"github.com/BROngineer/argocd-notifier/internal/render"
)

type fakeBuilder struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (b *fakeBuilder) Build(_ map[string]event.Event) (render.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.err != nil {
		return render.Message{}, b.err
	}
	return render.Message{Text: "rendered"}, nil
}

func (b *fakeBuilder) Calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

type posterCall struct {
	kind    string
	channel string
	ts      string
	text    string
}

type fakePoster struct {
	mu      sync.Mutex
	calls   []posterCall
	nextTS  int
	postErr map[string]error
}

func (p *fakePoster) Post(_ context.Context, channel string, _ render.Message) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err, ok := p.postErr[channel]; ok {
		return "", err
	}
	p.nextTS++
	ts := fmt.Sprintf("ts-%d", p.nextTS)
	p.calls = append(p.calls, posterCall{kind: "post", channel: channel, ts: ts})
	return ts, nil
}

func (p *fakePoster) Update(_ context.Context, channel, ts string, _ render.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, posterCall{kind: "update", channel: channel, ts: ts})
	return nil
}

func (p *fakePoster) PostThreadReply(_ context.Context, channel, threadTs, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, posterCall{kind: "thread", channel: channel, ts: threadTs, text: text})
	return nil
}

func (p *fakePoster) Calls() []posterCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]posterCall(nil), p.calls...)
}

func TestSessionPublisher_FirstUpsertPosts(t *testing.T) {
	builder := &fakeBuilder{}
	poster := &fakePoster{}
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour}, builder, poster, testLogger(), newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	if err := sp.Upsert(context.Background(), key, []event.Event{baseEvent()}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := poster.Calls()
	if len(calls) != 1 || calls[0].kind != "post" {
		t.Fatalf("expected 1 post call, got %+v", calls)
	}
	if builder.Calls() != 1 {
		t.Fatalf("expected builder called once, got %d", builder.Calls())
	}
}

func TestSessionPublisher_RealChangeCallsUpdate(t *testing.T) {
	builder := &fakeBuilder{}
	poster := &fakePoster{}
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour}, builder, poster, testLogger(), newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	first := baseEvent()
	first.HealthStatus = "Degraded"
	if err := sp.Upsert(context.Background(), key, []event.Event{first}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	second := first
	second.HealthStatus = "Healthy"
	if err := sp.Upsert(context.Background(), key, []event.Event{second}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := poster.Calls()
	if len(calls) != 2 || calls[1].kind != "update" || calls[1].ts != calls[0].ts {
		t.Fatalf("expected [post, update] with matching ts, got %+v", calls)
	}
}

func TestSessionPublisher_DuplicateContent_DropDefault(t *testing.T) {
	builder := &fakeBuilder{}
	poster := &fakePoster{}
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour, DuplicateAction: DuplicateActionDrop}, builder, poster, testLogger(), newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := poster.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected duplicate content to trigger no extra calls, got %+v", calls)
	}
	if builder.Calls() != 1 {
		t.Fatalf("expected builder not called again for duplicate content, got %d", builder.Calls())
	}
}

func TestSessionPublisher_DuplicateContent_Thread(t *testing.T) {
	builder := &fakeBuilder{}
	poster := &fakePoster{}
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour, DuplicateAction: DuplicateActionThread}, builder, poster, testLogger(), newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := poster.Calls()
	if len(calls) != 2 || calls[1].kind != "thread" || calls[1].ts != calls[0].ts {
		t.Fatalf("expected [post, thread] with matching ts, got %+v", calls)
	}
}

func TestSessionPublisher_SessionTTLExpiry(t *testing.T) {
	builder := &fakeBuilder{}
	poster := &fakePoster{}
	clock := newFakeClock()
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Minute}, builder, poster, testLogger(), clock)

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	clock.Advance(2 * time.Minute)
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := poster.Calls()
	if len(calls) != 2 || calls[0].kind != "post" || calls[1].kind != "post" {
		t.Fatalf("expected [post, post] after TTL expiry, got %+v", calls)
	}
	if calls[0].ts == calls[1].ts {
		t.Fatalf("expected a new ts after TTL expiry, got same ts twice: %s", calls[0].ts)
	}
}

func TestSessionPublisher_MultiChannelRecipients(t *testing.T) {
	builder := &fakeBuilder{}
	poster := &fakePoster{}
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour}, builder, poster, testLogger(), newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	first := baseEvent()
	first.Recipient = "chan1;chan2"
	if err := sp.Upsert(context.Background(), key, []event.Event{first}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	second := first
	second.HealthStatus = "Degraded"
	if err := sp.Upsert(context.Background(), key, []event.Event{second}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := poster.Calls()
	if len(calls) != 4 {
		t.Fatalf("expected 2 posts + 2 updates across 2 channels, got %+v", calls)
	}
	posts, updates := 0, 0
	for _, c := range calls {
		switch c.kind {
		case "post":
			posts++
		case "update":
			updates++
		}
	}
	if posts != 2 || updates != 2 {
		t.Fatalf("expected 2 posts and 2 updates, got posts=%d updates=%d", posts, updates)
	}
}

func TestSessionPublisher_PosterErrorOnOneChannelDoesNotBlockOthers(t *testing.T) {
	builder := &fakeBuilder{}
	poster := &fakePoster{postErr: map[string]error{"chan1": fmt.Errorf("boom")}}
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour}, builder, poster, testLogger(), newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	ev.Recipient = "chan1;chan2"

	err := sp.Upsert(context.Background(), key, []event.Event{ev})
	if err == nil || !strings.Contains(err.Error(), "chan1") {
		t.Fatalf("expected error mentioning chan1, got %v", err)
	}

	calls := poster.Calls()
	if len(calls) != 1 || calls[0].channel != "chan2" {
		t.Fatalf("expected chan2 to still be posted to, got %+v", calls)
	}
}
