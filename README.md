# argocd-notifier

ArgoCD notification aggregation service.

## Problem

ArgoCD's notifications-engine fires one message per `Application`. When an ApplicationSet fans a single service out across many clusters (e.g. one service targeting N clusters, all sharing a label like `application/name: sample`), a single fleet-wide deploy or incident produces one Slack message per cluster instead of one per rollout.

argocd-notifier sits between ArgoCD's notifications-engine and Slack: it receives one webhook call per Application event, groups events sharing a label, and keeps **one live Slack message per rollout**, editing it in place as more events arrive, instead of posting a new message every time.

## Documentation

- [docs/design.md](docs/design.md) — how it works, and why
- [docs/setup.md](docs/setup.md) — wiring ArgoCD's notifications-engine to argocd-notifier
- [docs/adding-a-backend.md](docs/adding-a-backend.md) — adding a notification backend beyond Slack

## Configuration

The service is configured entirely via environment variables — see [`chart/values.yaml`](chart/values.yaml) for the full list (server, aggregation, Slack, logging, leader election, pprof).

## High availability

Running more than 1 replica **requires** leader election (`LEADER_ELECTION_ENABLED=true`) — without it, a k8s `Service` load-balances ArgoCD's webhook calls across replicas, each keeping its own independent in-memory state, which reproduces the exact "many messages instead of one" problem this service exists to solve.

This is **failover-speed-plus-no-split-brain only, not state durability**: leader election guarantees exactly one replica is ever active, and failover to a standby is fast (seconds, bounded by `LEASE_DURATION`/`RENEW_DEADLINE`) — but the new leader still starts with empty in-memory session state, same as a single-replica restart. Making session state (and thus in-flight Slack message `ts` references) survive a leader change would need externalized state (e.g. Redis) — deliberately out of scope for now.

Mechanism: each replica runs a `leaderelection.LeaderElector` (`internal/leader`) against a `coordination.k8s.io/v1` `Lease`. Only the current leader's `/readyz` returns 200 (`httpx.ReadyzHandler` fed by `Elector.IsLeader`); non-leaders report not-ready, so the k8s Service's endpoint list contains only the leader and routes 100% of traffic to it.

Requires RBAC to get/create/update `Lease` objects in `LEADER_ELECTION_NAMESPACE`:

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
