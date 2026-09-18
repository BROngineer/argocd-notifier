package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/notification"
)

func newTestClient(baseURL string, maxRetries int) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 2 * time.Second},
		token:      "test-token",
		baseURL:    baseURL,
		maxRetries: maxRetries,
	}
}

func TestNewClient_WithBaseURL(t *testing.T) {
	c := NewClient("test-token", time.Second, 0, WithBaseURL("https://example.com/api"))
	if c.baseURL != "https://example.com/api" {
		t.Fatalf("baseURL = %q, want https://example.com/api", c.baseURL)
	}
}

func TestClient_Post_Success(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody postMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(apiResponse{OK: true, TS: "111.222"})
	}))
	defer server.Close()

	c := newTestClient(server.URL, 0)
	ts, err := c.Post(context.Background(), "chan1", notification.Notification{Summary: "hi"})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if ts != "111.222" {
		t.Fatalf("ts = %q, want 111.222", ts)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotPath != "/chat.postMessage" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody.Channel != "chan1" || gotBody.Text != "hi" || gotBody.ThreadTS != "" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClient_Update_Success(t *testing.T) {
	var gotPath string
	var gotBody updateMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(apiResponse{OK: true})
	}))
	defer server.Close()

	c := newTestClient(server.URL, 0)
	if err := c.Update(context.Background(), "chan1", "111.222", notification.Notification{Summary: "updated"}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if gotPath != "/chat.update" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody.TS != "111.222" || gotBody.Channel != "chan1" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClient_PostThreadReply_SetsThreadTS(t *testing.T) {
	var gotBody postMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(apiResponse{OK: true, TS: "333.444"})
	}))
	defer server.Close()

	c := newTestClient(server.URL, 0)
	if err := c.PostThreadReply(context.Background(), "chan1", "111.222", "note"); err != nil {
		t.Fatalf("PostThreadReply() error = %v", err)
	}
	if gotBody.ThreadTS != "111.222" || gotBody.Text != "note" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClient_RetriesOnServerErrorThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(apiResponse{OK: false, Error: "internal_error"})
			return
		}
		_ = json.NewEncoder(w).Encode(apiResponse{OK: true, TS: "555.666"})
	}))
	defer server.Close()

	c := newTestClient(server.URL, 3)
	ts, err := c.Post(context.Background(), "chan1", notification.Notification{Summary: "hi"})
	if err != nil {
		t.Fatalf("Post() error = %v", err)
	}
	if ts != "555.666" {
		t.Fatalf("ts = %q, want 555.666", ts)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestClient_TerminalAPIErrorNoRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_ = json.NewEncoder(w).Encode(apiResponse{OK: false, Error: "channel_not_found"})
	}))
	defer server.Close()

	c := newTestClient(server.URL, 3)
	_, err := c.Post(context.Background(), "chan1", notification.Notification{Summary: "hi"})
	if err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("err = %v, want channel_not_found", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on terminal error)", got)
	}
}

func TestClient_ExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(apiResponse{OK: false, Error: "internal_error"})
	}))
	defer server.Close()

	c := newTestClient(server.URL, 2)
	_, err := c.Post(context.Background(), "chan1", notification.Notification{Summary: "hi"})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3 (1 initial + 2 retries)", got)
	}
}
