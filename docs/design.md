# Design

How argocd-notifier works, and why it's built the way it is.

## Problem

ArgoCD's notifications-engine fires one message per `Application`. When an ApplicationSet fans a single service out across many clusters — one service targeting N clusters, all sharing a label like `application/name: sample` — a single fleet-wide deploy or incident produces one Slack message per cluster instead of one per rollout. For a service deployed to, say, 18 clusters, that's 18 separate messages for what a human thinks of as one event.

argocd-notifier sits between ArgoCD's notifications-engine and Slack. It receives one webhook call per `Application` trigger firing, groups events that share a label, and keeps **one live Slack message per rollout**, editing that message in place as more events arrive, instead of posting a new message every time.

## Components

| Package | Responsibility |
|---|---|
| `internal/event` | The wire contract with ArgoCD — the JSON shape a webhook call decodes into, and validation of the fields the rest of the system depends on. |
| `internal/receiver` | HTTP handler that decodes/validates/enqueues an event and acks fast (`202`), plus the worker pool draining the queue. |
| `internal/aggregator` | The debounce engine (`Engine`) that batches events per group, and the session store (`SessionPublisher`) that turns batches into backend posts/updates. |
| `internal/notification` | Backend-neutral domain model (`Notification`, `Item`) built from a session's per-app state, plus the `Backend`/`Updater`/`ThreadReplier` interfaces every notification backend implements. See [adding-a-backend.md](adding-a-backend.md). |
| `internal/slack` | The Slack backend: renders a `Notification` into Block Kit and talks to the Slack Web API (`chat.postMessage` / `chat.update`) with retry/backoff. |
| `internal/leader` | Leader election for safely running more than one replica (see [High Availability](#high-availability)). |
| `internal/config` | Environment-variable-driven configuration for all of the above. |
| `cmd/argocd-notifier` | Wires everything together and runs the HTTP server. |

## Why a debounce window, not just batching

An idle-reset timer (`IDLE_WINDOW`) extends every time a new event arrives in the same group; an independent hard cap (`MAX_WAIT`) never extends. Whichever fires first flushes the batch. This paces how often Slack gets hit — it does **not** define how long a rollout's message stays live (that's the session's job, below). Without the hard cap, a group that keeps receiving events in quick succession (e.g. a slow rolling deploy) could delay its first Slack message indefinitely; without the idle-reset, a burst of near-simultaneous events would get split across multiple debounce windows instead of batching into one.

## Sessions: why messages update in place

A Slack message represents one *session*. By default a session is keyed by `(groupKey, revision)`; with `COMBINE_TRIGGERS=false` it's keyed by `(groupKey, revision, trigger)` instead, so e.g. a deploy-success wave and a health-degraded wave for the same revision never share a message.

Every debounce flush for an existing session calls `chat.update` on the same message timestamp instead of posting a new message. The session keeps a `perApp` map of each app's *latest* known event (last-write-wins), so the message always reflects current reality — not just the delta from the most recent flush. A session expires after `SESSION_TTL` of inactivity; a later event for the same revision then starts a fresh message rather than editing one that has scrolled far out of view in the channel.

## Duplicate events: no standalone dedup cache

ArgoCD's webhook delivery is at-least-once, and the payload carries no send-id — so a network retry of the exact same underlying event is structurally indistinguishable from a genuine recurrence of identical status (e.g. the same app reporting "Degraded" twice, days apart). Two things make this tractable without a separate dedup cache:

1. **Retries landing in the same debounce window are already harmless.** They merge into the same `perApp` entry (last-write-wins, identical values) and the batch still produces exactly one render/post per flush.
2. **For anything landing in a later window**, the session compares the incoming event's content hash (`trigger|revision|syncPhase|healthStatus|operationMessage`) against the app's last recorded entry. A real change (even a same-trigger status refinement, e.g. `Error` → `Failed`) always updates the main message. Identical content is handled per the configurable `DUPLICATE_ACTION`:
   - `"drop"` (default) — no-op, matches naive dedup, zero extra Slack API calls.
   - `"thread"` — a short plain-text Slack thread reply under the session's message, main message untouched.

   Since a thread reply is truthful either way (a retry and a genuine recurrence both mean "this is still/again true"), treating an ambiguous case as a real recurrence costs little — there's no need to guess correctly.

## Rendering and delivery

`notification.Build` sorts apps by name for deterministic output and builds one backend-neutral `Item` per app via a per-trigger builder — matching each of ArgoCD's built-in triggers (`on-created`, `on-deleted`, `on-deployed`, `on-health-degraded`, `on-sync-failed`, `on-sync-running`, `on-sync-status-unknown`, `on-sync-succeeded`). An event whose trigger isn't recognized is skipped from the item list but still counted in the notification's summary line. `Item` carries structured fields (`CommitURL`/`CommitSHA`, `TriggeredBy`, `Images`, a generic `Fields` list) rather than any backend's markup — turning that into an actual message is each backend's own job. The Slack backend renders `Item`s into Block Kit attachments and chunks past Slack's documented 100-attachment limit with a "+N more" marker; a different backend would chunk (or not) according to its own limits.

The aggregator talks to backends **directly** — ArgoCD calls argocd-notifier through a `service.webhook.<name>` notifier, not the other way around (see [setup.md](setup.md) for the ArgoCD-side wiring). The existing per-app Slack channel list is reused verbatim: the subscribe-annotation's recipient value carries straight through as `.recipient` in the webhook body, so there's no new per-app config surface on the ArgoCD side. A session's recipient can be multiple channels (semicolon-joined); each gets its own post/update, tracked by its own backend-specific message reference (`MessageRefs`).

Only `Backend.Post` is required of every backend; `Updater` (edit an existing message) and `ThreadReplier` (reply under one) are optional capabilities checked via type assertion — a backend that can't edit just gets a fresh `Post` every flush instead of an in-place update, and `duplicateAction=thread` is rejected at startup (not silently ignored) if the wired backend doesn't implement `ThreadReplier`. See [adding-a-backend.md](adding-a-backend.md) for how to add one.

## Flow

```mermaid
flowchart TD
    A["ArgoCD Application\n(any cluster)"] -->|sync/health status change| B["notifications-engine trigger fires\n(any of the 8 built-in triggers)"]
    B -->|"synchronous webhook call\n(blocks the controller worker until acked)"| C["POST /events"]
    C --> D{"valid JSON + required fields?"}
    D -->|no| D1["400\n(no retry)"]
    D -->|yes| E["enqueue to buffered channel"]
    E --> F["202 Accepted\n(ArgoCD worker freed immediately)"]
    E --> G["worker pool: Engine.Ingest(event)"]
    G --> I["groupState[groupKey].buckets[trigger] += event"]
    I --> J["first event of batch? start hard-cap timer (MaxWait)\nevery event: reset idle timer (IdleWindow)"]
    J --> K{"idle timer OR hard-cap timer fires"}
    K --> L["flush(groupKey)\nsnapshot + clear buckets, stop timers"]
    L --> M["split into batches by revision\n(+ trigger, if combineTriggers=false)"]
    M --> N["sessions.Upsert(SessionKey, events)"]
    N --> N1["for each event:\nhash := contentHash(trigger, revision, syncPhase, healthStatus, operationMessage)"]
    N1 --> N2{"no prior perApp[appName]\nOR hash changed?"}
    N2 -->|yes: real change or first sighting| N3["perApp[appName] = event"]
    N2 -->|no: content unchanged| N4{"duplicateAction config"}
    N4 -->|drop, default| N5["no-op"]
    N4 -->|thread| N6["backend.(ThreadReplier).PostThreadReply(recipient, refs[recipient], note)\nmain message untouched"]
    N3 --> O["notification.Build(perApp) -> Notification{Summary, Items}"]
    O --> P{"backend implements Updater\nAND refs[recipient] exists?"}
    P -->|no| Q["backend.Post(recipient, n)\nstore returned ref only if backend implements Updater"]
    P -->|yes| R["backend.(Updater).Update(recipient, ref, n)\n(same message edited in place)"]
    Q --> S["session.lastFlush = now"]
    R --> S
    S --> T{"next Upsert for this key:\nnow - lastFlush > SessionTTL?"}
    T -->|yes| U["session replaced with a fresh one\n(next event for that revision posts a NEW message)"]
    T -->|no| S
```

## High availability

Running more than one replica requires leader election (`LEADER_ELECTION_ENABLED=true`). Without it, a Kubernetes `Service` load-balances ArgoCD's webhook calls across replicas, each keeping independent in-memory state — which reproduces the exact "many messages instead of one" problem this service exists to solve.

This buys **failover speed and no split-brain, not state durability**: exactly one replica is ever active (`internal/leader.Elector` campaigns for a `coordination.k8s.io/v1` `Lease`), and only the current leader's `/readyz` reports ready, so the Service's endpoint list — and therefore all traffic — follows the leader automatically. But the new leader still starts with empty in-memory session state, same as a plain restart; making session state (and in-flight Slack message `ts` references) survive a leader change would need externalized state (e.g. Redis), which is deliberately out of scope. See [README.md](../README.md#high-availability) for the required RBAC.

## Debugging

`net/http/pprof` is served on its own listener (`PPROF_ADDR`, default `:6060`) when `PPROF_ENABLED=true`, registered on a dedicated mux rather than `http.DefaultServeMux` — kept deliberately separate from the main server so it's never reachable through whatever routes ArgoCD's webhook traffic or readiness checks in. See [setup.md](setup.md) for `port-forward`/`go tool pprof` usage.

## Known limitations (accepted, not accidental)

- **In-memory state, no persistence.** A pod restart mid-rollout loses the session→message mapping; the next event for that revision posts a new message instead of editing the old one. Accepted as a rare-case tradeoff — restarts should be infrequent, and root causes (e.g. OOMs) should be fixed rather than papered over with dedup machinery.
- **No fan-out-count awareness.** A message can say "3 apps degraded" but not "3 of 20" — ArgoCD's notification payload carries no information about how many other Applications share a label, so that would require a separate watch/informer against the Application CRD. Deferred.
- **Slack is the only backend implemented so far.** The `Backend`/`Updater`/`ThreadReplier` interfaces (`internal/notification`) exist specifically so a second backend doesn't require reworking the engine or session store — see [adding-a-backend.md](adding-a-backend.md). Running multiple backends *simultaneously* from one process (fan-out to Slack + Teams at once) isn't supported; each running instance wires exactly one backend.
- **100-attachment cap is a hardcoded default, not yet configurable.** Slack doesn't publish an exact byte-size threshold to defend against separately — revisit if/when a single rollout's app count grows enough for it to matter.
