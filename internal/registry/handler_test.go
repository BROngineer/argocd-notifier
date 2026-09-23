package registry

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newRecordingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

const validRegisterBody = `{"name":"slack","baseURL":"http://slack-backend:8080","supportsThreadReply":true}`

func TestHandler_MalformedJSON(t *testing.T) {
	logger, logs := newRecordingLogger()
	h := NewHandler(NewRegistry(time.Minute), logger)
	req := httptest.NewRequest(http.MethodPost, "/v1/backends/register", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.RegisterBackend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "malformed") {
		t.Fatalf("expected a WARN log mentioning the malformed request, got %q", logs.String())
	}
}

func TestHandler_MissingFields(t *testing.T) {
	logger, logs := newRecordingLogger()
	h := NewHandler(NewRegistry(time.Minute), logger)
	req := httptest.NewRequest(http.MethodPost, "/v1/backends/register", strings.NewReader(`{"baseURL":"http://x:8080"}`))
	rec := httptest.NewRecorder()
	h.RegisterBackend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "rejected") {
		t.Fatalf("expected a WARN log mentioning the rejected registration, got %q", logs.String())
	}
}

func TestHandler_ValidRegistration(t *testing.T) {
	reg := NewRegistry(time.Minute)
	logger, logs := newRecordingLogger()
	h := NewHandler(reg, logger)
	req := httptest.NewRequest(http.MethodPost, "/v1/backends/register", strings.NewReader(validRegisterBody))
	rec := httptest.NewRecorder()
	h.RegisterBackend(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	got, ok := reg.Lookup("slack")
	if !ok {
		t.Fatal("Lookup(\"slack\") ok = false, want true")
	}
	if got.BaseURL != "http://slack-backend:8080" || !got.SupportsThreadReply {
		t.Fatalf("Lookup() = %+v, want registered fields", got)
	}
	if !strings.Contains(logs.String(), "level=INFO") || !strings.Contains(logs.String(), "slack") {
		t.Fatalf("expected an INFO log naming the registered backend, got %q", logs.String())
	}
}

func TestHandler_RepeatedRegistration_Heartbeat(t *testing.T) {
	reg := NewRegistry(time.Minute)
	logger, logs := newRecordingLogger()
	h := NewHandler(reg, logger)

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/backends/register", strings.NewReader(validRegisterBody))
		rec := httptest.NewRecorder()
		h.RegisterBackend(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	}

	if got := strings.Count(logs.String(), "level=INFO"); got != 1 {
		t.Fatalf("INFO log count = %d, want exactly 1 (only the first registration)", got)
	}
	if !strings.Contains(logs.String(), "level=DEBUG") || !strings.Contains(logs.String(), "backend heartbeat") {
		t.Fatalf("expected a DEBUG log for the repeated registration, got %q", logs.String())
	}
}
