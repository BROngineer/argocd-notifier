# Adding a notification backend

argocd-notifier ships with one backend, Slack (`internal/slack`). This walks through what a second one needs, using Slack as the reference implementation.

## The interfaces (`internal/notification`)

```go
type Backend interface {
    Post(ctx context.Context, recipient string, n Notification) (ref string, err error)
}

type Updater interface {
    Update(ctx context.Context, recipient, ref string, n Notification) error
}

type ThreadReplier interface {
    PostThreadReply(ctx context.Context, recipient, ref, text string) error
}
```

**Only `Backend.Post` is required.** That's the one thing every notification target can do — send something new. `Updater` and `ThreadReplier` are optional, checked via type assertion (the same pattern as `io.Writer`/`io.Closer`) — implement them only if your backend actually supports editing a previous message or replying in a thread under one. There's no `SupportsX() bool` method to keep in sync; implementing the method *is* the capability signal.

- **Implement `Updater`** if your backend can edit a message it already sent, given the `ref` you returned from `Post` (Slack's message `ts`, Discord/Telegram's message ID, etc.). If you don't, `SessionPublisher` never tracks a ref for your backend — every flush becomes a fresh `Post` instead of an in-place edit. That's a legitimate, supported mode, not a workaround: it's exactly right for backends where editing isn't possible at all (generic webhooks, email, PagerDuty-style incident creation).
- **Implement `ThreadReplier`** if your backend supports replying under an existing message. This is only used when `DUPLICATE_ACTION=thread` is configured. If you don't implement it, that's fine too — but `NewSessionPublisher` will refuse to start if the operator configures `thread` against your backend (fail fast, not a silent no-op at runtime).

## `Notification` and `Item`

```go
type Notification struct {
    Summary string
    Items   []Item
}

type Item struct {
    AppName, Cluster, Trigger string
    CommitURL, CommitSHA      string
    TriggeredBy               string
    Images                    []string
    Fields                    []Field // ordered label/value pairs for anything not above
    DetailText                string  // e.g. a truncated sync-failure message
    Link                      string  // "Open in ArgoCD" URL
}
```

This is deliberately plain data — no Slack markup, no Block Kit, nothing backend-specific. `CommitURL`/`CommitSHA` are separate fields rather than one pre-formatted "Commit" string precisely so each backend can render its own hyperlink syntax (Slack's `<url|text>`, Markdown's `[text](url)`, an HTML anchor, or just two plain fields) instead of inheriting Slack's.

`notification.Build(perApp map[string]event.Event) (Notification, error)` — the aggregation logic (grouping, sorting, per-trigger field selection) — is shared by every backend; you don't reimplement it.

## What a new backend needs to write

Using `internal/slack` as the template:

1. A renderer: `Item` → your wire format. This is where your backend's specific limits and markup live — Slack's is `internal/slack/render.go` (colors per trigger, Block Kit field construction, the 100-attachment chunking cap with a "+N more" marker).
2. A client: your backend's actual API/transport, implementing `Post` (and `Update`/`PostThreadReply` if applicable) — see `internal/slack/client.go` for retry/backoff conventions (bounded retries on 429/5xx, terminal on other API errors).
3. A `case` in `cmd/argocd-notifier/main.go`'s `switch cfg.Backend`, alongside `"slack"` — this is the one place that knows which backend names exist, selected via the `BACKEND` env var (`internal/config`'s `Backend` field, default `"slack"`). Each *running instance* still only ever talks to one backend picked at startup; there's no fan-out to multiple backends from a single process.
4. Any backend-specific required config (e.g. Slack's bot token) belongs in `Config.Validate()` guarded by `if c.Backend == "<yours>"`, the same way `SlackBotToken` is only required `if c.Backend == "slack"` — not an unconditional `envconfig` `required:"true"` tag, which would demand your backend's config even when a *different* backend is selected. The chart (`chart/templates/deployment.yaml`) follows the same pattern for its fail-fast checks and env wiring.

## Testing

Add a compile-time assertion your backend satisfies the interfaces it claims to (see `internal/slack/client.go`):

```go
var (
    _ notification.Backend       = (*Client)(nil)
    _ notification.Updater       = (*Client)(nil)
    _ notification.ThreadReplier = (*Client)(nil)
)
```

For `SessionPublisher` behavior with a backend that *doesn't* implement `Updater` (always-post-fresh) or `ThreadReplier` (fail-fast on `thread`), see `TestSessionPublisher_PostOnlyBackendAlwaysPostsFresh` and `TestNewSessionPublisher_ThreadActionRequiresThreadReplier` in `internal/aggregator/session_test.go` — those exercise the graceful-degradation and fail-fast paths this doc describes, against a fake backend with only `Post` implemented.
