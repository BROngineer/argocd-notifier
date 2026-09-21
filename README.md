# argocd-notifier

ArgoCD notification aggregation service.

## Problem

ArgoCD's notifications-engine fires one message per `Application`. When an ApplicationSet fans a single service out across many clusters (e.g. one service targeting N clusters, all sharing a label like `application/name: sample`), a single fleet-wide deploy or incident produces one notification per cluster instead of one per rollout.

argocd-notifier's core sits between ArgoCD's notifications-engine and one or more notification backends — separate, self-registering processes that render/deliver to wherever they actually notify (Slack, email, PagerDuty, ...). It receives one webhook call per Application event, groups events sharing a label, and keeps **one live message per rollout**, editing it in place as more events arrive, instead of posting a new one every time.

## Design

The core debounces bursts of events sharing a label (an idle-reset window plus a hard cap so a rollout's first notification is never delayed indefinitely), keeps one *session* per rollout, and edits that session's message in place as more events arrive instead of posting a new one every time. Delivery is fully decoupled: the core never talks to Slack or anything else directly — a notification backend is a separate process that self-registers with the core over HTTP and receives pushed notifications, and the core only ever routes an event to whichever backend it named. See [docs/design.md](docs/design.md) for the full design (debounce/session mechanics, dedup, high availability) and [docs/remote-backends.md](docs/remote-backends.md) for the registration protocol itself.

## Notification backends

This project ships one backend out of the box — Slack ([`cmd/slack-backend`](cmd/slack-backend)) — deployable from this same chart as a second workload that self-registers against the core with no manual URL wiring:

```sh
helm install argocd-notifier ./chart \
  --namespace argocd \
  --set aggregation.groupLabel=application/name \
  --set slackBackend.enabled=true \
  --set slackBackend.slack.botToken=xoxb-your-bot-token
```

See `slackBackend.*` in [`chart/values.yaml`](chart/values.yaml) for every setting (existing-Secret support, `coreURL`/`publicBaseURL` overrides, etc.), and [docs/setup.md](docs/setup.md) for the full walkthrough including required Slack bot scopes.

Anything beyond Slack — email, PagerDuty, Teams, a custom webhook — is up to you: the core doesn't ship it and doesn't need to know about it in advance. Implement the self-registration and `/notify` contract described in [docs/adding-a-backend.md](docs/adding-a-backend.md), in any language, run it as your own process, and point it at the core.

## Documentation

- [docs/design.md](docs/design.md) — how it works, and why
- [docs/setup.md](docs/setup.md) — wiring ArgoCD's notifications-engine to argocd-notifier
- [docs/adding-a-backend.md](docs/adding-a-backend.md) — adding a notification backend
- [docs/remote-backends.md](docs/remote-backends.md) — the self-registration design

## Configuration

The service is configured entirely via environment variables — see [`chart/values.yaml`](chart/values.yaml) for the full list (server, aggregation, backend registry, logging, leader election, pprof, and the `slackBackend` workload above).

## High availability

Running more than 1 replica **requires** leader election (`LEADER_ELECTION_ENABLED=true`) — without it, a k8s `Service` load-balances ArgoCD's webhook calls across replicas, each keeping its own independent in-memory state, which reproduces the exact "many messages instead of one" problem this service exists to solve.

This is **failover-speed-plus-no-split-brain only, not state durability**: leader election guarantees exactly one replica is ever active, and failover to a standby is fast (seconds, bounded by `LEASE_DURATION`/`RENEW_DEADLINE`) — but the new leader still starts with empty in-memory session state and zero registered backends, same as a single-replica restart. Making session state (and thus in-flight message refs) survive a leader change would need externalized state (e.g. Redis) — deliberately out of scope for now. Backends re-registering periodically (heartbeat) is what lets them get noticed by a new leader — see [docs/remote-backends.md](docs/remote-backends.md).

Mechanism: each replica runs a `leaderelection.LeaderElector` (`internal/leader`) against a `coordination.k8s.io/v1` `Lease`. `/readyz` is **not** gated on leadership — every replica reports ready unconditionally, so the Deployment rollout completes normally and the k8s Service load-balances across all of them, same as without leader election. What changes is what happens once a request lands: the leader handles it locally; a non-leader forwards it to the current leader (`internal/leaderproxy`, a reverse proxy addressed via a headless Service — `<pod-name>.<fullname>-headless.<namespace>.svc.cluster.local`, no extra RBAC needed) rather than processing it itself, since the in-memory session/registry state that matters only exists on the leader. In the brief window right after a failover where no leader is known yet, a non-leader returns `503` for that request specifically — not a readiness change, and ArgoCD's webhook already retries on 5xx.

Requires RBAC to get/create/update `Lease` objects in `LEADER_ELECTION_NAMESPACE` — nothing more; forwarding a request to the leader is DNS-based, not a Kubernetes API call, so it needs no additional permissions (e.g. on `pods`):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: argocd-notifier-leader-election
  namespace: argocd
rules:
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "create", "update"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: argocd-notifier-leader-election
  namespace: argocd
subjects:
  - kind: ServiceAccount
    name: argocd-notifier
    namespace: argocd
roleRef:
  kind: Role
  name: argocd-notifier-leader-election
  apiGroup: rbac.authorization.k8s.io
```
