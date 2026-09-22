package receiver

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/event"
)

func newRecordingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

const validEventBody = `{"groupKey":"tatooine","appName":"tatooine-dev","trigger":"on-deployed","revision":"rev-1","recipient":"chan1","backend":"slack"}`

func TestHandler_MalformedJSON(t *testing.T) {
	logger, logs := newRecordingLogger()
	h := NewHandler(1, logger)
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "malformed") {
		t.Fatalf("expected a WARN log mentioning the malformed payload, got %q", logs.String())
	}
}

func TestHandler_InvalidEvent(t *testing.T) {
	logger, logs := newRecordingLogger()
	h := NewHandler(1, logger)
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{"appName":"foo"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "invalid") {
		t.Fatalf("expected a WARN log mentioning the invalid event, got %q", logs.String())
	}
}

func TestHandler_ValidEvent_AcceptedAndEnqueued(t *testing.T) {
	logger, logs := newRecordingLogger()
	h := NewHandler(1, logger)
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(validEventBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	select {
	case ev := <-h.Events():
		if ev.AppName != "tatooine-dev" {
			t.Fatalf("appName = %q, want tatooine-dev", ev.AppName)
		}
	default:
		t.Fatal("expected event to be enqueued")
	}
	if !strings.Contains(logs.String(), "level=INFO") || !strings.Contains(logs.String(), "tatooine-dev") {
		t.Fatalf("expected an INFO log naming the accepted event, got %q", logs.String())
	}
}

func TestHandler_QueueFull(t *testing.T) {
	logger, logs := newRecordingLogger()
	h := NewHandler(1, logger)

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(validEventBody)))
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first request status = %d, want 202", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(validEventBody)))
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request status = %d, want 503", rec2.Code)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "queue full") {
		t.Fatalf("expected a WARN log mentioning the full queue, got %q", logs.String())
	}
}

func TestRunWorkers_DrainsAndInvokesIngest(t *testing.T) {
	const total = 5
	ch := make(chan event.Event, total)

	ctx := t.Context()

	var mu sync.Mutex
	var got []string
	var count atomic.Int32
	done := make(chan struct{})

	ingest := func(ev event.Event) {
		mu.Lock()
		got = append(got, ev.AppName)
		mu.Unlock()
		if count.Add(1) == total {
			close(done)
		}
	}

	RunWorkers(ctx, ch, 3, ingest)

	for i := range total {
		ch <- event.Event{GroupKey: "g", AppName: fmt.Sprintf("app-%d", i), Trigger: "on-deployed", Revision: "r", Recipient: "c"}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for all events to be ingested")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != total {
		t.Fatalf("got %d ingested events, want %d", len(got), total)
	}
}
