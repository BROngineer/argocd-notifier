package registry

import (
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrMissingName    = errors.New("MissingName")
	ErrMissingBaseURL = errors.New("MissingBaseURL")
)

type Backend struct {
	Name                string
	BaseURL             string
	SupportsThreadReply bool
	LastSeen            time.Time
}

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

// Registry holds backends self-registered at runtime, keyed by name. There
// is no background sweep: staleness is checked lazily on Lookup, and
// Register opportunistically prunes other stale entries so the map doesn't
// grow unbounded from backends that stopped heartbeating.
type Registry struct {
	mu       sync.Mutex
	ttl      time.Duration
	clock    Clock
	backends map[string]Backend
}

func NewRegistry(ttl time.Duration) *Registry {
	return newRegistryWithClock(ttl, realClock{})
}

func newRegistryWithClock(ttl time.Duration, clock Clock) *Registry {
	return &Registry{
		ttl:      ttl,
		clock:    clock,
		backends: make(map[string]Backend),
	}
}

func (r *Registry) Register(name, baseURL string, supportsThreadReply bool) error {
	var errs []error
	if name == "" {
		errs = append(errs, ErrMissingName)
	}
	if baseURL == "" {
		errs = append(errs, ErrMissingBaseURL)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock.Now()
	r.backends[name] = Backend{
		Name:                name,
		BaseURL:             strings.TrimSuffix(baseURL, "/"),
		SupportsThreadReply: supportsThreadReply,
		LastSeen:            now,
	}
	r.pruneLocked(now, name)

	return nil
}

func (r *Registry) Lookup(name string) (Backend, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.backends[name]
	if !ok || r.clock.Now().Sub(b.LastSeen) > r.ttl {
		return Backend{}, false
	}
	return b, true
}

func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.backends)
}

// pruneLocked removes every entry (other than skip, just registered) that
// has gone stale relative to now. Callers must hold r.mu.
func (r *Registry) pruneLocked(now time.Time, skip string) {
	for name, b := range r.backends {
		if name == skip {
			continue
		}
		if now.Sub(b.LastSeen) > r.ttl {
			delete(r.backends, name)
		}
	}
}
