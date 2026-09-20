# Adding a notification backend

A notification backend is a **separate process, in any language**. It self-registers with argocd-notifier's core over HTTP, then receives pushed notifications over HTTP. There is no Go interface to implement and no PR against this repo required — see `docs/remote-backends.md` for the full design rationale. This doc is the practical how-to; `cmd/slack-backend` (backed by `internal/slackbackend`) is a full reference implementation wrapping `internal/slack`'s rendering/client code behind this same contract — read it alongside this doc if you're writing a Go backend.

## 1. Register, and keep registering (heartbeat)

`POST {core's address}/v1/backends/register` (spec: `api/core/openapi.yaml`):

```json
{ "name": "my-backend", "baseURL": "http://my-backend.svc:8080", "supportsThreadReply": false }
```

- `name` is what events route to — an event's `backend` field (set via the `application/backend` Application label, see `docs/setup.md`) must match this exactly.
- `baseURL` is a base address only — the core appends `/notify` and `/thread-reply` itself. No trailing slash.
- Call this again with the same `name` to refresh your registration — there's no separate heartbeat endpoint. **You must keep doing this periodically** (well under the core's `BACKEND_REGISTRY_TTL`, default 90s): the registry is in-memory, so a leader failover starts with zero registered backends, and re-registering is how you're noticed again. A one-time registration at startup is not enough.
- No authentication today — anything that can reach the endpoint can claim any name. Don't expose this beyond a trusted network yet (see `docs/remote-backends.md`'s deferred-items list).

## 2. Serve `/notify`

`POST {your baseURL}/notify` (spec: `api/backendapi/openapi.yaml`):

```json
// in
{ "recipient": "...", "ref": "", "notification": { "summary": "...", "items": [ /* ... */ ] } }
// out
{ "ref": "..." }
```

**You decide post-vs-update, not the core.** An empty `ref` means "nothing exists yet — post fresh." A non-empty `ref` means "the core last saw this value — try to edit if you can." Either way, return whatever `ref` now identifies the result; the core stores it verbatim and sends it back on the next call for that same session/recipient. If you can't edit anything, always post fresh and always return a new `ref` — that's a fully supported mode, not a workaround.

`notification.items[]` fields (`appName`, `cluster`, `trigger` required; `commitURL`/`commitSHA`/`triggeredBy`/`images`/`fields`/`detailText`/`link` optional) are deliberately plain data — no markup, nothing platform-specific. Render them into your own format (Slack's Block Kit, a Discord embed, an email body, whatever).

## 3. Serve `/thread-reply` (optional)

Only if you set `supportsThreadReply: true` at registration. `POST {your baseURL}/thread-reply`:

```json
{ "recipient": "...", "ref": "...", "text": "..." }
```

The core only calls this when the operator has `DUPLICATE_ACTION=thread` configured **and** your registration currently declares support — it re-checks your latest registration on every call, so toggling `supportsThreadReply` on a later re-registration takes effect immediately, without restarting the core.

## Testing

- Round-trip your `/notify` and `/thread-reply` handlers against `api/backendapi`'s generated request/response shapes (if you're writing Go, `oapi-codegen`'s output in `api/backendapi/backendapi.gen.go` gives you both server and client types for this — see `internal/remotebackend`'s tests for the request/response shapes the core actually sends, and `internal/slackbackend/handler_test.go` for a worked example).
- Verify registering twice with the same `name` doesn't error (heartbeat) and updates `baseURL`/`supportsThreadReply` if they changed — see `internal/slackbackend/registrar_test.go`.
- If you support thread replies, verify the core's request reaches you only when you're currently registered with `supportsThreadReply: true`.
