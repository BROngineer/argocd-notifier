# Remote backends: design

This is the design record for pluggable notification backends. You can plug in a backend when you deploy. You do not need to compile it in.

## Motivation

`docs/adding-a-backend.md` used to describe the only extension mechanism. To add a backend, you did these steps:

1. Implement `notification.Backend`, `Updater` and `ThreadReplier` in Go.
2. Add a `case` to `switch cfg.Backend` in `main.go`.
3. Recompile.

This mechanism worked, but it had these limits:

- Each new backend needed a change to the Go code and the binary of this repository.
- Backends could not use other languages.
- Backends could not be deployed or versioned independently.
- You could not add a backend without a pull request against this project.

The goal is a backend that is a **separate process, deployed independently, in any language**. The backend registers itself with argocd-notifier at runtime. It receives pushed notifications over HTTP.

The core keeps 100% of the debounce, session and deduplication logic. This part is the hard and valuable part. It is already independent of the backend. A remote backend only renders and delivers.

## Why REST, not gRPC

We considered gRPC because of **Server Reflection**. With Server Reflection, the core can query the service descriptors of a registering backend. Then the core can check that the backend really implements the expected RPC before it trusts the registration. This is a real and standard capability. REST has no equivalent.

We dropped gRPC because it does not give what it seems to give.

- Reflection checks only the *shape*: the right method and types exist. It does not check the *behavior*: it does not show that the handler works.
- This system must already accept a failed call to a backend as a normal outcome. A `Post` or `Update` call can fail for reasons that have nothing to do with conformance. Examples are a network error, a backend that restarts and a timeout.
- The existing handling is: log the error, do not store a ref, and let the next flush retry with a new `Post`. A backend that does not implement `/notify` correctly fails through this same path.
- Reflection would change only *when* we find out (at registration or at the first real push) and how clean the error is. It would not change what we do about the error.

With this one difference left, there is no reason to add a heavier toolchain (`protoc` or `buf`) than the rest of this project uses. The rest of the project uses hand-written `net/http` and `encoding/json`, with no framework.

So the decision is: **no check of any kind at registration time.** A backend that does not conform fails its first real push. The core logs it like any other failure.

## Protocol: two independent OpenAPI specs

One spec cannot describe two different servers cleanly. Every path in an OpenAPI document uses the same `servers` list. There is no first-class way to say that a path belongs to server A and another path belongs to server B.

If we used one file, `oapi-codegen` would make one combined server interface. Both sides would have to implement it. Each side would need throwaway stubs for the endpoints that belong to the other side.

For this reason there are two specs. They have no schema overlap. We checked this: `RegisterBackendRequest` does not reference `Notification`, `Item` or `Field`.

- **`api/core/openapi.yaml`** is served by the core of argocd-notifier. It defines every endpoint that the core exposes: `POST /v1/backends/register`, `POST /events`, `GET /healthz` and `GET /readyz`. See "Fully generated-server-driven" below. The name is `core`, not `registration`, on purpose. The plan always included moving the other endpoints into this spec. A name that follows the one feature that existed at the time would need a rename after the move.
- **`api/backendapi/openapi.yaml`** is served by each remote backend at the `baseURL` that it registered. It defines `POST /notify` and `POST /thread-reply`. It also defines the `Notification`, `Item` and `Field` schemas.

The generated Go bindings are in `api/core/` and `api/backendapi/`. They are not in `internal/`. Packages in `internal/` cannot be imported outside this module. This would block the Go backend authors that this protocol serves. To regenerate the bindings, run `mise run api-gen`.

The schema `NotifyResponse` collided with a type of `oapi-codegen`. This type is the client wrapper `<OperationId>Response` that `oapi-codegen` generates for the `notify` operation. For this reason, the schema is named `NotifyResult`. Remember this if a future operation has the same collision.

`Notification`, `Item` and `Field` are **separate generated wire types**. They are not the `internal/notification` domain types with JSON tags added. This follows an existing precedent. `internal/slack` already keeps its own wire types (`postMessageRequest` and `attachment`) separate from the domain model. It converts them at the boundary.

`internal/notification` stays independent of the transport. A conversion function at the boundary of the remote-backend adapter (Phase 4) does the translation.

## Registration and heartbeat

Send `POST /v1/backends/register` with `{name, baseURL, supportsThreadReply}`. To refresh the heartbeat, call it again with the same `name`. There is no separate heartbeat endpoint.

`baseURL` is a base address only. The core adds the fixed suffixes `/notify` and `/thread-reply` itself. If `baseURL` has a trailing slash, the core normalizes it. An OpenAPI spec works the same way: a `servers` entry plus fixed relative `paths`. We did not invent an extra convention.

**Deferred on purpose, not forgotten:** registration has no authentication. Any process that can reach the registration endpoint can claim any backend name. This is a real gap. You must close it before you use this feature for more than local development and testing. We deferred it by explicit decision. The reason is that the immediate goal was to decide if the overall design is worth building.

The heartbeat is not only a liveness record. It is the **recovery mechanism for a leader failover**. The registry is in memory. A new leader after a failover starts with no registered backends. This is consistent with the way sessions already work. The heartbeat lets backends find this and register again. The implementation in Phase 3 must treat it as essential, not as optional polish.

## The `/notify` contract: the backend decides whether to post or to update

One endpoint does both. The input is `{recipient, notification, ref}`. The output is `{ref}`.

