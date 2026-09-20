# Remote backends: design

Design record for making notification backends pluggable at deploy time, not just compile time.

## Motivation

`docs/adding-a-backend.md` used to describe the only extension mechanism that existed: implement `notification.Backend`/`Updater`/`ThreadReplier` in Go, add a `case` to `main.go`'s `switch cfg.Backend`, recompile. That worked, but it meant every new backend required touching this repo's Go code and this repo's binary — no polyglot backends, no independent deployment/versioning, no adding a backend without a PR against this project.

The goal: let a backend be a **separate, independently deployed process**, written in any language, that self-registers with argocd-notifier at runtime and receives pushed notifications over HTTP. The core keeps 100% of the debounce/session/dedup logic (that's the hard, valuable part, and it's already backend-agnostic); a remote backend only ever renders-and-delivers.

## Why REST, not gRPC

We considered gRPC specifically for **Server Reflection** — the ability for the core to query a registering backend's actual service descriptors and confirm it really implements the expected RPC before trusting the registration. That's a real, standardized capability REST has no equivalent for.

We dropped it because it doesn't buy what it sounds like it buys: reflection only verifies *shape* (the right method/types exist), never *behavior* (the handler actually works). And this system already has to treat "a call to a backend failed" as a normal, expected outcome — a `Post`/`Update` call can fail for reasons that have nothing to do with conformance (network blip, backend mid-restart, timeout), and the existing handling is: log it, don't store a ref, let the next flush retry with a fresh `Post`. A backend that doesn't actually implement `/notify` correctly fails through that exact same path. Reflection would only change *when* we find out (registration time vs. first real push) and *how clean* the error is — not what we do about it. Once that's the only differentiator, there's no reason to carry a heavier toolchain (`protoc`/`buf`) than everything else in this project uses (hand-rolled `net/http`/`encoding/json`, no framework). So: **no registration-time verification of any kind** — a non-conforming backend simply fails its first real push and is logged like any other failure.

## Protocol: two independent OpenAPI specs

One spec can't cleanly describe two different servers — every path in an OpenAPI document is implicitly served by the same `servers` list, and there's no first-class way to say "this path belongs to server A, that one to server B." Forcing it into one file would make `oapi-codegen` emit a single combined server interface both sides would have to implement, with throwaway stubs for the endpoints that aren't actually theirs. So there are two specs, with zero schema overlap between them (checked — `RegisterBackendRequest` doesn't reference `Notification`/`Item`/`Field` at all):

- **`api/core/openapi.yaml`** — served by argocd-notifier's core: `POST /v1/backends/register`, `POST /events`, `GET /healthz`, `GET /readyz` — every endpoint the core exposes (see "Fully generated-server-driven" below). Named `core`, not `registration`, deliberately, since that migration was always the plan — naming it after the single feature that existed at the time would just have meant renaming everything once the rest moved in.
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

## Routing: a replacement, not a second option

**Correction**: this section originally said the registry-based mechanism was additive to the compiled-in `BACKEND=slack` switch, letting both coexist indefinitely. That was never actually decided with the project owner — it was an assumption this document asserted as settled without confirming it, and it was wrong. It **is** a replacement: `cfg.Backend`, the `switch` in `main.go`, and the construction-time `NewSessionPublisher` fail-fast are gone (`refactor: drop compiled-in backend selection from core`). Every backend — including Slack — is now a separate process that self-registers; there is no in-process notification path left in the core.

`event.Event`'s `Backend` field (required, no default — see Phase 2) is the only routing input: `aggregator.BackendResolver` resolves it purely via `remotebackend.Resolver`, a live registry lookup, with no special-cased name. Apps are grouped by `(backend, recipient)` (`groupAppsByBackendRecipient` in `internal/aggregator/session.go`), extending the per-recipient routing fix already shipped — different apps in one session can legitimately need different destinations, and that was already true for recipient alone before backend routing existed.

Each *routed event* goes to exactly **one** backend — this is not fan-out to multiple backends per event. Running several backends means different events go to different single destinations, not the same notification broadcast everywhere.

## Fully generated-server-driven: /events, /healthz, /readyz migrated too

The core originally served `/events`, `/healthz`, and `/readyz` as hand-rolled `net/http`, alongside `/v1/backends/register` on a generated one — the spec's own description used to call this out as future work. It's done: `api/core/openapi.yaml` now defines all four, and `cmd/argocd-notifier`'s `server` type implements the resulting `core.ServerInterface` by delegating to the same handlers as before (`receiver.Handler.ServeHTTP`, `httpx.HealthzHandler`/`ReadyzHandler`, `registry.Handler.RegisterBackend`) — none of their internal logic changed, only how they're reached. `core.Handler(server)` replaced `main.go`'s manually-built `http.ServeMux` entirely.

`event.Event` did **not** get a separate generated wire type the way `Notification`/`Item` did for `backendapi` — it was already designed to be the wire contract with ArgoCD (its own doc comment says so), so minting a second struct and converting between them would be pure duplication. The `Event` schema in the spec documents the shape for anyone reading it (or generating a client), but `SubmitEvent` decodes straight into `internal/event.Event`, exactly as `receiver.Handler` always has.

One real, deliberate consequence: `EVENTS_PATH` (a runtime-configurable env var) is gone. OpenAPI paths are static strings baked in at codegen time, so once `/events` is spec-routed there's no way for that path to stay configurable — it's now a fixed `/events`, which was also the only value anyone had ever actually configured it to.

## Deferred by explicit decision

- **Registration authentication** — real gap, must land before production use, deferred for now.
- **A Go SDK/plugin framework** (`import argocd-notifier/backend/server`, implement one handler function) — a legitimate, well-established pattern (see HashiCorp's `go-plugin`), but sequenced *after* the raw protocol is proven via a real reference implementation (Phase 5), not before. Building convenience tooling on top of an unproven protocol was judged premature.
- **Multi-backend fan-out per event** — never in scope; one event, one backend.
