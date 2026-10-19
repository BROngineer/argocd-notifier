# Custom Slack message templates

The default renderer of `cmd/slack-backend` (`internal/slack/render.go`) is Go code with a fixed layout. It makes one colored Block Kit attachment for each app. The layout is the same for every trigger. This is acceptable for a few apps. A session that aggregates many apps (a fleet-wide rollout) can make a message that is too long to read. To change the layout, you must change the code and rebuild.

`slackBackend.messageTemplate` lets you supply your own template. The template is a Go [`text/template`](https://pkg.go.dev/text/template). [Sprig](https://masterminds.github.io/sprig/) functions are available. The templates in the ArgoCD `argocd-notifications-cm` use the same style. The setting is optional. If you do not set it, the backend uses the built-in renderer, unchanged.

## 1. The contract

Your template must define exactly two named blocks:

```
{{define "text"}}...{{end}}
{{define "attachments"}}...{{end}}
```

- `text` becomes the top-level `text` of the Slack message. This is the `text` field of `chat.postMessage` and `chat.update`. A push notification also shows it.
- `attachments` must render to a **raw JSON array**. This is the legacy Slack `attachments` field: an array of `{"color", "blocks"}` objects. The backend validates the result with `json.Valid` after rendering. Invalid output is a render failure (see [Failure handling](#4-failure-handling)).

Both blocks run against the **same aggregated session**. They do not run for one app at a time. The next section explains this.

## 2. Available data

Both blocks run against a `notification.Notification`:

```go
type Notification struct {
    Summary string
    Items   []Item
}
```

- **`.Summary`**: argocd-notifier builds it (`internal/notification/build.go`). ArgoCD does not supply it. The format is `"<groupKey> — <N> deployed, <M> health-degraded, ..."`. It counts every trigger of every event in the session.
- **`.Items`** has one entry for each app that the session currently knows. A session usually aggregates *more than one* Application. This is the purpose of this project. For this reason, there is no single top-level "app". Data for each app exists only in `.Items`.

| Field            | Type       | Notes |
|------------------|------------|-------|
| `AppName`        | `string`   | |
| `Cluster`        | `string`   | The value of the `target` label that you configured (for example `application/target`). |
| `Trigger`        | `string`   | For example `on-deployed` or `on-health-degraded`. The value is one of the 8 built-in ArgoCD triggers. |
| `CommitURL`      | `string`   | The link to the resolved commit. Empty if `repoURL` or `revision` was not available. |
| `CommitSHA`      | `string`   | The **resolved commit SHA**, truncated to 8 characters. It is not the configured tag or branch. The value is `"?"` if the SHA is not available. |
| `TargetRevision` | `string`   | The **configured** source revision of the Application, for example a git tag or branch such as `v0.21.0`. Use this field to show a version that a person can read. Do not use `CommitSHA` for this. |
| `TriggeredBy`    | `string`   | The person or process that started the sync (`"auto-sync"` for an automatic sync). The backend fills this field only for triggers where it has a meaning: `on-deployed` and `on-sync-running`. |
| `Images`         | `[]string` | |
| `Fields`         | `[]Field`  | `{Label, Value}` pairs for some triggers only, for example `Phase` for `on-sync-failed`. |
| `DetailText`     | `string`   | Truncated free text, for example a sync error message. |
| `Link`           | `string`   | **The "Open in ArgoCD" URL for this app.** Use this field. Do not use anything from the ArgoCD side. |

The backend fills `TargetRevision` and the other fields for *every* trigger. The built-in renderer selects fields for each trigger, but your template does not get this selection. Your template decides what to show for each `.Trigger`.

## 3. It's a different template context than the ArgoCD-side one

**Do not use `.context` or `.app`.** They belong to the *ArgoCD-side* webhook body template. This template is in your `argocd-notifications-cm`. The ArgoCD notifications-engine runs it against `.app`, `.context` and `.recipient` (see [setup.md](setup.md)).

The template in this document is a different template. argocd-notifier runs it against the `Notification` above. `.context` and `.app` do not exist in it. If you use `.context.argocdUrl` or `.app.metadata.name` to build a deep link, the template fails at execution. Use `$item.Link` instead. The ArgoCD side already builds it from `ArgoCDURL` and `AppName` of `.app.status.summary`.

Inside `{{range $i, $item := .Items}}`, the `.` points to the current `Item`. Use `$item.Field`. Do not use `.Field`. To get the *root* `Notification` inside a `range`, use `$.Summary`. Do not use `.Summary`.

## 4. Failure handling

There are two independent layers. The backend logs both.

1. **A template does not parse, or it has no `text` or `attachments` block.** The backend rejects the template when it loads or reloads it. It keeps the last good template. If no template ever loaded successfully, it keeps the built-in renderer. A typo in your template does not stop production notifications.
2. **A template parses but fails to execute, or it makes invalid JSON, for one specific notification.** The setting `slackBackend.messageTemplate.fallbackToDefault` (default `false`) controls this case.
   - `false`: The backend logs the error and drops only this notification. This matches the existing handling of failures for each `(backend, recipient)` pair in this project. A template bug that is out of date does nothing, and the log shows it. It does not silently render something that you did not intend.
   - `true`: The backend uses the built-in renderer for this one message. The backend still sends a message.

The backend polls the template file for changes. `slackBackend.messageTemplate.reloadInterval` sets the interval (default `30s`). If you edit a mounted `existingConfigMap`, the change takes effect without a pod restart. The change takes effect after the ConfigMap sync of Kubernetes catches up.

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

See `slackBackend.messageTemplate.*` in [`chart/values.yaml`](../chart/values.yaml) for the exact defaults and two more worked examples.

## 6. Worked examples

**Simple.** The layout is the same for every item:

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

**A different rendering for each trigger.** The template shows a summary when there are many apps. The template sees the whole `.Items` list at once, so it can decide this itself. The built-in renderer cannot do this:

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

**A compact line for each app, with an "Open in ArgoCD" button:**

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

Each top-level block in a `blocks` array always stacks **vertically**. Slack has no "horizontal block". To show several values on one line, use one of these methods:

- Put the values in one `text` string, as in the example above.
- Put the values in the `fields` array of one `section`. This makes a compact grid with 2 columns. The built-in renderer already does this.

Values can have different lengths, for example cluster names. Plain spaces do not align them, because Slack `mrkdwn` uses a proportional font by default. To align the values, pad them with the Go built-in `printf`. Put the result in backticks to force a monospace span:

```
`Cluster: {{printf "%-16s" $item.Cluster}}| Revision: {{$item.TargetRevision}}`
```

`%-16s` left-justifies the value and pads it to 16 characters. Choose a width that is clearly wider than your longest expected value.
