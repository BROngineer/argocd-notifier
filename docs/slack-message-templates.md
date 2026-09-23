# Custom Slack message templates

`cmd/slack-backend`'s default rendering (`internal/slack/render.go`) is fixed Go code: one colored Block Kit attachment per app, laid out the same way for every trigger. That's fine for a handful of apps, but a session aggregating many apps (a fleet-wide rollout) can produce an unreadably long message, and there's no way to change the look without a code change and a rebuild.

`slackBackend.messageTemplate` lets you supply your own template instead — a Go [`text/template`](https://pkg.go.dev/text/template), with [Sprig](https://masterminds.github.io/sprig/) functions available (the same style ArgoCD's own `argocd-notifications-cm` templates already use). It's optional: leave it unset and you get today's built-in rendering, unchanged.

## 1. The contract

Your template must define exactly two named blocks:

```
{{define "text"}}...{{end}}
{{define "attachments"}}...{{end}}
```

- `text` becomes the Slack message's top-level `text` (`chat.postMessage`/`chat.update`'s own text field — also what a push notification shows).
- `attachments` must render to a **raw JSON array** — Slack's legacy `attachments` field (an array of `{"color", "blocks"}` objects). It's validated (`json.Valid`) after rendering; invalid output is treated as a render failure (see [Failure handling](#4-failure-handling) below).

Both blocks are executed against the **same aggregated session**, not one app at a time — see the next section.

## 2. Available data

Both blocks execute against a `notification.Notification`:

```go
type Notification struct {
    Summary string
    Items   []Item
}
```

- **`.Summary`** is computed by argocd-notifier itself (`internal/notification/build.go`), not passed through from ArgoCD verbatim: `"<groupKey> — <N> deployed, <M> health-degraded, ..."`, tallying every trigger across every event in the session.
- **`.Items`** is one entry per app currently known in the session — a session routinely aggregates *multiple* Applications (that's the whole point of this project), so there is no single top-level "the app" — per-app data only exists inside `.Items`:

| Field            | Type       | Notes |
|------------------|------------|-------|
| `AppName`        | `string`   | |
| `Cluster`        | `string`   | From the `target` label you configured (e.g. `cd.tigerdata.com/target`). |
| `Trigger`        | `string`   | e.g. `on-deployed`, `on-health-degraded` — one of ArgoCD's 8 built-in triggers. |
| `CommitURL`      | `string`   | Link to the resolved commit; empty if `repoURL`/`revision` weren't available. |
| `CommitSHA`      | `string`   | The **resolved commit SHA** (truncated to 8 chars) — not the configured tag/branch. `"?"` if unavailable. |
| `TargetRevision` | `string`   | The Application's **configured** source revision (e.g. a git tag/branch like `v0.21.0`) — this is what you want if you want to show a human-readable version, not `CommitSHA`. |
| `TriggeredBy`    | `string`   | Who/what triggered the sync (`"auto-sync"` if automatic) — only populated for triggers where it's meaningful (`on-deployed`, `on-sync-running`). |
| `Images`         | `[]string` | |
| `Fields`         | `[]Field`  | `{Label, Value}` pairs specific to certain triggers (e.g. `on-sync-failed`'s `Phase`). |
| `DetailText`     | `string`   | Truncated free text, e.g. a sync error message. |
| `Link`           | `string`   | **The "Open in ArgoCD" URL for this app** — use this, not anything ArgoCD-side. |

`TargetRevision` and the others are populated for *every* trigger, not curated per-trigger the way the built-in renderer's fields are — your template decides what to show for each `.Trigger`.

## 3. It's a different template context than the ArgoCD-side one

**Don't reach for `.context` or `.app`** — those belong to the *ArgoCD-side* webhook body template (the one in your `argocd-notifications-cm`, evaluated by ArgoCD's notifications-engine against `.app`/`.context`/`.recipient`, see [setup.md](setup.md)). This is a completely different template, evaluated by argocd-notifier itself against the `Notification` above. Neither `.context` nor `.app` exist here — reaching for `.context.argocdUrl` / `.app.metadata.name` for a deep link will fail at execution; use `$item.Link` instead (already built from `.app.status.summary`'s `ArgoCDURL`/`AppName` on the ArgoCD side).

Also note: inside `{{range $i, $item := .Items}}`, `.` rebinds to the current `Item` — reference `$item.Field`, not `.Field`, and use `$.Summary` (not `.Summary`) if you need the *root* `Notification` from inside a `range`.

## 4. Failure handling

Two independent layers, both logged:

1. **A template that fails to parse, or is missing `text`/`attachments`**, is rejected on load/reload — the previously-loaded good template (or the built-in renderer, if none has ever loaded successfully) keeps being used. A typo in your template never takes production notifications down.
2. **A template that parses fine but fails to execute, or produces invalid JSON, for one specific notification** — controlled by `slackBackend.messageTemplate.fallbackToDefault` (default `false`): `false` logs the error and drops just that notification (matches the rest of this project's existing per-`(backend, recipient)` failure handling — a stale template bug loudly does nothing rather than silently rendering something you didn't intend); `true` falls back to the built-in renderer for that one message instead, so something still gets sent.

The template file itself is polled for changes (`slackBackend.messageTemplate.reloadInterval`, default `30s`) — editing a mounted `existingConfigMap` takes effect without restarting the pod once Kubernetes' own ConfigMap sync catches up.

## 5. Configuring it

```yaml
slackBackend:
  messageTemplate:
    # Rendered into a ConfigMap this chart creates. Use --set-file to load
    # a template from disk, e.g.:
    #   --set-file slackBackend.messageTemplate.content=./message.tmpl
    content: ""
    # Or point at a ConfigMap you manage yourself instead — set at most one
    # of content/existingConfigMap, mirroring slackBackend.slack.botToken/
    # existingSecret.
    existingConfigMap: ""
    existingConfigMapKey: "message.tmpl"
    reloadInterval: "30s"
    fallbackToDefault: false
```

See `slackBackend.messageTemplate.*` in [`chart/values.yaml`](../chart/values.yaml) for the exact defaults and two more worked examples inline.

## 6. Worked examples

**Simple** — same layout for every item:

```
{{define "text"}}{{.Summary}}{{end}}
{{define "attachments"}}
[
{{range $i, $item := .Items}}{{if $i}},{{end}}
  {"color":"#2eb1f2","blocks":[{"type":"section","fields":[
    {"type":"mrkdwn","text":"*App*\n{{$item.AppName}}"},
    {"type":"mrkdwn","text":"*Cluster*\n{{$item.Cluster}}"}
  ]}]}
{{end}}
]
{{end}}
```

**Different rendering per trigger, collapsing to a summary once there are many apps** — the template sees the whole `.Items` list at once, so it can decide this itself (the built-in renderer can't):

```
{{define "text"}}{{.Summary}}{{end}}
{{define "attachments"}}
{{if gt (len .Items) 8}}
[{"color":"#2eb1f2","blocks":[{"type":"section","text":{"type":"mrkdwn",
  "text":"{{len .Items}} apps: {{range $i, $item := .Items}}{{if $i}}, {{end}}{{$item.AppName}}{{end}}"}}]}]
{{else}}
[
{{range $i, $item := .Items}}{{if $i}},{{end}}
  {{if eq $item.Trigger "on-deployed"}}
    {"color":"#18be52","blocks":[{"type":"section","fields":[
      {"type":"mrkdwn","text":"*App*\n{{$item.AppName}}"},
      {"type":"mrkdwn","text":"*Commit*\n`{{$item.CommitSHA}}`"}
    ]}]}
  {{else if eq $item.Trigger "on-health-degraded"}}
    {"color":"#f4c030","blocks":[{"type":"section","fields":[
      {"type":"mrkdwn","text":"*App*\n{{$item.AppName}}"},
      {"type":"mrkdwn","text":"*Health*\n:large_yellow_circle: Degraded"}
    ]}]}
  {{end}}
{{end}}
]
{{end}}
{{end}}
```

**A compact one-liner per app, with an "Open in ArgoCD" button**:

```
{{define "text"}}:rocket: Application state update: {{.Summary}}{{end}}
{{define "attachments"}}
[
{{range $i, $item := .Items}}{{if $i}},{{end}}
  {"color":"#18be52","blocks":[
    {"type":"section","text":{"type":"mrkdwn","text":"Cluster: {{$item.Cluster}} | Revision: `{{$item.TargetRevision}}` | Triggered by: `{{$item.TriggeredBy}}`"}},
    {"type":"actions","elements":[
      {"type":"button","text":{"type":"plain_text","text":"Open in ArgoCD"},"url":"{{$item.Link}}"}
    ]}
  ]}
{{end}}
]
{{end}}
```

Each top-level block in a `blocks` array always stacks **vertically** — Slack has no "horizontal block". To get several values on one line, put them in one `text` string (as above), or in one `section`'s `fields` array (a compact 2-column grid — what the built-in renderer already does). For values whose length varies (e.g. cluster names), plain spaces won't visually align — Slack's `mrkdwn` renders in a proportional font by default. Pad with Go's built-in `printf` and wrap in backticks to force a monospace span:

```
`Cluster: {{printf "%-16s" $item.Cluster}}| Revision: {{$item.TargetRevision}}`
```

`%-16s` left-justifies and pads to 16 characters — pick a width comfortably wider than your longest expected value.
