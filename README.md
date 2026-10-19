# argocd-notifier

ArgoCD notification aggregation service.

## Problem

The ArgoCD notifications-engine sends one message for each `Application`. An ApplicationSet can deploy one service to many clusters. All these `Application` objects can share a label, for example `application/name: sample`. In this case, one fleet-wide deploy or incident causes one notification for each cluster. You get N messages instead of one message for each rollout.

argocd-notifier has a core and one or more notification backends. The core sits between the ArgoCD notifications-engine and the backends. A backend is a separate process that registers itself with the core. It renders and delivers the message to the target system, for example Slack, email or PagerDuty.

The core receives one webhook call for each `Application` event. It groups the events that share a label. It keeps **one live message for each rollout** and edits this message in place when more events arrive. It does not post a new message each time.

## Design

The core debounces bursts of events that share a label. It uses an idle-reset window and a hard cap. The hard cap makes sure that the first notification of a rollout is never delayed without limit.

The core keeps one *session* for each rollout. It edits the message of the session in place when more events arrive.

Delivery is fully decoupled. The core never talks to Slack or to any other target directly. A notification backend is a separate process. It registers itself with the core over HTTP and receives pushed notifications. The core sends an event only to the backend that the event names.

- [docs/design.md](docs/design.md) gives the full design: debounce and session mechanics, deduplication and high availability.
- [docs/remote-backends.md](docs/remote-backends.md) gives the registration protocol.

## Notification backends

This project includes one backend: Slack ([`cmd/slack-backend`](cmd/slack-backend)). You can deploy it from this chart as a second workload. It registers itself with the core. You do not set a URL by hand.

```sh
helm install argocd-notifier ./chart \
  --namespace argocd \
  --set aggregation.groupLabel=application/name \
  --set slackBackend.enabled=true \
  --set slackBackend.slack.botToken=xoxb-your-bot-token
```

- Find all settings in the `slackBackend.*` section of [`chart/values.yaml`](chart/values.yaml). Examples are existing-Secret support and the `coreURL` and `publicBaseURL` overrides.
- For the full procedure, including the required Slack bot scopes, read [docs/setup.md](docs/setup.md).
- You can change how the backend renders messages. Read [docs/slack-message-templates.md](docs/slack-message-templates.md).

This project does not include other backends, for example email, PagerDuty, Teams or a custom webhook. The core does not need to know about a backend in advance. To add a backend, do these steps:

1. Implement the self-registration and `/notify` contract in [docs/adding-a-backend.md](docs/adding-a-backend.md). You can use any language.
2. Run the backend as your own process.
3. Point the backend at the core.

## Documentation

- [docs/design.md](docs/design.md) - how the service works, and why
- [docs/setup.md](docs/setup.md) - how to connect the ArgoCD notifications-engine to argocd-notifier
- [docs/slack-message-templates.md](docs/slack-message-templates.md) - how to customize the message rendering of the Slack backend
- [docs/adding-a-backend.md](docs/adding-a-backend.md) - how to add a notification backend
- [docs/remote-backends.md](docs/remote-backends.md) - the self-registration design

## Configuration

You configure the service only with environment variables. [`chart/values.yaml`](chart/values.yaml) lists all of them. The groups are server, aggregation, backend registry, logging, leader election, pprof and the `slackBackend` workload.

## High availability

If you run more than 1 replica, you **must** enable leader election (`LEADER_ELECTION_ENABLED=true`). Without leader election, a Kubernetes `Service` shares the ArgoCD webhook calls between the replicas. Each replica keeps its own in-memory state. This causes the problem that this service exists to solve: many messages instead of one.

Leader election gives fast failover and prevents split-brain. It does not make the state durable.

- Leader election makes sure that only one replica is active.
- Failover to a standby replica takes seconds. `LEASE_DURATION` and `RENEW_DEADLINE` set the limit.
- The new leader starts with empty in-memory session state and no registered backends. A restart of a single replica gives the same result.
- To keep session state (and the references to messages in progress) after a leader change, the service needs external state, for example Redis. This is deliberately out of scope for now.
- A backend registers again at regular intervals (heartbeat). A new leader finds the backend through this heartbeat. See [docs/remote-backends.md](docs/remote-backends.md).

### Mechanism

Each replica runs a `leaderelection.LeaderElector` (`internal/leader`) against a `coordination.k8s.io/v1` `Lease`.

`/readyz` does not depend on leadership. Every replica always reports ready. The Deployment rollout completes normally, and the Kubernetes `Service` shares requests between all replicas, as it does without leader election.

When a request arrives, the replica does one of these actions:

- The leader processes the request locally.
- A non-leader forwards the request to the current leader through a reverse proxy (`internal/leaderproxy`). The in-memory session and registry state exists only on the leader.

The service finds the address of the leader with a `Pods.Get` call to the Kubernetes API (`internal/leader.PodAddressResolver`). It caches the address until the leader changes.

We first used DNS with per-pod hostnames through a headless Service. We reverted this change. A Deployment cannot give each replica a unique and stable hostname, but a StatefulSet can. The per-pod DNS records were never created.

Directly after a failover, no leader can be known for a short time. In this window, a non-leader returns `503` for the request. This is not a readiness change. The ArgoCD webhook already retries on 5xx.

### RBAC

Leader election needs these permissions:

- `Lease` objects in `LEADER_ELECTION_NAMESPACE`.
- Read-only `Pods.Get` in the namespace of the pods (`Release.Namespace`). This namespace can differ from `LEADER_ELECTION_NAMESPACE`. For this reason, the two permissions use separate Role and RoleBinding pairs.

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
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: argocd-notifier-pod-lookup
  namespace: argocd
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: argocd-notifier-pod-lookup
  namespace: argocd
subjects:
  - kind: ServiceAccount
    name: argocd-notifier
    namespace: argocd
roleRef:
  kind: Role
  name: argocd-notifier-pod-lookup
  apiGroup: rbac.authorization.k8s.io
```
