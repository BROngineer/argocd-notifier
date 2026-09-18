package aggregator

import (
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
