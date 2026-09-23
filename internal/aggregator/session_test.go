package aggregator

import (
	"context"
	"fmt"
	"sort"
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

// build encodes the sorted app names it was called with into the
// Notification's Summary, so tests can assert which apps ended up in
// which recipient's rendered view without a real renderer.
func (b *buildCounter) build(perApp map[string]event.Event) (notification.Notification, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.err != nil {
		return notification.Notification{}, b.err
	}
	names := make([]string, 0, len(perApp))
	for name := range perApp {
		names = append(names, name)
	}
	sort.Strings(names)
	return notification.Notification{Summary: strings.Join(names, ",")}, nil
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
	summary   string
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

func (b *fakeBackend) Post(_ context.Context, recipient string, n notification.Notification) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err, ok := b.postErr[recipient]; ok {
		return "", err
	}
	b.nextRef++
	ref := fmt.Sprintf("ref-%d", b.nextRef)
	b.calls = append(b.calls, backendCall{kind: "post", recipient: recipient, ref: ref, summary: n.Summary})
	return ref, nil
}

func (b *fakeBackend) Update(_ context.Context, recipient, ref string, n notification.Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, backendCall{kind: "update", recipient: recipient, ref: ref, summary: n.Summary})
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

// fakeNotifierBackend implements only notification.Notifier — every call
// returns a brand new ref regardless of what ref it was given, modeling a
// remote backend that always reposts (or otherwise never reuses a ref) yet
// must still have its returned ref tracked and passed back on the next call.
type fakeNotifierBackend struct {
	mu      sync.Mutex
	calls   []backendCall
	nextRef int
}

func (b *fakeNotifierBackend) Notify(_ context.Context, recipient, ref string, n notification.Notification) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextRef++
	newRef := fmt.Sprintf("nref-%d", b.nextRef)
	b.calls = append(b.calls, backendCall{kind: "notify", recipient: recipient, ref: ref, summary: n.Summary})
	return newRef, nil
}

func (b *fakeNotifierBackend) Calls() []backendCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backendCall(nil), b.calls...)
}

// fakeResolver is a map-based BackendResolver — every backend, including
// "slack", is resolved dynamically by name; there is no special-cased
// compiled-in backend to test separately from any other name.
type fakeResolver map[string]any

func (f fakeResolver) Resolve(name string) (any, bool) {
	b, ok := f[name]
	return b, ok
}

// newTestPublisher resolves backend under the name "slack" — matching
// baseEvent()'s Backend field — with no other names resolvable unless the
// test needs one (see newTestPublisherMulti).
func newTestPublisher(t *testing.T, cfg PublisherConfig, build *buildCounter, backend any, clock Clock) *SessionPublisher {
	t.Helper()
	return newTestPublisherMulti(t, cfg, build, fakeResolver{"slack": backend}, clock)
}

func newTestPublisherMulti(t *testing.T, cfg PublisherConfig, build *buildCounter, backends fakeResolver, clock Clock) *SessionPublisher {
	t.Helper()
	return newSessionPublisherWithClock(cfg, build.build, backends, testLogger(), clock)
}

