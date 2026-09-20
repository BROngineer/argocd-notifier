# Remote backends: design

Design record for making notification backends pluggable at deploy time, not just compile time.

## Motivation

`docs/adding-a-backend.md` describes today's extension mechanism: implement `notification.Backend`/`Updater`/`ThreadReplier` in Go, add a `case` to `main.go`'s `switch cfg.Backend`, recompile. That works, but it means every new backend requires touching this repo's Go code and this repo's binary — no polyglot backends, no independent deployment/versioning, no adding a backend without a PR against this project.

The goal: let a backend be a **separate, independently deployed process**, written in any language, that self-registers with argocd-notifier at runtime and receives pushed notifications over HTTP. The core keeps 100% of the debounce/session/dedup logic (that's the hard, valuable part, and it's already backend-agnostic); a remote backend only ever renders-and-delivers.

## Why REST, not gRPC

We considered gRPC specifically for **Server Reflection** — the ability for the core to query a registering backend's actual service descriptors and confirm it really implements the expected RPC before trusting the registration. That's a real, standardized capability REST has no equivalent for.

We dropped it because it doesn't buy what it sounds like it buys: reflection only verifies *shape* (the right method/types exist), never *behavior* (the handler actually works). And this system already has to treat "a call to a backend failed" as a normal, expected outcome — a `Post`/`Update` call can fail for reasons that have nothing to do with conformance (network blip, backend mid-restart, timeout), and the existing handling is: log it, don't store a ref, let the next flush retry with a fresh `Post`. A backend that doesn't actually implement `/notify` correctly fails through that exact same path. Reflection would only change *when* we find out (registration time vs. first real push) and *how clean* the error is — not what we do about it. Once that's the only differentiator, there's no reason to carry a heavier toolchain (`protoc`/`buf`) than everything else in this project uses (hand-rolled `net/http`/`encoding/json`, no framework). So: **no registration-time verification of any kind** — a non-conforming backend simply fails its first real push and is logged like any other failure.

## Protocol: two independent OpenAPI specs

One spec can't cleanly describe two different servers — every path in an OpenAPI document is implicitly served by the same `servers` list, and there's no first-class way to say "this path belongs to server A, that one to server B." Forcing it into one file would make `oapi-codegen` emit a single combined server interface both sides would have to implement, with throwaway stubs for the endpoints that aren't actually theirs. So there are two specs, with zero schema overlap between them (checked — `RegisterBackendRequest` doesn't reference `Notification`/`Item`/`Field` at all):

- **`api/core/openapi.yaml`** — served by argocd-notifier's core. Today: just `POST /v1/backends/register`. Named `core`, not `registration`, deliberately — the core already serves `/events`/`/healthz`/`/readyz` as hand-rolled `net/http`, and those are expected to migrate into this same spec in a later phase once the core's HTTP server is refactored to be fully generated-server-driven. Naming it after the current single feature would just mean renaming everything once that migration happens.
- **`api/backendapi/openapi.yaml`** — served by each remote backend, at whatever `baseURL` it registered. `POST /notify` and `POST /thread-reply`, plus the `Notification`/`Item`/`Field` schemas.

