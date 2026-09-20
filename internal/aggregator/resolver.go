package aggregator

// BackendResolver resolves the concrete backend to use for a routed
// event's declared Backend name. The resolved value may implement
// notification.Backend (+ optionally Updater/ThreadReplier) or
// notification.Notifier (+ optionally ThreadReplier) — Upsert type-asserts
// for whichever shape it got, since the two are mutually exclusive and
// share no common method.
type BackendResolver interface {
	Resolve(name string) (any, bool)
}

// staticResolver checks the one compiled-in backend by name first — this
// keeps a single-backend deployment's behavior identical to today: an
// event whose Backend matches the compiled one never touches fallback at
// all. Any other name is delegated to fallback (typically a dynamic,
// registry-backed resolver for remote backends).
//
// backend is `any`, not notification.Backend: the compiled-in backend can
// itself be a Notifier-only implementation, same as a remote one — nothing
// here assumes it has a Post method.
type staticResolver struct {
	name     string
	backend  any
	fallback BackendResolver
}

func NewStaticResolver(name string, backend any, fallback BackendResolver) BackendResolver {
	return &staticResolver{name: name, backend: backend, fallback: fallback}
}

func (r *staticResolver) Resolve(name string) (any, bool) {
	if name == r.name {
		return r.backend, true
	}
	if r.fallback == nil {
		return nil, false
	}
	return r.fallback.Resolve(name)
}
