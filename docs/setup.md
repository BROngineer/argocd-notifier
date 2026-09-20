# Setup

How to wire ArgoCD's notifications-engine to argocd-notifier and have it aggregate real notifications. See [design.md](design.md) for how the service behaves once wired up.

There are three pieces: deploy argocd-notifier, add it as a webhook notification service in ArgoCD, then subscribe your Applications to it.

## 1. Deploy argocd-notifier

Using the chart at [`chart/`](../chart):

```sh
helm install argocd-notifier ./chart \
  --namespace argocd \
  --set aggregation.groupLabel=application/name  # must match a real label on your Applications — see step 3
```

See [`chart/values.yaml`](../chart/values.yaml) for every setting. Multi-replica setups need `--set leaderElection.enabled=true` — the chart renders the RBAC `Role`/`RoleBinding` this requires automatically; see [design.md#high-availability](design.md#high-availability) before doing that (skip it for a single replica).

This deploys the core only — the part that receives, debounces, and routes events. It doesn't talk to Slack or anything else directly: notifications only actually go anywhere once at least one backend process self-registers against it. See [`adding-a-backend.md`](adding-a-backend.md) for the registration contract, and [`remote-backends.md`](remote-backends.md) for the design behind it.

For Slack specifically, this same chart also deploys the reference [`cmd/slack-backend`](../cmd/slack-backend) as a second workload in the same release — enable it and it self-registers against the core Service this release already creates, no manual URL wiring needed:

```sh
helm install argocd-notifier ./chart \
  --namespace argocd \
  --set aggregation.groupLabel=application/name \
  --set slackBackend.enabled=true \
  --set slackBackend.slack.botToken=xoxb-your-bot-token
```

Prefer an existing Secret you manage yourself (sealed-secrets, external-secrets) over `slackBackend.slack.botToken` in production: `--set slackBackend.slack.existingSecret=my-slack-secret`. The Slack bot token needs the `chat:write` scope, and the bot must be invited to every channel you intend to notify (`/invite @your-bot`) or `chat.postMessage`/`chat.update` will fail with `not_in_channel`. See `slackBackend.*` in [`chart/values.yaml`](../chart/values.yaml) for every setting, including `coreURL`/`publicBaseURL` overrides for pointing this backend at a core deployed outside this release.

To profile a running instance, set `--set pprof.enabled=true` (its own container port, deliberately not exposed via the Service), then:

```sh
kubectl port-forward -n argocd deploy/argocd-notifier 6060:6060
go tool pprof http://localhost:6060/debug/pprof/heap
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

## 2. Add argocd-notifier as a webhook service in ArgoCD

Edit `argocd-notifications-cm` (the ConfigMap ArgoCD's notifications-engine reads):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-notifications-cm
  namespace: argocd
data:
  context: |
    argocdUrl: https://argocd.example.com

  service.webhook.argocd-notifier: |
    url: http://argocd-notifier.argocd.svc.cluster.local:8080
```

The `url` is the base address; argocd-notifier's HTTP path is fixed at `/events` (see `api/core/openapi.yaml`) and set per-template below.

Now add (or edit) the templates for whichever triggers you want aggregated. If you already have ArgoCD's default notification catalog loaded, these template *names* likely already exist (feeding the default `slack:` key) — you're adding a `webhook:` key to the same template, not replacing the trigger definitions. Three representative examples (the same fields argocd-notifier's `render` package already knows how to draw for every one of ArgoCD's 8 built-in triggers — `on-created`, `on-deleted`, `on-deployed`, `on-health-degraded`, `on-sync-failed`, `on-sync-running`, `on-sync-status-unknown`, `on-sync-succeeded`):

```yaml
  template.app-deployed: |
    webhook:
      argocd-notifier:
        method: POST
        path: /events
        body: |
          {{$rev := .app.status.sync.revision}}{{if not $rev}}{{$rev = index .app.status.sync.revisions 1}}{{end}}{{$src := .app.spec.source}}{{if not $src}}{{$src = index .app.spec.sources 1}}{{end}}
          {
            "groupKey": {{index .app.metadata.labels "application/name" | default .app.metadata.name | toJson}},
            "labels": {{.app.metadata.labels | toJson}},
            "appName": {{.app.metadata.name | toJson}},
            "target": {{index .app.metadata.labels "application/target" | default "" | toJson}},
            "backend": {{index .app.metadata.labels "application/backend" | toJson}},
            "trigger": "on-deployed",
            "healthStatus": {{.app.status.health.status | toJson}},
            "syncPhase": {{.app.status.operationState.phase | toJson}},
            "syncStatus": {{.app.status.sync.status | toJson}},
            "operationMessage": {{.app.status.operationState.message | default "" | trunc 500 | toJson}},
            "revision": {{$rev | toJson}},
            "repoURL": {{$src.repoURL | toJson}},
            "images": {{.app.status.summary.images | toJson}},
            "initiatedBy": {{if .app.status.operationState.operation.initiatedBy.username}}{{.app.status.operationState.operation.initiatedBy.username | toJson}}{{else}}"auto-sync"{{end}},
            "argocdUrl": {{.context.argocdUrl | toJson}},
            "recipient": {{.recipient | toJson}}
          }

  template.app-sync-failed: |
    webhook:
      argocd-notifier:
        method: POST
        path: /events
        body: |
          {{$rev := .app.status.sync.revision}}{{if not $rev}}{{$rev = index .app.status.sync.revisions 1}}{{end}}{{$src := .app.spec.source}}{{if not $src}}{{$src = index .app.spec.sources 1}}{{end}}
          {
            "groupKey": {{index .app.metadata.labels "application/name" | default .app.metadata.name | toJson}},
            "labels": {{.app.metadata.labels | toJson}},
            "appName": {{.app.metadata.name | toJson}},
            "target": {{index .app.metadata.labels "application/target" | default "" | toJson}},
            "backend": {{index .app.metadata.labels "application/backend" | toJson}},
            "trigger": "on-sync-failed",
            "healthStatus": {{.app.status.health.status | toJson}},
            "syncPhase": {{.app.status.operationState.phase | toJson}},
            "syncStatus": {{.app.status.sync.status | toJson}},
            "operationMessage": {{.app.status.operationState.message | default "" | trunc 500 | toJson}},
            "revision": {{$rev | toJson}},
            "repoURL": {{$src.repoURL | toJson}},
            "argocdUrl": {{.context.argocdUrl | toJson}},
            "recipient": {{.recipient | toJson}}
          }

  template.app-health-degraded: |
    webhook:
      argocd-notifier:
        method: POST
        path: /events
        body: |
          {
            "groupKey": {{index .app.metadata.labels "application/name" | default .app.metadata.name | toJson}},
            "labels": {{.app.metadata.labels | toJson}},
            "appName": {{.app.metadata.name | toJson}},
            "target": {{index .app.metadata.labels "application/target" | default "" | toJson}},
            "backend": {{index .app.metadata.labels "application/backend" | toJson}},
            "trigger": "on-health-degraded",
            "healthStatus": {{.app.status.health.status | toJson}},
            "argocdUrl": {{.context.argocdUrl | toJson}},
            "recipient": {{.recipient | toJson}}
          }
```

Notes on this template shape:

- **`"trigger"` is a literal string per template**, not derived — each ArgoCD template name maps to exactly one trigger condition, so there's no need (and no clean way) to compute it dynamically from `.app`.
- **`{{.recipient}}`** carries the subscribe annotation's value straight through — this is how the aggregator knows which Slack channel(s) to post to, reusing whatever channel config you already have (see step 3).
- **`application/name`** is just an example label key — replace it with whatever label you actually use to group an app across clusters/environments, and set argocd-notifier's `GROUP_LABEL` to the same key.
- **`"backend"`** has no fallback — argocd-notifier rejects an event with no `backend` value (`ErrMissingBackend`), so every Application must carry the `application/backend` label (e.g. `slack`, or a separately registered remote backend — see `docs/remote-backends.md`).
- To wire up the remaining 5 triggers (`on-created`, `on-deleted`, `on-sync-running`, `on-sync-status-unknown`, `on-sync-succeeded`), repeat this pattern against `template.app-created`, `template.app-deleted`, etc. (or whatever your catalog names them), changing only the `"trigger"` literal and dropping fields that don't apply (e.g. `on-deleted` has no useful health/sync status).
- If you don't already have `trigger.on-*` definitions (no default catalog loaded), you also need those — see [ArgoCD's notification triggers docs](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/triggers/) for the conditions; argocd-notifier doesn't care what condition fired a trigger, only its name and the fields in the body above.

