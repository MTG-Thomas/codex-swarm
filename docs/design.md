# codex-swarm architecture

## Purpose

`codex-swarm` supplies durable local coordination around Codex and adjacent
worker runtimes. It records who owns a task, where it is executing, what it can
do, what it touched, how messages were delivered, which evidence was produced,
and how the work closed.

It is not an agent runtime, a source-control system, or a replacement control
plane for GitHub, Bifrost, or another target platform.

## System shape

```text
cs operator CLI
  |-- direct SQLite transactions
  |-- optional loopback calls to csd
  |-- explicit Git, gh, SSH, and Bifrost adapters
  `-- Codex app-server sessions

csd local daemon
  |-- read-only status and event APIs
  |-- idempotent message, touch, completion, readiness, and dispatch APIs
  `-- the same machine-global SQLite store
```

The CLI remains useful without the daemon. The daemon adds a long-lived local
delivery and inspection surface; it does not become an arbitrary remote shell.

## Authority boundaries

The machine-global SQLite store is authoritative for workers, capabilities,
claims, messages, events, gates, issue and PR links, and lifecycle state. The
historical default filename remains `state.json` for compatibility. Migration
retains a legacy JSON copy with a `.legacy.json` suffix.

Other systems keep their own authority:

- Git owns commits, branches, worktrees, merges, and source conflicts.
- GitHub owns issues, pull requests, checks, and reviews.
- Bifrost owns workspace revisions, changesets, validation, activation, and
  compare-and-swap decisions.
- Remote SSH hosts own their Git and Codex credentials.
- Codex app-server owns its threads, turns, and runtime events.

The Codex task index is a durable discovery cache, not lifecycle authority.
Codex hosts explicitly ingest bounded metadata snapshots. Stable host/thread
identity, labels, paths, observed status and unread state, source timestamps,
last-seen state, and opaque wait cursors remain discoverable after the task
falls outside the host's current listing window. Prompt and response bodies are
not part of this schema. Coordinator-authored P0-P3 classification, outcome,
unresolved-loop, operator-decision, and next-action summaries may be retained
without copying task messages. A missing task is recorded only from a host-declared
complete snapshot; absence from a bounded window is not completion, deletion,
or even evidence that the task is unavailable.

Host-observed task status does not derive from an attached swarm worker. This
keeps an active/resumable Codex task visible when a synchronous launch request
times out and marks only its worker attempt failed.

Logical operations are also derived rather than persisted. A normalized GitHub
issue reference is the strongest key; otherwise workers inherit the root
parent worker key. Claims can use their own explicit issue link, while
messages, gates, pull-request state, and indexed Codex tasks join only through
their existing worker or exact runtime identity. Broken ancestry and unlinked
records stay visible without receiving a fabricated key. This projection is a
protocol seam for operation-level evidence and decisions, not a new authority.

Swarm records identity, warnings, links, and returned evidence. It does not
silently override a target system's decision.

## Worker model

A worker records its repository, role, parent, prompt, lifecycle state, engine,
thread and turn identity, issue and pull-request links, optional worktree, and
operator report.

Engine names describe implementation. Capabilities describe behavior:

- `live_message`
- `resume`
- `managed_worktree`
- `automatic_completion`
- `external_tracker`

Coordination code should branch on capabilities. Engine identity remains
available for diagnostics and engine-specific transport.

Attaching an existing task creates a tracker record without inventing runtime
ownership. App-server workers record host, thread, turn, and runtime owner. A
`cs`-owned turn polls durable messages over its existing connection. An
externally owned task exposes a native-steering request for its owning Codex
host instead of opening a competing connection. Remote app-server workers
retain the same coordination model while their checkout and Codex process live
over SSH.

## State and events

SQLite transactions preserve worker lifecycle, normalized messages and
deliveries, append-only delivery transitions, claims, recent file touches,
gates, and event envelopes. Store
mutations must be atomic and safe under concurrent CLI and daemon access.

