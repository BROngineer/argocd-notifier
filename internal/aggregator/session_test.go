package aggregator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/event"
	"github.com/BROngineer/argocd-notifier/internal/notification"
)

type buildCounter struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (b *buildCounter) build(_ map[string]event.Event) (notification.Notification, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.err != nil {
		return notification.Notification{}, b.err
	}
	return notification.Notification{Summary: "built"}, nil
}

func (b *buildCounter) Calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

type backendCall struct {
	kind      string
	recipient string
	ref       string
	text      string
}

// fakeBackend implements Backend, Updater, and ThreadReplier — the
// full-featured case. fakePostOnlyBackend (below) implements only Backend,
// for testing graceful degradation and fail-fast validation.
type fakeBackend struct {
	mu      sync.Mutex
	calls   []backendCall
	nextRef int
	postErr map[string]error
}

func (b *fakeBackend) Post(_ context.Context, recipient string, _ notification.Notification) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err, ok := b.postErr[recipient]; ok {
		return "", err
	}
	b.nextRef++
	ref := fmt.Sprintf("ref-%d", b.nextRef)
	b.calls = append(b.calls, backendCall{kind: "post", recipient: recipient, ref: ref})
	return ref, nil
}

func (b *fakeBackend) Update(_ context.Context, recipient, ref string, _ notification.Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, backendCall{kind: "update", recipient: recipient, ref: ref})
	return nil
}

func (b *fakeBackend) PostThreadReply(_ context.Context, recipient, ref, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, backendCall{kind: "thread", recipient: recipient, ref: ref, text: text})
	return nil
}

func (b *fakeBackend) Calls() []backendCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backendCall(nil), b.calls...)
}

type fakePostOnlyBackend struct {
	mu      sync.Mutex
	calls   []backendCall
	nextRef int
}

func (b *fakePostOnlyBackend) Post(_ context.Context, recipient string, _ notification.Notification) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextRef++
	ref := fmt.Sprintf("ref-%d", b.nextRef)
	b.calls = append(b.calls, backendCall{kind: "post", recipient: recipient, ref: ref})
	return ref, nil
}

func (b *fakePostOnlyBackend) Calls() []backendCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backendCall(nil), b.calls...)
}

func newTestPublisher(t *testing.T, cfg PublisherConfig, build *buildCounter, backend notification.Backend, clock Clock) *SessionPublisher {
	t.Helper()
	sp, err := newSessionPublisherWithClock(cfg, build.build, backend, testLogger(), clock)
	if err != nil {
		t.Fatalf("newSessionPublisherWithClock() error = %v", err)
	}
	return sp
}

func TestNewSessionPublisher_ThreadActionRequiresThreadReplier(t *testing.T) {
	_, err := newSessionPublisherWithClock(
		PublisherConfig{SessionTTL: time.Hour, DuplicateAction: DuplicateActionThread},
		(&buildCounter{}).build,
		&fakePostOnlyBackend{},
		testLogger(),
		newFakeClock(),
	)
	if !strings.Contains(fmt.Sprint(err), "BackendDoesNotSupportThreadReply") {
		t.Fatalf("error = %v, want ErrBackendDoesNotSupportThreadReply", err)
	}
}

func TestSessionPublisher_FirstUpsertPosts(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	if err := sp.Upsert(context.Background(), key, []event.Event{baseEvent()}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 1 || calls[0].kind != "post" {
		t.Fatalf("expected 1 post call, got %+v", calls)
	}
	if build.Calls() != 1 {
		t.Fatalf("expected build called once, got %d", build.Calls())
	}
}

func TestSessionPublisher_RealChangeCallsUpdate(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

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

	calls := backend.Calls()
	if len(calls) != 2 || calls[1].kind != "update" || calls[1].ref != calls[0].ref {
		t.Fatalf("expected [post, update] with matching ref, got %+v", calls)
	}
}

func TestSessionPublisher_DuplicateContent_DropDefault(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour, DuplicateAction: DuplicateActionDrop}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected duplicate content to trigger no extra calls, got %+v", calls)
	}
	if build.Calls() != 1 {
		t.Fatalf("expected build not called again for duplicate content, got %d", build.Calls())
	}
}

func TestSessionPublisher_DuplicateContent_Thread(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour, DuplicateAction: DuplicateActionThread}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 2 || calls[1].kind != "thread" || calls[1].ref != calls[0].ref {
		t.Fatalf("expected [post, thread] with matching ref, got %+v", calls)
	}
}

func TestSessionPublisher_SessionTTLExpiry(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	clock := newFakeClock()
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Minute}, build, backend, clock)

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	clock.Advance(2 * time.Minute)
	if err := sp.Upsert(context.Background(), key, []event.Event{ev}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 2 || calls[0].kind != "post" || calls[1].kind != "post" {
		t.Fatalf("expected [post, post] after TTL expiry, got %+v", calls)
	}
	if calls[0].ref == calls[1].ref {
		t.Fatalf("expected a new ref after TTL expiry, got same ref twice: %s", calls[0].ref)
	}
}

func TestSessionPublisher_MultiChannelRecipients(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

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

	calls := backend.Calls()
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

func TestSessionPublisher_BackendErrorOnOneChannelDoesNotBlockOthers(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{postErr: map[string]error{"chan1": fmt.Errorf("boom")}}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "camel", Revision: "rev-1"}
	ev := baseEvent()
	ev.Recipient = "chan1;chan2"

	err := sp.Upsert(context.Background(), key, []event.Event{ev})
	if err == nil || !strings.Contains(err.Error(), "chan1") {
		t.Fatalf("expected error mentioning chan1, got %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 1 || calls[0].recipient != "chan2" {
		t.Fatalf("expected chan2 to still be posted to, got %+v", calls)
	}
}

func TestSessionPublisher_PostOnlyBackendAlwaysPostsFresh(t *testing.T) {
	build := &buildCounter{}
	backend := &fakePostOnlyBackend{}
	sp, err := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour}, build.build, backend, testLogger(), newFakeClock())
	if err != nil {
		t.Fatalf("newSessionPublisherWithClock() error = %v", err)
	}

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

	calls := backend.Calls()
	if len(calls) != 2 || calls[0].kind != "post" || calls[1].kind != "post" {
		t.Fatalf("expected [post, post] for a backend without Updater support, got %+v", calls)
	}
	if calls[0].ref == calls[1].ref {
		t.Fatalf("expected distinct refs since no ref is ever reused without Updater support")
	}
}
