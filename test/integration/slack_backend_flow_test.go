package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/api/backendapi"
	"github.com/BROngineer/argocd-notifier/api/core"
	"github.com/BROngineer/argocd-notifier/internal/notification"
	"github.com/BROngineer/argocd-notifier/internal/registry"
	"github.com/BROngineer/argocd-notifier/internal/remotebackend"
	"github.com/BROngineer/argocd-notifier/internal/slack"
	"github.com/BROngineer/argocd-notifier/internal/slackbackend"
)

// TestSlackBackendFlow_RegistersAndDeliversViaRemoteBackendAdapter wires the
// same two composition roots as the real binaries do — cmd/argocd-notifier's
// registry+remotebackend.Resolver and cmd/slack-backend's
// slackbackend.Handler+Registrar — against real HTTP servers, and proves a
// notification travels core -> registry lookup -> HTTP -> standalone
// backend -> Slack, exactly as it would in production.
func TestSlackBackendFlow_RegistersAndDeliversViaRemoteBackendAdapter(t *testing.T) {
	slackBaseURL, getSlackCalls := startMockSlack(t)

	// Core side: just the registry + its registration endpoint.
	reg := registry.NewRegistry(time.Minute)
	coreMux := http.NewServeMux()
	core.HandlerFromMux(registry.NewHandler(reg, testLogger()), coreMux)
	coreServer := httptest.NewServer(coreMux)
	t.Cleanup(coreServer.Close)

	// Standalone backend side: what cmd/slack-backend/main.go wires.
	slackClient := slack.NewClient("test-token", 2*time.Second, 1, slack.WithBaseURL(slackBaseURL))
	backendMux := http.NewServeMux()
	backendapi.HandlerFromMux(slackbackend.NewHandler(slackClient, testLogger()), backendMux)
	backendServer := httptest.NewServer(backendMux)
	t.Cleanup(backendServer.Close)

	registrar, err := slackbackend.NewRegistrar(
		coreServer.URL, "slack", backendServer.URL, true, 20*time.Millisecond, http.DefaultClient, testLogger())
	if err != nil {
		t.Fatalf("NewRegistrar() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go registrar.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for !registrar.HasRegistered() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the backend to register")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Now act as the core does at flush time: resolve "slack" via the
	// registry-backed adapter and deliver a notification through it.
	resolver := remotebackend.NewResolver(reg, http.DefaultClient)
	resolved, ok := resolver.Resolve("slack")
	if !ok {
		t.Fatal("Resolve(\"slack\") ok = false, want true after registration")
	}
	notifier, ok := resolved.(notification.Notifier)
	if !ok {
		t.Fatalf("resolved backend = %T, want notification.Notifier", resolved)
	}

	n := notification.Notification{
		Summary: "s",
		Items:   []notification.Item{{AppName: "app-a", Cluster: "c", Trigger: "on-deployed"}},
	}
	ref, err := notifier.Notify(context.Background(), "chan1", "", n)
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if ref == "" {
		t.Fatal("Notify() ref = \"\", want a non-empty Slack ts")
	}

	calls := waitForCallCount(t, getSlackCalls, 1)
	if calls[0].path != "/chat.postMessage" || calls[0].channel != "chan1" {
		t.Fatalf("expected 1 postMessage call to chan1, got %+v", calls)
	}
}