The task index uses normalized SQLite rows because host, status, unread,
staleness, and project filters plus keyset pagination are proven query needs.
Snapshot ingestion has its own durable request-ID replay table. Reusing a
request ID with different normalized metadata is rejected. The replay table is
bounded to the latest 5,000 ingests so a long-running heartbeat cannot grow it
without limit. Seen, missing, and tombstoned transitions share a durable
observation-time/request-ID watermark, while coordinator classification has a
separate classification-time/request-ID watermark; late snapshots cannot
regress either state machine.

Worker snapshots are deterministic handoff artifacts. Transcripts expose the
durable event timeline. Work packets combine worker identity, repository,
worktree, issue, claims, recent events, report, and next action for resumption.

State schema changes should remain narrow. Add a table or index when a proven
query, transaction, retention, or migration boundary needs it—not to mirror
every external object.

## Messaging and conflict detection

Messages are durable before delivery is attempted. A `cs`-owned active turn can
receive `turn/steer` over the app-server connection that owns it. For an
externally owned active task, the message response carries a native-steering
request with the exact prompt and runtime identity. The owning Codex host
injects it and confirms the delivery against the same worker, thread, and turn.
No confirmation means the delivery remains queued. Every material delivery
observation is retained with its state, error, and timestamp; repeated
identical observations do not create duplicate transitions. Final app-server
agent text is retained in the worker event timeline so a recipient's
acknowledgement can be correlated with the message and delivery IDs.

The communication vocabulary stays small:

- direct and subtree messages
- handoffs
- child completion reports
- bilateral conflict warnings

File touches record read or write intent. Overlapping writes create conflict
messages for both workers. Claims cover path, task, or live-resource scopes.
Neither mechanism is a lock, and neither rejects the underlying operation.

## Lifecycle and closeout

`close` is the normal terminal transaction. It marks a worker done or failed,
releases all active claims, refreshes attached pull requests, clears blocker
fields, and forwards completion to the parent. Request IDs make retries
idempotent.

`report` remains a lower-level lifecycle update for cases where claims must stay
active. Janitor commands identify stale workers and releasable claims; applying
cleanup is always explicit.

## Git and workspace isolation

Codex thread separation is conversation isolation, not filesystem isolation.
Parallel writers need distinct branches and worktrees. Managed worktrees use
repo-local branch locks only while swarm creates or reuses the checkout. Dirty
worktrees are preserved and reported rather than reset.

Remote Git sessions create an isolated remote checkout and branch per worker.
They do not push, open a pull request, merge, or delete the remote workspace
without an explicit operator action.

## Daemon contract

Broad daemon surfaces are read-only. Mutation routes remain loopback-only,
small, typed, and idempotent. API contracts shared outside handlers live in
`internal/protocol`; versioned mutation paths return typed JSON errors.

`GET /v1/codex-tasks` and `GET /v1/codex-tasks/status` expose the discovery
cache. `POST /v1/codex-tasks/ingest` is the only task-index mutation: it accepts
a bounded metadata-only snapshot, requires loopback access and a request ID,
and does not start Codex or scrape proprietary session storage.

The daemon's message route is queue-capable but intentionally does not launch
Codex processes. Native steering of an external task belongs to the Codex host
that owns its connection, preventing a SYSTEM or root service from inheriting
agent credentials or turning a loopback mutation into privileged command
execution.

New daemon mutations must demonstrate:

1. a real long-lived delivery or recovery need;
2. an explicit request ID and stable replay result;
3. durable state readback before success;
4. bounded inputs without arbitrary command execution; and
5. tests for cancellation, concurrent access, and error identity.

## Dependency policy

Prefer the Go standard library. Dependencies are justified where they reduce
cross-platform or persistence risk at a durable boundary. Current examples are
SQLite and native Windows service integration. A CLI framework, GitHub SDK, or
service abstraction should be added only when the existing boundary becomes
measurably less safe or maintainable.
