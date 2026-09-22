package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/BROngineer/argocd-notifier/internal/leader"
	"github.com/BROngineer/argocd-notifier/internal/leaderproxy"
)

// TestLeaderProxyFlow_RequestAlwaysHandledByTheRealLeader wires two real
// leader.Elector instances (a fake in-memory Lease, no real cluster needed)
// behind two real HTTP servers, each wrapped in leaderproxy.Handler exactly
// as cmd/argocd-notifier does — proving that no matter which "pod" a
// request lands on, it's the actual current leader's local handler that
// ends up processing it, and that this still holds after a failover.
func TestLeaderProxyFlow_RequestAlwaysHandledByTheRealLeader(t *testing.T) {
	client := fake.NewSimpleClientset()

	electorA, err := leader.New(client, leaderTestConfig("pod-a"), testLogger())
	if err != nil {
		t.Fatalf("New(pod-a) error = %v", err)
	}
	electorB, err := leader.New(client, leaderTestConfig("pod-b"), testLogger())
	if err != nil {
		t.Fatalf("New(pod-b) error = %v", err)
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	t.Cleanup(cancelA)
	t.Cleanup(cancelB)
	go electorA.Run(ctxA)
	go electorB.Run(ctxB)

	waitForLeader(t, electorA, electorB)

	// serverA/serverB stand in for the two pods; addrByIdentity simulates
	// what per-pod headless-Service DNS resolution would do in a real
	// cluster (identity -> reachable address).
	addrByIdentity := map[string]string{}

	newLocalHandler := func(identity string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(identity))
		})
	}

	leaderAddrFor := func(elector *leader.Elector) func() (string, bool) {
		return func() (string, bool) {
			identity, ok := elector.CurrentLeader()
			if !ok {
				return "", false
			}
			addr, ok := addrByIdentity[identity]
			return addr, ok
		}
	}

	serverA := httptest.NewServer(leaderproxy.New(electorA.IsLeader, leaderAddrFor(electorA), newLocalHandler("pod-a"), time.Second, testLogger()))
	t.Cleanup(serverA.Close)
	serverB := httptest.NewServer(leaderproxy.New(electorB.IsLeader, leaderAddrFor(electorB), newLocalHandler("pod-b"), time.Second, testLogger()))
	t.Cleanup(serverB.Close)

	addrByIdentity["pod-a"] = serverA.URL
	addrByIdentity["pod-b"] = serverB.URL

	assertBothServersReturn := func(t *testing.T, wantBody string) {
		t.Helper()
		for _, url := range []string{serverA.URL, serverB.URL} {
			resp, err := http.Post(url+"/events", "application/json", nil)
			if err != nil {
				t.Fatalf("post to %s: %v", url, err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status from %s = %d, want 200", url, resp.StatusCode)
			}
			if string(body) != wantBody {
				t.Fatalf("body from %s = %q, want %q (the real leader's identity, regardless of which pod was hit)", url, string(body), wantBody)
			}
		}
	}

	currentLeader, _ := electorA.CurrentLeader()
	assertBothServersReturn(t, currentLeader)

	// Simulate the leader's pod dying (its context is canceled, its Run
	// loop — and thus its own view of CurrentLeader — stops for good) and
	// confirm the survivor observes and forwards to the new leader.
	survivor, survivorURL := electorB, serverB.URL
	if electorA.IsLeader() {
		cancelA()
	} else {
		cancelB()
		survivor, survivorURL = electorA, serverA.URL
	}

	waitFor(t, 3*time.Second, func() bool {
		newLeader, ok := survivor.CurrentLeader()
		return ok && newLeader != currentLeader
	})

	newLeader, _ := survivor.CurrentLeader()
	resp, err := http.Post(survivorURL+"/events", "application/json", nil)
	if err != nil {
		t.Fatalf("post to survivor: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != newLeader {
		t.Fatalf("survivor response = (%d, %q), want (200, %q) after failover", resp.StatusCode, string(body), newLeader)
	}
}

func leaderTestConfig(identity string) leader.Config {
	return leader.Config{
		Namespace:     "default",
		LeaseName:     "test-lease",
		Identity:      identity,
		LeaseDuration: time.Second,
		RenewDeadline: 500 * time.Millisecond,
		RetryPeriod:   100 * time.Millisecond,
	}
}

// waitForLeader waits not just until someone is leader, but until every
// elector's own CurrentLeader() agrees — a standby's leaderproxy.Handler
// needs that to resolve an address, and it can lag briefly behind the
// leader's own IsLeader() flipping true (it learns of a new leader on its
// own poll cycle, not synchronously with the election itself).
func waitForLeader(t *testing.T, electors ...*leader.Elector) {
	t.Helper()
	waitFor(t, 2*time.Second, func() bool {
		var identity string
		for i, e := range electors {
			got, ok := e.CurrentLeader()
			if !ok {
				return false
			}
			if i == 0 {
				identity = got
			} else if got != identity {
				return false
			}
		}
		return true
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
