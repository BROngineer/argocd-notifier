package aggregator

import (
	"sync"
	"testing"
	"time"
)

func TestRealClock_Now(t *testing.T) {
	c := realClock{}
	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Fatalf("Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestRealClock_AfterFunc(t *testing.T) {
	c := realClock{}
	done := make(chan struct{})
	c.AfterFunc(10*time.Millisecond, func() { close(done) })

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AfterFunc callback did not fire in time")
	}
}

// fakeClock lets tests advance time deterministically instead of sleeping.
// Timers scheduled via AfterFunc only fire when Advance moves "now" past
// their deadline; callbacks run synchronously on the caller's goroutine.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{deadline: c.now.Add(d), fn: f, active: true, clock: c}
	c.timers = append(c.timers, t)
	return t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var due []*fakeTimer
	for _, t := range c.timers {
		t.mu.Lock()
		if t.active && !t.deadline.After(now) {
			due = append(due, t)
			t.active = false
		}
		t.mu.Unlock()
	}
	c.mu.Unlock()

	for _, t := range due {
		t.fn()
	}
}

type fakeTimer struct {
	mu       sync.Mutex
	deadline time.Time
	fn       func()
	active   bool
	clock    *fakeClock
}

func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := t.active
	t.active = false
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := t.active
	t.deadline = t.clock.Now().Add(d)
	t.active = true
	return wasActive
}