func TestSessionPublisher_FirstUpsertPosts(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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

func TestSessionPublisher_Upsert_LogsSendToBackendAtDebug(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	logger, logs := newRecordingLogger()
	sp := newSessionPublisherWithClock(PublisherConfig{SessionTTL: time.Hour}, build.build, fakeResolver{"slack": backend}, logger, newFakeClock())

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
	if err := sp.Upsert(context.Background(), key, []event.Event{baseEvent()}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	logged := logs.String()
	if !strings.Contains(logged, "level=DEBUG") || !strings.Contains(logged, "sending aggregated notification to backend") {
		t.Fatalf("expected a DEBUG log for the outgoing notification, got %q", logged)
	}
	if !strings.Contains(logged, "backend=slack") || !strings.Contains(logged, "recipient=test1234asdf") {
		t.Fatalf("expected the log to name the backend and recipient, got %q", logged)
	}
}

func TestSessionPublisher_RealChangeCallsUpdate(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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

// TestSessionPublisher_MixedRecipientsRouteIndependently covers a real
// setup: one service, dev apps route sync events to one channel, prod apps
// route sync events to a different channel and health events to a third —
// all landing in the same flush (e.g. combineTriggers=true). Each channel
// must only see the apps actually addressed to it.
func TestSessionPublisher_MixedRecipientsRouteIndependently(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	dev := baseEvent()
	dev.AppName = "tatooine-dev-new-republic"
	dev.Trigger = "on-deployed"
	dev.Recipient = "feed-deploy-dev"

	prodDeployed := baseEvent()
	prodDeployed.AppName = "tatooine-prod-new-republic"
	prodDeployed.Trigger = "on-deployed"
	prodDeployed.Recipient = "feed-deploy"

	prodDegraded := baseEvent()
	prodDegraded.AppName = "tatooine-prod-empire"
	prodDegraded.Trigger = "on-health-degraded"
	prodDegraded.Recipient = "ops-alerts"

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
	if err := sp.Upsert(context.Background(), key, []event.Event{dev, prodDeployed, prodDegraded}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 posts (one per distinct recipient), got %+v", calls)
	}

	byRecipient := make(map[string]backendCall, len(calls))
	for _, c := range calls {
		if c.kind != "post" {
			t.Fatalf("expected all calls to be posts on first flush, got %+v", calls)
		}
		byRecipient[c.recipient] = c
	}

	tests := []struct {
		recipient string
		wantApps  string
	}{
		{"feed-deploy-dev", "tatooine-dev-new-republic"},
		{"feed-deploy", "tatooine-prod-new-republic"},
		{"ops-alerts", "tatooine-prod-empire"},
	}
	for _, tt := range tests {
		c, ok := byRecipient[tt.recipient]
		if !ok {
			t.Fatalf("expected a post to %q, got calls %+v", tt.recipient, calls)
		}
		if c.summary != tt.wantApps {
			t.Fatalf("recipient %q got apps %q, want exactly %q", tt.recipient, c.summary, tt.wantApps)
		}
	}
}

func TestSessionPublisher_BackendErrorOnOneChannelDoesNotBlockOthers(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{postErr: map[string]error{"chan1": fmt.Errorf("boom")}}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
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

func TestSessionPublisher_NotifierBackendTracksChangedRef(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeNotifierBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
	first := baseEvent()
	if err := sp.Upsert(context.Background(), key, []event.Event{first}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	second := first
	second.HealthStatus = "Degraded"
	if err := sp.Upsert(context.Background(), key, []event.Event{second}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 2 || calls[0].kind != "notify" || calls[1].kind != "notify" {
		t.Fatalf("expected [notify, notify], got %+v", calls)
	}
	if calls[0].ref != "" {
		t.Fatalf("first notify ref = %q, want empty (post)", calls[0].ref)
	}
	if calls[1].ref != "nref-1" {
		t.Fatalf("second notify ref = %q, want nref-1 (the ref returned by the first call)", calls[1].ref)
	}
}

func TestSessionPublisher_RoutesToDifferentBackendsByEventField(t *testing.T) {
	build := &buildCounter{}
	slackBackend := &fakeBackend{}
	otherBackend := &fakeBackend{}
	sp := newTestPublisherMulti(t, PublisherConfig{SessionTTL: time.Hour}, build, fakeResolver{"slack": slackBackend, "other": otherBackend}, newFakeClock())

	slackEvent := baseEvent()
	slackEvent.AppName = "app-slack"
	slackEvent.Recipient = "chan1"

	otherEvent := baseEvent()
	otherEvent.AppName = "app-other"
	otherEvent.Backend = "other"
	otherEvent.Recipient = "chan1" // same recipient string as slackEvent, deliberately: must not collide

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
	if err := sp.Upsert(context.Background(), key, []event.Event{slackEvent, otherEvent}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	slackCalls := slackBackend.Calls()
	if len(slackCalls) != 1 || slackCalls[0].summary != "app-slack" {
		t.Fatalf("slack backend calls = %+v, want exactly one post for app-slack", slackCalls)
	}
	otherCalls := otherBackend.Calls()
	if len(otherCalls) != 1 || otherCalls[0].summary != "app-other" {
		t.Fatalf("other backend calls = %+v, want exactly one post for app-other", otherCalls)
	}
}

func TestSessionPublisher_UnknownBackendLogsErrorAndContinuesOthers(t *testing.T) {
	build := &buildCounter{}
	backend := &fakeBackend{}
	sp := newTestPublisher(t, PublisherConfig{SessionTTL: time.Hour}, build, backend, newFakeClock())

	unknownEvent := baseEvent()
	unknownEvent.AppName = "app-unknown"
	unknownEvent.Backend = "nonexistent"
	unknownEvent.Recipient = "chan2"

	knownEvent := baseEvent()
	knownEvent.AppName = "app-known"
	knownEvent.Recipient = "chan1"

	key := SessionKey{GroupKey: "tatooine", Revision: "rev-1"}
	err := sp.Upsert(context.Background(), key, []event.Event{unknownEvent, knownEvent})
	if err == nil || !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("expected error mentioning \"nonexistent\", got %v", err)
	}

	calls := backend.Calls()
	if len(calls) != 1 || calls[0].recipient != "chan1" {
		t.Fatalf("expected the known backend's recipient to still be posted to, got %+v", calls)
	}
}
