# Setup

This document explains how to connect the ArgoCD notifications-engine to argocd-notifier. After you do this, argocd-notifier aggregates real notifications. For the behavior of the service after setup, see [design.md](design.md).

Setup has three parts:

1. Deploy argocd-notifier.
2. Add argocd-notifier as a webhook notification service in ArgoCD.
3. Subscribe your Applications to the service.

## 1. Deploy argocd-notifier

Use the chart in [`chart/`](../chart):

```sh
helm install argocd-notifier ./chart \
  --namespace argocd \
  --set aggregation.groupLabel=application/name  # must match a real label on your Applications — see step 3
```

See [`chart/values.yaml`](../chart/values.yaml) for all settings.

For more than one replica, add `--set leaderElection.enabled=true`. The chart renders the necessary RBAC `Role` and `RoleBinding` automatically. Read [design.md#high-availability](design.md#high-availability) first. Do not enable leader election for a single replica.

This command deploys the core only. The core receives, debounces and routes events. It does not talk to Slack or to any other target directly. The core sends notifications only after at least one backend process registers itself with the core. For the registration contract, see [`adding-a-backend.md`](adding-a-backend.md). For the design behind it, see [`remote-backends.md`](remote-backends.md).

For Slack, the same chart can also deploy the reference backend [`cmd/slack-backend`](../cmd/slack-backend). It runs as a second workload in the same release. When you enable it, it registers itself with the core Service that this release creates. You do not set a URL by hand:

```sh
helm install argocd-notifier ./chart \
  --namespace argocd \
  --set aggregation.groupLabel=application/name \
  --set slackBackend.enabled=true \
  --set slackBackend.slack.botToken=xoxb-your-bot-token
```

In production, use an existing Secret that you manage yourself (for example with sealed-secrets or external-secrets). Do not use `slackBackend.slack.botToken`. To use an existing Secret, set `--set slackBackend.slack.existingSecret=my-slack-secret`.

The Slack bot token needs the `chat:write` scope. Invite the bot to every channel that you want to notify (`/invite @your-bot`). If you do not, `chat.postMessage` and `chat.update` fail with `not_in_channel`.

See `slackBackend.*` in [`chart/values.yaml`](../chart/values.yaml) for all settings. The settings include the `coreURL` and `publicBaseURL` overrides. Use them to point this backend at a core that you deploy outside this release.

To profile a running instance, set `--set pprof.enabled=true`. The profiler uses its own container port. The Service deliberately does not expose this port. Then run:

```sh
kubectl port-forward -n argocd deploy/argocd-notifier 6060:6060
go tool pprof http://localhost:6060/debug/pprof/heap
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

## 2. Add argocd-notifier as a webhook service in ArgoCD

Edit `argocd-notifications-cm`. This is the ConfigMap that the ArgoCD notifications-engine reads:

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

The `url` is the base address. The HTTP path of argocd-notifier is always `/events` (see `api/core/openapi.yaml`). You set the path in each template (see below).

Next, add or edit the templates for the triggers that you want to aggregate. If you already loaded the default notification catalog of ArgoCD, these template *names* likely exist already. They feed the default `slack:` key. You add a `webhook:` key to the same template. You do not replace the trigger definitions.

The examples below show three templates. The Slack backend renderer (`internal/slack/render.go`) can draw the same fields for all 8 built-in ArgoCD triggers. The triggers are `on-created`, `on-deleted`, `on-deployed`, `on-health-degraded`, `on-sync-failed`, `on-sync-running`, `on-sync-status-unknown` and `on-sync-succeeded`.

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
            "targetRevision": {{$src.targetRevision | toJson}},
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
            "targetRevision": {{$src.targetRevision | toJson}},
            "argocdUrl": {{.context.argocdUrl | toJson}},
            "recipient": {{.recipient | toJson}}
          }

  template.app-health-degraded: |
    webhook:
      argocd-notifier:
        method: POST
        path: /events
        body: |
          {{$src := .app.spec.source}}{{if not $src}}{{$src = index .app.spec.sources 1}}{{end}}
          {
            "groupKey": {{index .app.metadata.labels "application/name" | default .app.metadata.name | toJson}},
            "labels": {{.app.metadata.labels | toJson}},
            "appName": {{.app.metadata.name | toJson}},
            "target": {{index .app.metadata.labels "application/target" | default "" | toJson}},
            "backend": {{index .app.metadata.labels "application/backend" | toJson}},
            "trigger": "on-health-degraded",
            "healthStatus": {{.app.status.health.status | toJson}},
            "targetRevision": {{$src.targetRevision | toJson}},
            "argocdUrl": {{.context.argocdUrl | toJson}},
            "recipient": {{.recipient | toJson}}
          }
```

Notes on the template shape:

- **`"trigger"` is a literal string in each template.** It is not derived. Each ArgoCD template name maps to exactly one trigger condition. You do not need to compute it from `.app`, and no clean way to do it exists.
- **`{{.recipient}}` carries the value of the subscribe annotation unchanged.** The aggregator uses it to find the Slack channels to post to. It reuses the channel configuration that you already have (see step 3).
- **`application/name` is only an example label key.** Replace it with the label that you use to group an app across clusters or environments. Set the `GROUP_LABEL` of argocd-notifier to the same key.
- **`"backend"` has no fallback.** argocd-notifier rejects an event that has no `backend` value (`ErrMissingBackend`). Each Application must have the `application/backend` label. The value is `slack` or the name of a separately registered remote backend (see `docs/remote-backends.md`).
- To connect the 5 remaining triggers, repeat this pattern. The triggers are `on-created`, `on-deleted`, `on-sync-running`, `on-sync-status-unknown` and `on-sync-succeeded`. Use `template.app-created`, `template.app-deleted` and so on, or the names in your catalog. Change only the `"trigger"` literal. Remove the fields that do not apply. For example, `on-deleted` has no useful health or sync status.
- If you did not load a default catalog, you also need the `trigger.on-*` definitions. For the conditions, see [the ArgoCD notification triggers documentation](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/triggers/). argocd-notifier does not use the condition that fired a trigger. It uses only the trigger name and the fields in the body above.

## 3. Subscribe your Applications

Give each Application that you want to group the label that `GROUP_LABEL` names. Then add one subscribe annotation for each trigger and for each webhook service name. In the examples above, the service name is `argocd-notifier`:

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

The annotation value is a list of Slack channel names, separated by semicolons. The built-in `slack` service of ArgoCD uses the same format and the same mechanism. The only change is the address: `argocd-notifier` replaces `slack`.

If you generate Applications with an `ApplicationSet`, add these labels and annotations in the same way as you do for the `slack` service today. The channel configuration of each app stays the same.

## 4. Verify end-to-end

1. Run `curl http://<argocd-notifier>:8080/healthz`. The response must be `200`.
2. Trigger a sync on a subscribed Application, or wait for the auto-sync.
3. Read the logs of the aggregator for `POST /events` handling. If `GROUP_LABEL` or the subscribe annotations are wrong, the requests fail with `400` (missing required field). Make sure that your template fills `groupKey` and `recipient`. The service does not fail silently.
4. Make sure that exactly one Slack message appears in the target channel. A second event for the same app and revision must **edit** this message. It must not post a new message.

If leader election is on, every replica returns `200` on `/readyz`. To find the leader, look at which pod holds the lease:

```sh
kubectl get lease argocd-notifier-leader -n argocd -o jsonpath='{.spec.holderIdentity}{"\n"}'
```