- An empty `ref` means "post a new message".
- A `ref` that is not empty means "try to edit this message".

The backend decides what it can do. It returns the `ref` that represents the result. A backend that cannot edit anything always posts a new message and returns a new `ref` on every call. The behavior of the core is the same in all cases. It always sends the `ref` that it has now, or none. It always stores the `ref` that comes back.

For this reason, **the backend does not declare an "update capability" at registration.** An earlier draft of this design had it. We removed it. The backend decides for itself, so the core never branches on this decision. A declaration would not change anything that the core does. We found this by questioning the draft, not by assuming the declaration was necessary.

## Thread-reply: the one capability that needs a declaration

The choice between post and update is mechanical. It has no wrong answer. A thread reply is different. It is an optional *feature*. The operator config `DUPLICATE_ACTION=thread` controls it.

The existing in-process design fails fast at startup on purpose. `NewSessionPublisher` refused to start if the configured backend could not send a thread reply. We rejected a silent no-op on a misconfiguration, because it would be a regression. The field `supportsThreadReply` in the registration payload keeps this guarantee. It does not exist because the backend cannot decide for itself.

**Resolved (Phase 4a):** the old capability check was a Go type assertion. It ran once, at construction. This does not work when backends register and deregister during the lifetime of the process.

- `remotebackend.Backend.PostThreadReply` reads `registry.Lookup` again on every call.
- It returns `ErrThreadReplyNotSupported` if the registered entry says no.
- This changes "fail fast at startup" to "fail on each call", for the dynamic path only.
- The in-process type-assertion model does not change for compiled-in backends. The check still runs once, at startup, and not on each call.
- Phase 4b moves that check from `NewSessionPublisher` to the wiring in `main.go`. `NewSessionPublisher` no longer has one fixed backend to check.

This work showed a second, related gap. `notification.Updater.Update` cannot report a *changed* ref to the caller. But `/notify` explicitly allows a backend to return a different ref on what looks like an edit.

A new interface, `notification.Notifier`, covers this gap. It is `Notify(ctx, recipient, ref, n) (newRef string, err error)`. `remotebackend.Backend` implements it instead of `Backend` and `Updater`. `internal/slack` does not change. Its `ts` never changes on an edit, so it has no reason to implement the new interface.

## Routing: a replacement, not a second option

**Correction:** this section first said that the registry-based mechanism is additive to the compiled-in `BACKEND=slack` switch, and that both can coexist without a time limit. We never decided this with the project owner. This document stated an assumption as a settled fact without a check with the owner. The assumption was wrong.

The registry-based mechanism **is** a replacement. These items no longer exist:

- `cfg.Backend`.
- The `switch` in `main.go`.
- The fail-fast check at construction in `NewSessionPublisher`.

The change is `refactor: drop compiled-in backend selection from core`. Every backend, including Slack, is now a separate process that registers itself. The core has no in-process notification path.

The field `Backend` of `event.Event` is the only routing input. It is required and has no default (see Phase 2). `aggregator.BackendResolver` resolves it only through `remotebackend.Resolver`. This is a live registry lookup. No name has special handling.

The aggregator groups apps by `(backend, recipient)` (`groupAppsByBackendRecipient` in `internal/aggregator/session.go`). This extends the per-recipient routing fix that we already shipped. Different apps in one session can need different destinations. This was already true for the recipient alone before backend routing existed.

Each *routed event* goes to exactly **one** backend. This is not fan-out to many backends for each event. If you run several backends, different events go to different single destinations. The same notification is not broadcast to all of them.

## Fully generated-server-driven: /events, /healthz and /readyz are migrated too

At first, the core served `/events`, `/healthz` and `/readyz` with hand-written `net/http`. The generated server served `/v1/backends/register` beside them. The description of the spec called this future work.

This work is done. `api/core/openapi.yaml` now defines all four endpoints. The `server` type in `cmd/argocd-notifier` implements the resulting `core.ServerInterface`. It delegates to the same handlers as before: `receiver.Handler.ServeHTTP`, `httpx.HealthzHandler`, `httpx.ReadyzHandler` and `registry.Handler.RegisterBackend`. The internal logic of these handlers did not change. Only the way to reach them changed. `core.Handler(server)` replaced the `http.ServeMux` that `main.go` built by hand.

`event.Event` did **not** get a separate generated wire type, as `Notification` and `Item` did for `backendapi`. It was already designed as the wire contract with ArgoCD. Its own doc comment says this. A second struct and a conversion between the two would be pure duplication.

The `Event` schema in the spec documents the shape for anyone who reads it or generates a client. `SubmitEvent` decodes directly into `internal/event.Event`, as `receiver.Handler` always did.

One real consequence is deliberate: `EVENTS_PATH` is gone. It was an env var that you could set at runtime. OpenAPI paths are static strings that codegen fixes. When the spec routes `/events`, the path cannot stay configurable. It is now always `/events`. This was also the only value that anyone had ever configured.

## Deferred by explicit decision

- **Registration authentication.** It is deferred for now.
- **A Go SDK or plugin framework.** Example: `import argocd-notifier/backend/server` and implement one handler function. This is a legitimate and well-established pattern (see `go-plugin` from HashiCorp). We plan it *after* a real reference implementation proves the raw protocol (Phase 5). We judged it too early to build convenience tools on top of a protocol that is not yet proven.
- **Multi-backend fan-out for each event.** This was never in scope. One event goes to one backend.
