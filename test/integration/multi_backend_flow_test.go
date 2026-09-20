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
	"github.com/BROngineer/argocd-notifier/internal/slack"
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
// whose resolver falls back to a registry-backed remotebackend.Resolver for
// any backend name other than the compiled-in "slack" one.
func newMultiBackendPipeline(t *testing.T, slackBaseURL string) (serverURL string) {
	slackClient := slack.NewClient("test-token", 2*time.Second, 1, slack.WithBaseURL(slackBaseURL))

	reg := registry.NewRegistry(time.Minute)
	remoteResolver := remotebackend.NewResolver(reg, http.DefaultClient)
	resolver := aggregator.NewStaticResolver("slack", slackClient, remoteResolver)

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

	mux := http.NewServeMux()
	mux.Handle("/events", eventsHandler)
	core.HandlerFromMux(registryHandler, mux)

	server := httptest.NewServer(mux)
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
	slackBaseURL, getSlackCalls := startMockSlack(t)
	remoteBaseURL, getRemoteCalls := startMockRemoteBackend(t)

	serverURL := newMultiBackendPipeline(t, slackBaseURL)
	registerRemoteBackend(t, serverURL, "custom", remoteBaseURL)

	postEvent(t, serverURL+"/events",
		`{"groupKey":"tatooine","appName":"app-slack","trigger":"on-deployed","revision":"rev-1","recipient":"chan1","backend":"slack"}`)
	postEvent(t, serverURL+"/events",
		`{"groupKey":"tatooine","appName":"app-custom","trigger":"on-deployed","revision":"rev-1","recipient":"chan1","backend":"custom"}`)

	deadline := time.Now().Add(2 * time.Second)
	for len(getSlackCalls()) < 1 || len(getRemoteCalls()) < 1 {

		if time.Now().After(deadline) {
			t.Fatalf("timed out: slack calls = %+v, remote calls = %+v", getSlackCalls(), getRemoteCalls())
		}
		time.Sleep(10 * time.Millisecond)
	}

	slackCalls := getSlackCalls()
	if len(slackCalls) != 1 || slackCalls[0].path != "/chat.postMessage" {
		t.Fatalf("expected exactly 1 slack postMessage call, got %+v", slackCalls)
	}

	remoteCalls := getRemoteCalls()
	if len(remoteCalls) != 1 || len(remoteCalls[0].appNames) != 1 || remoteCalls[0].appNames[0] != "app-custom" {
		t.Fatalf("expected exactly 1 remote notify call for app-custom, got %+v", remoteCalls)
	}
}
