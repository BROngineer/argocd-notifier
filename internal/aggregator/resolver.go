package aggregator

// BackendResolver resolves the concrete backend to use for a routed
// event's declared Backend name — always dynamically, via whatever
// registered-backend lookup the caller wires in (see
// internal/remotebackend.Resolver). The resolved value may implement
// notification.Backend (+ optionally Updater/ThreadReplier) or
// notification.Notifier (+ optionally ThreadReplier) — Upsert type-asserts
// for whichever shape it got, since the two are mutually exclusive and
// share no common method.
type BackendResolver interface {
	Resolve(name string) (any, bool)
}
