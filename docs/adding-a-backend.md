# Adding a notification backend

A notification backend is a **separate process. You can write it in any language.** The backend registers itself with the core of argocd-notifier over HTTP. Then it receives pushed notifications over HTTP. You do not implement a Go interface. You do not make a pull request to this repository.

For the design reasons, see `docs/remote-backends.md`. This document is the practical guide.

`cmd/slack-backend` is a full reference implementation. It uses `internal/slackbackend`. It wraps the rendering and client code of `internal/slack` behind the same contract. If you write a backend in Go, read it together with this document.

## 1. Register, and keep registering (heartbeat)

Send `POST {core's address}/v1/backends/register`. The specification is in `api/core/openapi.yaml`.

```json
{ "name": "my-backend", "baseURL": "http://my-backend.svc:8080", "supportsThreadReply": false }
```

- `name` is the name that events route to. The `backend` field of an event must match this name exactly. You set the field with the `application/backend` label of the Application (see `docs/setup.md`).
- `baseURL` is a base address only. The core adds `/notify` and `/thread-reply` itself. Do not add a trailing slash.
- To refresh your registration, call this endpoint again with the same `name`. There is no separate heartbeat endpoint.
- **You must call it again at regular intervals.** Use an interval that is much shorter than `BACKEND_REGISTRY_TTL` of the core (default 90s). The registry is in memory. After a leader failover, the new leader has no registered backends. When you register again, the core finds your backend again. One registration at startup is not enough.
- There is no authentication today. Any process that can reach the endpoint can claim any name. Do not expose the endpoint outside a trusted network. See the list of deferred items in `docs/remote-backends.md`.

## 2. Serve `/notify`

Serve `POST {your baseURL}/notify`. The specification is in `api/backendapi/openapi.yaml`.

```json
// in
{ "recipient": "...", "ref": "", "notification": { "summary": "...", "items": [ /* ... */ ] } }
// out
{ "ref": "..." }
```

**You decide whether to post or to update. The core does not decide.**

- An empty `ref` means that nothing exists yet. Post a new message.
- A `ref` that is not empty is the value that the core saw last. Edit the message if you can.
- In both cases, return the `ref` that now identifies the result. The core stores it unchanged. It sends it back on the next call for the same session and recipient.
- If you cannot edit anything, always post a new message and always return a new `ref`. This mode is fully supported. It is not a workaround.

Some fields in `notification.items[]` are required: `appName`, `cluster` and `trigger`. These fields are optional: `commitURL`, `commitSHA`, `triggeredBy`, `images`, `fields`, `detailText` and `link`. The fields are plain data on purpose. They have no markup and nothing that is specific to a platform. Render them into your own format, for example Slack Block Kit, a Discord embed or an email body.

## 3. Serve `/thread-reply` (optional)

Do this only if you set `supportsThreadReply: true` at registration. Serve `POST {your baseURL}/thread-reply`:

```json
{ "recipient": "...", "ref": "...", "text": "..." }
```

The core calls this endpoint only when both conditions are true:

- The operator set `DUPLICATE_ACTION=thread`.
- Your registration declares support at this time.

The core checks your latest registration on every call. If you change `supportsThreadReply` in a later registration, the change takes effect immediately. You do not restart the core.

## Testing

- Test the round trip of your `/notify` and `/thread-reply` handlers. Use the request and response shapes that `api/backendapi` generates.
  - In Go, the output of `oapi-codegen` in `api/backendapi/backendapi.gen.go` gives you the server types and the client types.
  - The tests of `internal/remotebackend` show the requests and responses that the core actually sends.
  - `internal/slackbackend/handler_test.go` is a worked example.
- Make sure that you can register twice with the same `name` without an error (heartbeat). Make sure that the second registration updates `baseURL` and `supportsThreadReply` if they changed. See `internal/slackbackend/registrar_test.go`.
- If you support thread replies, make sure that the core sends the request to you only when you are registered with `supportsThreadReply: true`.
