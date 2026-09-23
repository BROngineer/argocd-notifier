package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BROngineer/argocd-notifier/internal/aggregator"
	"github.com/BROngineer/argocd-notifier/internal/receiver"
	"github.com/BROngineer/argocd-notifier/internal/slack"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type slackCall struct {
	path        string
	channel     string
	requestTS   string
	respTS      string
	text        string
	attachments string
}

func startMockSlack(t *testing.T) (baseURL string, getCalls func() []slackCall) {
	var mu sync.Mutex
	var calls []slackCall
	var counter atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)

		var body map[string]any
		for k, v := range raw {
			var decoded any
			_ = json.Unmarshal(v, &decoded)
			if body == nil {
				body = map[string]any{}
			}
			body[k] = decoded
		}
		channel, _ := body["channel"].(string)
		requestTS, _ := body["ts"].(string)
		text, _ := body["text"].(string)
		var attachments string
		if v, ok := raw["attachments"]; ok {
			attachments = string(v)
		}

		respTS := requestTS
		if respTS == "" {
			respTS = fmt.Sprintf("ts-%d", counter.Add(1))
		}

		mu.Lock()
		calls = append(calls, slackCall{path: r.URL.Path, channel: channel, requestTS: requestTS, respTS: respTS, text: text, attachments: attachments})
		mu.Unlock()

		// Real Slack resolves whatever channel value a request named (often
		// a human-friendly name) to a canonical channel ID in its response -
		// simulated here with a distinct prefix so a test can tell apart
		// "used the raw recipient" from "used the resolved ID from ref".
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": respTS, "channel": "resolved-" + channel})
	}))
	t.Cleanup(server.Close)

	return server.URL, func() []slackCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]slackCall(nil), calls...)
	}
}

func waitForCallCount(t *testing.T, getCalls func() []slackCall, want int) []slackCall {
	deadline := time.Now().Add(2 * time.Second)
	for {
		calls := getCalls()
		if len(calls) >= want {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d slack calls, got %d: %+v", want, len(calls), calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// staticTestResolver resolves a fixed set of backends by name — used where
// a test wires a backend directly rather than through the registry (see
// multi_backend_flow_test.go for the full registry-backed path).
type staticTestResolver map[string]any

func (r staticTestResolver) Resolve(name string) (any, bool) {
	b, ok := r[name]
	return b, ok
}

func newPipeline(t *testing.T, slackBaseURL string) (receiverURL string) {
	slackClient := slack.NewClient("test-token", 2*time.Second, 1, slack.WithBaseURL(slackBaseURL))
	resolver := staticTestResolver{"slack": slackClient}

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

	handler := receiver.NewHandler(32, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	receiver.RunWorkers(ctx, handler.Events(), 2, engine.Ingest)

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server.URL
}

func postEvent(t *testing.T, receiverURL, body string) {
	resp, err := http.Post(receiverURL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post event: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
}

func TestAggregationFlow_PostThenUpdateSameTS(t *testing.T) {
	slackBaseURL, getCalls := startMockSlack(t)
	receiverURL := newPipeline(t, slackBaseURL)

	for i := range 3 {
		postEvent(t, receiverURL, fmt.Sprintf(
			`{"groupKey":"tatooine","appName":"app-%d","trigger":"on-deployed","revision":"rev-1","recipient":"chan1","backend":"slack"}`, i))
	}

	firstCalls := waitForCallCount(t, getCalls, 1)
	if len(firstCalls) != 1 || firstCalls[0].path != "/chat.postMessage" {
		t.Fatalf("expected 1 postMessage call, got %+v", firstCalls)
	}
	postedTS := firstCalls[0].respTS

	for i := range 3 {
		postEvent(t, receiverURL, fmt.Sprintf(
			`{"groupKey":"tatooine","appName":"app-%d","trigger":"on-health-degraded","revision":"rev-1","healthStatus":"Degraded","recipient":"chan1","backend":"slack"}`, i))
	}

	secondCalls := waitForCallCount(t, getCalls, 2)
	if len(secondCalls) != 2 || secondCalls[1].path != "/chat.update" {
		t.Fatalf("expected a second call to be chat.update, got %+v", secondCalls)
	}
	if secondCalls[1].requestTS != postedTS {
		t.Fatalf("update ts = %q, want %q (same message edited in place)", secondCalls[1].requestTS, postedTS)
	}
	// Regression coverage for the channel_not_found bug: chat.update must
	// use the channel ID Slack resolved at post time ("resolved-chan1"),
	// not the raw "recipient" the aggregator was configured with ("chan1")
	// — sending the latter to a real chat.update is exactly what produced
	// channel_not_found in production.
	wantChannel := "resolved-" + firstCalls[0].channel
	if secondCalls[1].channel != wantChannel {
		t.Fatalf("update channel = %q, want %q (the resolved channel ID from the original post, not the raw recipient)", secondCalls[1].channel, wantChannel)
	}
}

func TestAggregationFlow_DifferentRevisionIndependentPost(t *testing.T) {
	slackBaseURL, getCalls := startMockSlack(t)
	receiverURL := newPipeline(t, slackBaseURL)

	postEvent(t, receiverURL, `{"groupKey":"tatooine","appName":"app-a","trigger":"on-deployed","revision":"rev-1","recipient":"chan1","backend":"slack"}`)
	postEvent(t, receiverURL, `{"groupKey":"tatooine","appName":"app-a","trigger":"on-deployed","revision":"rev-2","recipient":"chan1","backend":"slack"}`)

	calls := waitForCallCount(t, getCalls, 2)
	if calls[0].path != "/chat.postMessage" || calls[1].path != "/chat.postMessage" {
		t.Fatalf("expected 2 independent postMessage calls, got %+v", calls)
	}
	if calls[0].respTS == calls[1].respTS {
		t.Fatalf("expected distinct ts per revision, got %q twice", calls[0].respTS)
	}
}
