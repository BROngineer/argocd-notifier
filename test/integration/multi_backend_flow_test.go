package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/api/core"
	"github.com/BROngineer/argocd-notifier/internal/aggregator"
	"github.com/BROngineer/argocd-notifier/internal/receiver"
	"github.com/BROngineer/argocd-notifier/internal/registry"
	"github.com/BROngineer/argocd-notifier/internal/remotebackend"
)

type remoteCall struct {
	recipient string
	appNames  []string
}

func startMockRemoteBackend(t *testing.T) (baseURL string, getCalls func() []remoteCall) {
	var mu sync.Mutex
	var calls []remoteCall
	var counter atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /notify", func(w http.ResponseWriter, r *http.Request) {
		var req backendapi.NotifyRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		names := make([]string, len(req.Notification.Items))
		for i, item := range req.Notification.Items {
			names[i] = item.AppName
		}

		mu.Lock()
		calls = append(calls, remoteCall{recipient: req.Recipient, appNames: names})
		mu.Unlock()

		counter.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(backendapi.NotifyResult{Ref: fmt.Sprintf("remote-ref-%d", counter.Load())})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server.URL, func() []remoteCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]remoteCall(nil), calls...)
	}
}

// newMultiBackendPipeline wires the same shape main.go does: one server mux
// exposing /events and /v1/backends/register, an aggregator.SessionPublisher
// whose resolver is a registry-backed remotebackend.Resolver — every
// backend name, with no special-cased compiled-in one, resolves the same
// way: a live registry lookup.
func newMultiBackendPipeline(t *testing.T) (serverURL string) {
	reg := registry.NewRegistry(time.Minute)
	resolver := remotebackend.NewResolver(reg, http.DefaultClient)

	publisher := aggregator.NewSessionPublisher(
		aggregator.PublisherConfig{SessionTTL: time.Hour, DuplicateAction: aggregator.DuplicateActionDrop},
		resolver,
		testLogger(),
	)

	engine := aggregator.NewEngine(
		aggregator.Config{IdleWindow: 50 * time.Millisecond, MaxWait: time.Second, CombineTriggers: true},
		publisher,
		testLogger(),
	)

	eventsHandler := receiver.NewHandler(32, testLogger())
	registryHandler := registry.NewHandler(reg, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	receiver.RunWorkers(ctx, eventsHandler.Events(), 2, engine.Ingest)

	server := httptest.NewServer(core.Handler(&coreServer{events: eventsHandler, registry: registryHandler}))
	t.Cleanup(server.Close)

	return server.URL
}

func registerRemoteBackend(t *testing.T, serverURL, name, baseURL string) {
	body := fmt.Sprintf(`{"name":%q,"baseURL":%q,"supportsThreadReply":false}`, name, baseURL)
	resp, err := http.Post(serverURL+"/v1/backends/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register backend: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("register backend status = %d, want 204", resp.StatusCode)
	}
}

func TestMultiBackendFlow_RoutesEventsToTheirOwnBackend(t *testing.T) {
	slackBaseURL, getSlackCalls := startMockRemoteBackend(t)
	customBaseURL, getCustomCalls := startMockRemoteBackend(t)

	serverURL := newMultiBackendPipeline(t)
	registerRemoteBackend(t, serverURL, "slack", slackBaseURL)
	registerRemoteBackend(t, serverURL, "custom", customBaseURL)

	postEvent(t, serverURL+"/events",
		`{"groupKey":"tatooine","appName":"app-slack","trigger":"on-deployed","revision":"rev-1","recipient":"chan1","backend":"slack"}`)
	postEvent(t, serverURL+"/events",
		`{"groupKey":"tatooine","appName":"app-custom","trigger":"on-deployed","revision":"rev-1","recipient":"chan1","backend":"custom"}`)

	deadline := time.Now().Add(2 * time.Second)
	for len(getSlackCalls()) < 1 || len(getCustomCalls()) < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: slack calls = %+v, custom calls = %+v", getSlackCalls(), getCustomCalls())
		}
		time.Sleep(10 * time.Millisecond)
	}

	slackCalls := getSlackCalls()
	if len(slackCalls) != 1 || len(slackCalls[0].appNames) != 1 || slackCalls[0].appNames[0] != "app-slack" {
		t.Fatalf("expected exactly 1 notify call for app-slack, got %+v", slackCalls)
	}

	customCalls := getCustomCalls()
	if len(customCalls) != 1 || len(customCalls[0].appNames) != 1 || customCalls[0].appNames[0] != "app-custom" {
		t.Fatalf("expected exactly 1 notify call for app-custom, got %+v", customCalls)
	}
}
