package slackbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/internal/notification"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type call struct {
	kind      string
	recipient string
	ref       string
	text      string
}

type fakeBackend struct {
	mu      sync.Mutex
	calls   []call
	nextRef int
}

func (b *fakeBackend) Post(_ context.Context, recipient string, _ notification.Notification) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextRef++
	ref := fmt.Sprintf("ref-%d", b.nextRef)
	b.calls = append(b.calls, call{kind: "post", recipient: recipient, ref: ref})
	return ref, nil
}

func (b *fakeBackend) Update(_ context.Context, recipient, ref string, _ notification.Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call{kind: "update", recipient: recipient, ref: ref})
	return nil
}

func (b *fakeBackend) PostThreadReply(_ context.Context, recipient, ref, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call{kind: "thread", recipient: recipient, ref: ref, text: text})
	return nil
}

func (b *fakeBackend) Calls() []call {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]call(nil), b.calls...)
}

type fakePostOnlyBackend struct {
	mu    sync.Mutex
	calls []call
}

func (b *fakePostOnlyBackend) Post(_ context.Context, recipient string, _ notification.Notification) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call{kind: "post", recipient: recipient, ref: "fresh-ref"})
	return "fresh-ref", nil
}

func (b *fakePostOnlyBackend) Calls() []call {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]call(nil), b.calls...)
}

const validNotifyBody = `{"recipient":"chan1","ref":"","notification":{"summary":"s","items":[{"appName":"a","cluster":"c","trigger":"on-deployed"}]}}`

func TestHandler_Notify_MalformedJSON(t *testing.T) {
	h := NewHandler(&fakeBackend{}, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_Notify_EmptyRefPosts(t *testing.T) {
	backend := &fakeBackend{}
	h := NewHandler(backend, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(validNotifyBody))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	calls := backend.Calls()
	if len(calls) != 1 || calls[0].kind != "post" {
		t.Fatalf("expected 1 post call, got %+v", calls)
	}

	var result backendapi.NotifyResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Ref != calls[0].ref {
		t.Fatalf("response ref = %q, want %q", result.Ref, calls[0].ref)
	}
}

func TestHandler_Notify_NonEmptyRefUpdates(t *testing.T) {
	backend := &fakeBackend{}
	h := NewHandler(backend, testLogger())
	body := `{"recipient":"chan1","ref":"existing-ref","notification":{"summary":"s","items":[]}}`
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	calls := backend.Calls()
	if len(calls) != 1 || calls[0].kind != "update" || calls[0].ref != "existing-ref" {
		t.Fatalf("expected 1 update call with existing-ref, got %+v", calls)
	}

	var result backendapi.NotifyResult
	_ = json.NewDecoder(rec.Body).Decode(&result)
	if result.Ref != "existing-ref" {
		t.Fatalf("response ref = %q, want existing-ref (unchanged by Update)", result.Ref)
	}
}

func TestHandler_Notify_NonEmptyRefFallsBackToPostWithoutUpdater(t *testing.T) {
	backend := &fakePostOnlyBackend{}
	h := NewHandler(backend, testLogger())
	body := `{"recipient":"chan1","ref":"stale-ref","notification":{"summary":"s","items":[]}}`
	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Notify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	calls := backend.Calls()
	if len(calls) != 1 || calls[0].kind != "post" {
		t.Fatalf("expected 1 post call (no Updater support), got %+v", calls)
	}
}

func TestHandler_ThreadReply_MalformedJSON(t *testing.T) {
	h := NewHandler(&fakeBackend{}, testLogger())
	req := httptest.NewRequest(http.MethodPost, "/thread-reply", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.ThreadReply(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_ThreadReply_Success(t *testing.T) {
	backend := &fakeBackend{}
	h := NewHandler(backend, testLogger())
	body := `{"recipient":"chan1","ref":"ref-1","text":"again"}`
	req := httptest.NewRequest(http.MethodPost, "/thread-reply", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ThreadReply(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	calls := backend.Calls()
	if len(calls) != 1 || calls[0].kind != "thread" || calls[0].text != "again" {
		t.Fatalf("expected 1 thread call, got %+v", calls)
	}
}

func TestHandler_ThreadReply_NotSupported(t *testing.T) {
	backend := &fakePostOnlyBackend{}
	h := NewHandler(backend, testLogger())
	body := `{"recipient":"chan1","ref":"ref-1","text":"again"}`
	req := httptest.NewRequest(http.MethodPost, "/thread-reply", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ThreadReply(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
