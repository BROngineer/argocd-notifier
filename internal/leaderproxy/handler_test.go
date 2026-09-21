package leaderproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandler_IsLeader_CallsLocal(t *testing.T) {
	var localCalled bool
	local := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		localCalled = true
		w.WriteHeader(http.StatusOK)
	})

	h := New(func() bool { return true }, func() (string, bool) {
		t.Fatal("leaderAddr should not be consulted when this instance is the leader")
		return "", false
	}, local, time.Second)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/events", nil))

	if !localCalled {
		t.Fatal("local handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandler_NotLeader_LeaderUnknown_Returns503(t *testing.T) {
	h := New(func() bool { return false }, func() (string, bool) { return "", false }, nil, time.Second)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/events", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHandler_NotLeader_ProxiesToLeader(t *testing.T) {
	var gotPath, gotBody string
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer leader.Close()

	h := New(func() bool { return false }, func() (string, bool) { return leader.URL, true }, nil, time.Second)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{"groupKey":"g"}`))
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if gotPath != "/events" {
		t.Fatalf("leader received path = %q, want /events", gotPath)
	}
	if gotBody != `{"groupKey":"g"}` {
		t.Fatalf("leader received body = %q, want the original request body", gotBody)
	}
	if rec.Body.String() != `{"status":"accepted"}` {
		t.Fatalf("proxied response body = %q, want the leader's response passed through", rec.Body.String())
	}
}

func TestHandler_NotLeader_ProxyTimeout_Returns502(t *testing.T) {
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer leader.Close()

	h := New(func() bool { return false }, func() (string, bool) { return leader.URL, true }, nil, 20*time.Millisecond)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/events", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (leader response timed out)", rec.Code)
	}
}