## 3. Subscribe your Applications

Label every Application you want grouped together with the label named in `GROUP_LABEL`, and add a subscribe annotation per trigger, per webhook service name (`argocd-notifier` in the examples above):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: tatooine-dev-new-republic
  labels:
    application/name: tatooine
    application/target: dev-new-republic
  annotations:
    notifications.argoproj.io/subscribe.on-deployed.argocd-notifier: "team-deploys"
    notifications.argoproj.io/subscribe.on-sync-failed.argocd-notifier: "team-deploys;team-oncall"
    notifications.argoproj.io/subscribe.on-health-degraded.argocd-notifier: "team-oncall"
spec:
  # ...
```

The annotation value is semicolon-joined Slack channel name(s) — exactly the same format/mechanism ArgoCD's built-in `slack` service already uses, just addressed at `argocd-notifier` instead. If you're generating Applications via an `ApplicationSet`, template these labels/annotations the same way you would for the `slack` service today; nothing else about your existing per-app channel config needs to change.

## 4. Verify end-to-end

1. `curl http://<argocd-notifier>:8080/healthz` → `200`.
2. Trigger a sync (or wait for auto-sync) on a subscribed Application.
3. Watch the aggregator's logs for `POST /events` handling; if `GROUP_LABEL` or the subscribe annotations are misconfigured, requests will 400 (missing required field — check `groupKey`/`recipient` are actually populated by your template) rather than silently doing nothing.
4. Confirm exactly one Slack message appears in the target channel, and that a second event for the same app/revision **edits** the same message rather than posting a new one.

If running with leader election, `curl http://<pod>:8080/readyz` returns `200` only on the current leader — confirm exactly one pod's `/readyz` is `200` at a time.
