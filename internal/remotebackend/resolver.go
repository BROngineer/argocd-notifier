package remotebackend

import (
	"net/http"

	"github.com/BROngineer/argocd-notifier/internal/registry"
)

// Resolver looks up a backend name against the registry and, if currently
// registered, hands back a Backend adapter for it. It reports not-found
// immediately for a name that was never registered (or has gone stale)
// rather than handing back an adapter that would only fail once called.
//
// Resolve returns `any`, not notification.Backend: a remote Backend only
// ever implements Notifier/ThreadReplier, never Backend.Post — the caller
// is expected to type-assert for whichever shape it actually got.
type Resolver struct {
	reg  *registry.Registry
	http *http.Client
}

func NewResolver(reg *registry.Registry, httpClient *http.Client) *Resolver {
	return &Resolver{reg: reg, http: httpClient}
}

func (r *Resolver) Resolve(name string) (any, bool) {
	if _, ok := r.reg.Lookup(name); !ok {
		return nil, false
	}
	return New(name, r.reg, r.http), true
}
