package leader

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(identity string) Config {
	return Config{
		Namespace:     "default",
		LeaseName:     "test-lease",
		Identity:      identity,
		LeaseDuration: time.Second,
		RenewDeadline: 500 * time.Millisecond,
		RetryPeriod:   100 * time.Millisecond,
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
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

func TestElector_SingleLeaderAndFailover(t *testing.T) {
	client := fake.NewSimpleClientset()

	e1, err := New(client, testConfig("pod-a"), testLogger())
	if err != nil {
		t.Fatalf("New(pod-a) error = %v", err)
	}
	e2, err := New(client, testConfig("pod-b"), testLogger())
	if err != nil {
		t.Fatalf("New(pod-b) error = %v", err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	t.Cleanup(cancel2)

	go e1.Run(ctx1)
	go e2.Run(ctx2)

	waitFor(t, 2*time.Second, func() bool { return e1.IsLeader() || e2.IsLeader() })

	leader, standby, cancelLeader := e1, e2, cancel1
	if e2.IsLeader() {
		leader, standby, cancelLeader = e2, e1, cancel2
	}

	if leader.IsLeader() == standby.IsLeader() {
		t.Fatalf("expected exactly one leader, got e1=%v e2=%v", e1.IsLeader(), e2.IsLeader())
	}

	leaderIdentity, ok := leader.CurrentLeader()
	if !ok {
		t.Fatal("leader.CurrentLeader() ok = false, want true")
	}
	if standbyIdentity, ok := standby.CurrentLeader(); !ok || standbyIdentity != leaderIdentity {
		t.Fatalf("standby.CurrentLeader() = (%q, %v), want (%q, true)", standbyIdentity, ok, leaderIdentity)
	}

	cancelLeader()

	waitFor(t, 3*time.Second, standby.IsLeader)

	newLeaderIdentity, ok := standby.CurrentLeader()
	if !ok || newLeaderIdentity == leaderIdentity {
		t.Fatalf("CurrentLeader() after failover = (%q, %v), want a different identity than %q", newLeaderIdentity, ok, leaderIdentity)
	}
}

func TestElector_CurrentLeader_UnknownBeforeElection(t *testing.T) {
	e, err := New(fake.NewSimpleClientset(), testConfig("pod-a"), testLogger())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, ok := e.CurrentLeader(); ok {
		t.Fatal("CurrentLeader() ok = true before any election, want false")
	}
}
