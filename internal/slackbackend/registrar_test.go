package slackbackend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/api/core"
)

type fakeCoreServer struct {
	*httptest.Server
	mu     sync.Mutex
	calls  []core.RegisterBackendRequest
	status int
}

func newFakeCoreServer() *fakeCoreServer {
	fs := &fakeCoreServer{status: http.StatusNoContent}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/backends/register", func(w http.ResponseWriter, r *http.Request) {
		var req core.RegisterBackendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		fs.mu.Lock()
		fs.calls = append(fs.calls, req)
		status := fs.status
		fs.mu.Unlock()

		w.WriteHeader(status)
	})
	fs.Server = httptest.NewServer(mux)
	return fs
}

func (fs *fakeCoreServer) Calls() []core.RegisterBackendRequest {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]core.RegisterBackendRequest(nil), fs.calls...)
}

func waitForCallCountAtLeast(t *testing.T, get func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for get() < want {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for at least %d calls, got %d", want, get())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRegistrar_RegistersImmediatelyAndHeartbeats(t *testing.T) {
	fs := newFakeCoreServer()
	defer fs.Close()

	logger, logs := newRecordingLogger()
	r, err := NewRegistrar(fs.URL, "slack", "http://slack-backend:8081", true, 20*time.Millisecond, http.DefaultClient, logger)
	if err != nil {
		t.Fatalf("NewRegistrar() error = %v", err)
	}

	ctx := t.Context()
	go r.Run(ctx)

	waitForCallCountAtLeast(t, func() int { return len(fs.Calls()) }, 2)

	calls := fs.Calls()
	if calls[0].Name != "slack" || calls[0].BaseURL != "http://slack-backend:8081" || !calls[0].SupportsThreadReply {
		t.Fatalf("first call = %+v, want matching registration fields", calls[0])
	}
	if !r.HasRegistered() {
		t.Fatal("HasRegistered() = false, want true after a successful registration")
	}

	waitFor(t, time.Second, func() bool { return strings.Contains(logs.String(), "heartbeat refreshed") })

	logged := logs.String()
	if !strings.Contains(logged, "level=INFO") || !strings.Contains(logged, "msg=registered") {
		t.Fatalf("expected an INFO log for the first registration, got %q", logged)
	}
	if !strings.Contains(logged, "heartbeat refreshed") {
		t.Fatalf("expected an INFO log for the subsequent heartbeat, got %q", logged)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRegistrar_FailedAttemptDoesNotPanicAndKeepsRetrying(t *testing.T) {
	fs := newFakeCoreServer()
	defer fs.Close()
	fs.status = http.StatusBadRequest

	r, err := NewRegistrar(fs.URL, "slack", "http://slack-backend:8081", true, 20*time.Millisecond, http.DefaultClient, testLogger())
	if err != nil {
		t.Fatalf("NewRegistrar() error = %v", err)
	}

	ctx := t.Context()
	go r.Run(ctx)

	waitForCallCountAtLeast(t, func() int { return len(fs.Calls()) }, 2)

	if r.HasRegistered() {
		t.Fatal("HasRegistered() = true, want false — every attempt was rejected")
	}
}

func TestRegistrar_StopsOnContextCancel(t *testing.T) {
	fs := newFakeCoreServer()
	defer fs.Close()

	r, err := NewRegistrar(fs.URL, "slack", "http://slack-backend:8081", true, 5*time.Millisecond, http.DefaultClient, testLogger())
	if err != nil {
		t.Fatalf("NewRegistrar() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	waitForCallCountAtLeast(t, func() int { return len(fs.Calls()) }, 1)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}
