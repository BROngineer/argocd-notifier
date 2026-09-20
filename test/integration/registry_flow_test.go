package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/api/core"
	"github.com/BROngineer/argocd-notifier/internal/receiver"
	"github.com/BROngineer/argocd-notifier/internal/registry"
)

// newServerMux builds the same mux shape cmd/argocd-notifier/main.go wires:
// the events receiver and the backend registration endpoint sharing one mux,
// so a route added by core.HandlerFromMux can't silently shadow /events.
func newServerMux(t *testing.T) (serverURL string, reg *registry.Registry) {
	handler := receiver.NewHandler(32, testLogger())
	reg = registry.NewRegistry(time.Minute)
	registryHandler := registry.NewHandler(reg, testLogger())

	mux := http.NewServeMux()
	mux.Handle("/events", handler)
	core.HandlerFromMux(registryHandler, mux)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server.URL, reg
}

func TestRegistryFlow_RegisterThenLookup(t *testing.T) {
	serverURL, reg := newServerMux(t)

	resp, err := http.Post(serverURL+"/v1/backends/register", "application/json",
		strings.NewReader(`{"name":"slack","baseURL":"http://slack-backend:8080/","supportsThreadReply":true}`))
	if err != nil {
		t.Fatalf("post register: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	got, ok := reg.Lookup("slack")
	if !ok {
		t.Fatal("Lookup(\"slack\") ok = false, want true")
	}
	if got.BaseURL != "http://slack-backend:8080" || !got.SupportsThreadReply {
		t.Fatalf("Lookup() = %+v, want registered fields with trailing slash trimmed", got)
	}
}

func TestRegistryFlow_MalformedBody(t *testing.T) {
	serverURL, _ := newServerMux(t)

	resp, err := http.Post(serverURL+"/v1/backends/register", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("post register: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRegistryFlow_EventsRouteUnaffected(t *testing.T) {
	serverURL, _ := newServerMux(t)

	resp, err := http.Post(serverURL+"/events", "application/json",
		strings.NewReader(`{"groupKey":"g","appName":"a","trigger":"on-deployed","revision":"r","recipient":"c","backend":"slack"}`))
	if err != nil {
		t.Fatalf("post event: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (registry route must not shadow /events)", resp.StatusCode)
	}
}
