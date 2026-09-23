package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	coreHTTPServer := httptest.NewServer(core.Handler(&coreServer{registry: registry.NewHandler(reg, testLogger())}))
	t.Cleanup(coreHTTPServer.Close)

	// Standalone backend side: what cmd/slack-backend/main.go wires.
	slackClient := slack.NewClient("test-token", 2*time.Second, 1, slack.WithBaseURL(slackBaseURL))
	backendMux := http.NewServeMux()
	backendapi.HandlerFromMux(slackbackend.NewHandler(slackClient, testLogger()), backendMux)
	backendServer := httptest.NewServer(backendMux)
	t.Cleanup(backendServer.Close)

	registrar, err := slackbackend.NewRegistrar(
		coreHTTPServer.URL, "slack", backendServer.URL, true, 20*time.Millisecond, http.DefaultClient, testLogger())
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

// TestSlackBackendFlow_TargetRevisionSurvivesWireRoundTrip guards against a
// real bug: TargetRevision was added to notification.Item/event.Event but
// the backendapi wire schema (and remotebackend.toWireItem/
// slackbackend.fromWireItem) were never updated to carry it, so it silently
// vanished on the core -> standalone-backend HTTP hop despite being present
// on both sides of it. A custom template makes the value observable in the
// final Slack payload without relying on DefaultRenderer, which doesn't
// surface TargetRevision at all.
func TestSlackBackendFlow_TargetRevisionSurvivesWireRoundTrip(t *testing.T) {
	slackBaseURL, getSlackCalls := startMockSlack(t)

	path := filepath.Join(t.TempDir(), "message.tmpl")
	src := `{{define "text"}}{{.Summary}}{{end}}{{define "attachments"}}[{{range $i, $item := .Items}}{{if $i}},{{end}}{"color":"#000","blocks":[{"type":"section","text":{"type":"mrkdwn","text":"{{$item.TargetRevision}}"}}]}{{end}}]{{end}}`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
	watcher := slack.NewTemplateWatcher(path, false, testLogger())
	watcher.Reload()

	reg := registry.NewRegistry(time.Minute)
	coreHTTPServer := httptest.NewServer(core.Handler(&coreServer{registry: registry.NewHandler(reg, testLogger())}))
	t.Cleanup(coreHTTPServer.Close)

	slackClient := slack.NewClient("test-token", 2*time.Second, 1, slack.WithBaseURL(slackBaseURL), slack.WithRenderer(watcher))
	backendMux := http.NewServeMux()
	backendapi.HandlerFromMux(slackbackend.NewHandler(slackClient, testLogger()), backendMux)
	backendServer := httptest.NewServer(backendMux)
	t.Cleanup(backendServer.Close)

	if _, err := reg.Register("slack", backendServer.URL, false); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	resolver := remotebackend.NewResolver(reg, http.DefaultClient)
	resolved, ok := resolver.Resolve("slack")
	if !ok {
		t.Fatal("Resolve(\"slack\") ok = false, want true")
	}
	notifier, ok := resolved.(notification.Notifier)
	if !ok {
		t.Fatalf("resolved backend = %T, want notification.Notifier", resolved)
	}

	n := notification.Notification{
		Summary: "s",
		Items:   []notification.Item{{AppName: "app-a", Cluster: "c", Trigger: "on-deployed", TargetRevision: "v0.21.0"}},
	}
	if _, err := notifier.Notify(context.Background(), "chan1", "", n); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	calls := waitForCallCount(t, getSlackCalls, 1)
	if !strings.Contains(calls[0].attachments, "v0.21.0") {
		t.Fatalf("attachments = %s, want TargetRevision (v0.21.0) to have survived the wire round trip", calls[0].attachments)
	}
}

// TestSlackBackendFlow_CustomMessageTemplate wires a slack.TemplateWatcher
// pointed at a temp file the same way cmd/slack-backend/main.go does when
// MESSAGE_TEMPLATE_PATH is set, and proves the custom template's output —
// not the built-in DefaultRenderer's — is what actually reaches Slack.
func TestSlackBackendFlow_CustomMessageTemplate(t *testing.T) {
	slackBaseURL, getSlackCalls := startMockSlack(t)

	path := filepath.Join(t.TempDir(), "message.tmpl")
	src := `{{define "text"}}CUSTOM: {{.Summary}}{{end}}{{define "attachments"}}[{"color":"#custom","blocks":[]}]{{end}}`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}

	watcher := slack.NewTemplateWatcher(path, false, testLogger())
	watcher.Reload()

	slackClient := slack.NewClient("test-token", 2*time.Second, 1, slack.WithBaseURL(slackBaseURL), slack.WithRenderer(watcher))
	backendMux := http.NewServeMux()
	backendapi.HandlerFromMux(slackbackend.NewHandler(slackClient, testLogger()), backendMux)
	backendServer := httptest.NewServer(backendMux)
	t.Cleanup(backendServer.Close)

	resp, err := http.Post(backendServer.URL+"/notify", "application/json",
		strings.NewReader(`{"recipient":"chan1","ref":"","notification":{"summary":"s","items":[{"appName":"a","cluster":"c","trigger":"on-deployed"}]}}`))
	if err != nil {
		t.Fatalf("POST /notify: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	calls := waitForCallCount(t, getSlackCalls, 1)
	if calls[0].text != "CUSTOM: s" {
		t.Fatalf("text = %q, want the custom template's text", calls[0].text)
	}
	if !strings.Contains(calls[0].attachments, "#custom") {
		t.Fatalf("attachments = %s, want the custom template's attachments", calls[0].attachments)
	}
}