Generated Go bindings live in `api/core/` and `api/backendapi/` (not under `internal/` — `internal/` packages can't be imported outside this module, which would block the Go backend authors this protocol is meant to serve). Regenerate via `mise run api-gen`. Note: `NotifyResponse` collided with `oapi-codegen`'s auto-generated `<OperationId>Response` client wrapper type for the `notify` operation — the schema is named `NotifyResult` to avoid it. Worth remembering if a future operation hits the same collision.

`Notification`/`Item`/`Field` are **separate generated wire types**, not the `internal/notification` domain types with JSON tags bolted on. This matches existing precedent — `internal/slack` already keeps its own wire types (`postMessageRequest`, `attachment`) distinct from the domain model, converting at the boundary. `internal/notification` stays transport-agnostic; a conversion function at the remote-backend-adapter boundary (Phase 4) does the translation.

## Registration and heartbeat

`POST /v1/backends/register` with `{name, baseURL, supportsThreadReply}`. Calling it again with the same `name` refreshes the heartbeat — there is no separate heartbeat endpoint. `baseURL` is a base address only; the core appends the fixed suffixes `/notify` and `/thread-reply` itself (normalizing a trailing slash if present), the same way an OpenAPI spec's `servers` entry plus fixed relative `paths` already works — not an extra convention invented on top.

**Explicitly deferred, not forgotten**: registration has no authentication. Any process that can reach the registration endpoint can currently claim any backend name. This is a real gap that must be closed before this is used for anything beyond local development/testing — deferred by explicit decision, not an oversight, because the immediate goal was deciding whether the overall design is worth building at all.

Heartbeat isn't just liveness bookkeeping — it's the **recovery mechanism for leader failover**. The registry is in-memory, and (consistent with how sessions already work) a new leader after failover starts with zero registered backends. Heartbeat is what lets backends notice and re-register; it needs to be treated as load-bearing in Phase 3's implementation, not optional polish.

## The `/notify` contract: backend decides post-vs-update

One endpoint handles both: `{recipient, notification, ref}` in, `{ref}` out. Empty `ref` means "post new"; non-empty means "try to edit this message." The backend decides internally what it's capable of and returns whatever `ref` represents the result — a backend that can't edit anything just always posts fresh and returns a new `ref` every call. The core's behavior on both sides is identical either way: it always sends whatever `ref` it currently has (or none), and always stores whatever comes back.

This means **no "update capability" needs declaring at registration**. It was in an earlier draft of this design and got removed — if the backend decides for itself, the core never branches on that decision, so declaring it doesn't change anything the core does. Caught by questioning the draft rather than assuming it was needed.

## Thread-reply: the one capability that *does* need declaring

Post-vs-update is a mechanical choice with no wrong answer. Thread-reply is an optional *feature* gated by operator config (`DUPLICATE_ACTION=thread`), and the existing in-process design deliberately fails fast at startup (`NewSessionPublisher` refuses to start) if the configured backend can't do it — silently no-op'ing on a misconfiguration was rejected as a regression. `supportsThreadReply` in the registration payload exists to preserve that guarantee, not because the backend can't decide for itself.

**Resolved (Phase 4a)**: the old capability check was a Go type-assertion, done once, at construction — that doesn't work once backends register and deregister throughout the process's lifetime. `remotebackend.Backend.PostThreadReply` re-reads `registry.Lookup` on every call and returns `ErrThreadReplyNotSupported` if the currently-registered entry says no, turning "fail fast at startup" into "fail per-call" for the dynamic path only. The in-process type-assertion model is unchanged for compiled-in backends — still checked once, at startup, not per-call; Phase 4b relocates that check from `NewSessionPublisher` into `main.go`'s wiring, since `NewSessionPublisher` itself no longer has a single fixed backend to validate.

Resolving this exposed a second, related gap: `notification.Updater.Update` has no way to report a *changed* ref back to the caller, but `/notify` explicitly allows a backend to return a different ref on what looks like an edit. A new `notification.Notifier` interface (`Notify(ctx, recipient, ref, n) (newRef string, err error)`) covers this — `remotebackend.Backend` implements it instead of `Backend`/`Updater`; `internal/slack` is untouched, since its `ts` never changes on edit and it has no reason to implement the new interface.

## Routing: additive, not a replacement

`event.Event` gains a `Backend` field, sourced from an Application label with a default (mirrors how `groupKey`/`target` already work) — see `docs/adding-a-backend.md` for the label-based pattern this follows. This is **additive** to the existing in-process `BACKEND=slack` switch in `main.go`, not a replacement: a simple single-backend deployment is unaffected and doesn't need to run anything extra. The `Backend` field lets a *single running instance* route different events to different backends — either a compiled-in one (existing mechanism) or a dynamically-registered remote one (new).

This directly extends the per-recipient routing fix already shipped (`groupAppsByRecipient` in `internal/aggregator/session.go`): apps get grouped by `(backend, recipient)` instead of just `recipient`, for the same reason — different apps in one session can legitimately need different destinations, and that was already true for recipient alone before backend routing existed.

Each *routed event* goes to exactly **one** backend — this is not fan-out to multiple backends per event. Running several backends means different events go to different single destinations, not the same notification broadcast everywhere.

## Deferred by explicit decision

- **Registration authentication** — real gap, must land before production use, deferred for now.
- **A Go SDK/plugin framework** (`import argocd-notifier/backend/server`, implement one handler function) — a legitimate, well-established pattern (see HashiCorp's `go-plugin`), but sequenced *after* the raw protocol is proven via a real reference implementation (Phase 5), not before. Building convenience tooling on top of an unproven protocol was judged premature.
- **gRPC** — rejected; see above.
- **Multi-backend fan-out per event** — never in scope; one event, one backend.

## Phased implementation plan

1. ✅ **Protocol definition** — the two OpenAPI specs, `oapi-codegen` tooling (`mise run api-gen`), generated bindings. Done (`feat/backend-api`).
2. ✅ **Event routing groundwork** — `event.Event.Backend`, required (no default — an event with no backend fails validation rather than silently routing to `slack`), sourced from the `application/backend` Application label with no template-side fallback. Done (`feat/event-backend-field`, #20).
3. ✅ **Core-side registry + registration server** — `internal/registry.Registry`: in-memory map keyed by backend name, `Register` (also serves as the heartbeat — no separate endpoint) with no verification step per the decision above, `Lookup` treats a stale entry (no heartbeat within `BACKEND_REGISTRY_TTL`, default 90s) as not-found, and `Register` opportunistically prunes other stale entries so the map stays bounded without a background sweep. `registry.Handler` implements `core.ServerInterface` and is mounted onto the main server's mux via `core.HandlerFromMux`, so registration shares the same port, middleware, and leader-gated readiness as `/events` — only the current leader ever receives it. Done (`feat/backend-registry`).
4. **Remote backend adapter + multi-backend routing**, split in two:
   - 4a. ✅ **`notification.Notifier` + `internal/remotebackend`** — the HTTP adapter wrapping a registry entry (`Notify`/`PostThreadReply`, both re-reading the registry on every call — see "Resolved (Phase 4a)" above) and a `Resolver` that turns a backend name into one. Unit-tested standalone, not yet wired to anything. Done (`feat/remote-backend-adapter`).
   - 4b. **`aggregator`/`main.go` wiring** — a `BackendResolver` in `aggregator` composing the one compiled-in backend with the dynamic `remotebackend.Resolver` as fallback; extend recipient-grouping to `(backend, recipient)`; move the thread-reply startup check out of `NewSessionPublisher` into `aggregator.ValidateStaticBackend`, called from `main.go` against the compiled-in backend only.
5. **Reference implementation: Slack as a standalone backend** — a new binary wrapping the existing `internal/slack` rendering/client code behind the `backendapi` server interface, self-registering and heartbeating. Proves the design end-to-end. The existing in-process `BACKEND=slack` path stays as-is; this is a second, additive deployment option.
6. **Chart + docs** — deployment support for running a remote backend as its own workload; rewrite `docs/adding-a-backend.md` to cover both extension mechanisms.
