package registry

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const validRegisterBody = `{"name":"slack","baseURL":"http://slack-backend:8080","supportsThreadReply":true}`

func TestHandler_MalformedJSON(t *testing.T) {
	h := NewHandler(NewRegistry(time.Minute), testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/backends/register", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	h.RegisterBackend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_MissingFields(t *testing.T) {
	h := NewHandler(NewRegistry(time.Minute), testLogger())
	req := httptest.NewRequest(http.MethodPost, "/v1/backends/register", strings.NewReader(`{"baseURL":"http://x:8080"}`))
	rec := httptest.NewRecorder()
	h.RegisterBackend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_ValidRegistration(t *testing.T) {
	reg := NewRegistry(time.Minute)
	h := NewHandler(reg, testLogger())
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
}

func TestHandler_RepeatedRegistration_Heartbeat(t *testing.T) {
	reg := NewRegistry(time.Minute)
	h := NewHandler(reg, testLogger())

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/backends/register", strings.NewReader(validRegisterBody))
		rec := httptest.NewRecorder()
		h.RegisterBackend(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	}
}
