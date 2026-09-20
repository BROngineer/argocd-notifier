package registry

import (
	"errors"
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := newRegistryWithClock(time.Minute, newFakeClock())

	if err := r.Register("slack", "http://slack-backend:8080", true); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	got, ok := r.Lookup("slack")
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	want := Backend{Name: "slack", BaseURL: "http://slack-backend:8080", SupportsThreadReply: true, LastSeen: time.Unix(0, 0)}
	if got != want {
		t.Fatalf("Lookup() = %+v, want %+v", got, want)
	}
}

func TestRegistry_Register_MissingFields(t *testing.T) {
	tests := []struct {
		name    string
		nameArg string
		baseURL string
		wantErr []error
	}{
		{name: "missing name", nameArg: "", baseURL: "http://x:8080", wantErr: []error{ErrMissingName}},
		{name: "missing baseURL", nameArg: "slack", baseURL: "", wantErr: []error{ErrMissingBaseURL}},
		{name: "missing both", nameArg: "", baseURL: "", wantErr: []error{ErrMissingName, ErrMissingBaseURL}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRegistryWithClock(time.Minute, newFakeClock())
			err := r.Register(tt.nameArg, tt.baseURL, false)
			for _, want := range tt.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("Register() = %v, want errors.Is(_, %v)", err, want)
				}
			}
		})
	}
}

func TestRegistry_Register_TrimsTrailingSlash(t *testing.T) {
	r := newRegistryWithClock(time.Minute, newFakeClock())

	if err := r.Register("slack", "http://slack-backend:8080/", true); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	got, ok := r.Lookup("slack")
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if got.BaseURL != "http://slack-backend:8080" {
		t.Fatalf("BaseURL = %q, want trailing slash trimmed", got.BaseURL)
	}
}

func TestRegistry_Register_RefreshesHeartbeat(t *testing.T) {
	clock := newFakeClock()
	r := newRegistryWithClock(time.Minute, clock)

	if err := r.Register("slack", "http://old:8080", false); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	clock.Advance(30 * time.Second)
	if err := r.Register("slack", "http://new:8080", true); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	got, ok := r.Lookup("slack")
	if !ok {
		t.Fatal("Lookup() ok = false, want true")
	}
	if got.BaseURL != "http://new:8080" || !got.SupportsThreadReply {
		t.Fatalf("Lookup() = %+v, want refreshed fields", got)
	}
	if !got.LastSeen.Equal(clock.Now()) {
		t.Fatalf("LastSeen = %v, want %v", got.LastSeen, clock.Now())
	}
}

func TestRegistry_Lookup_UnknownName(t *testing.T) {
	r := newRegistryWithClock(time.Minute, newFakeClock())

	if _, ok := r.Lookup("nope"); ok {
		t.Fatal("Lookup() ok = true, want false for unknown name")
	}
}

func TestRegistry_Lookup_StaleEntryNotFound(t *testing.T) {
	clock := newFakeClock()
	r := newRegistryWithClock(time.Minute, clock)

	if err := r.Register("slack", "http://slack-backend:8080", false); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	clock.Advance(2 * time.Minute)

	if _, ok := r.Lookup("slack"); ok {
		t.Fatal("Lookup() ok = true, want false for stale entry")
	}
}

func TestRegistry_Register_PrunesStaleEntries(t *testing.T) {
	clock := newFakeClock()
	r := newRegistryWithClock(time.Minute, clock)

	if err := r.Register("stale", "http://stale:8080", false); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
	if got := r.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}

	clock.Advance(2 * time.Minute)
	if err := r.Register("fresh", "http://fresh:8080", false); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}

	if got := r.Len(); got != 1 {
		t.Fatalf("Len() = %d after pruning, want 1 (only the fresh entry)", got)
	}
	if _, ok := r.Lookup("fresh"); !ok {
		t.Fatal("Lookup(\"fresh\") ok = false, want true")
	}
}
