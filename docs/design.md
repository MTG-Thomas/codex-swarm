# codex-swarm design sketch

## Goal

Build a small local daemon and CLI that use Codex's app-server JSON-RPC API as the agent execution primitive. The project should make Codex workers easier to run, resume, schedule, and connect to GitHub issues without carrying a full dashboard/plugin/runtime framework.

## Non-goals

- replacing Codex CLI or Codex app-server
- implementing a terminal multiplexer
- building a general agent marketplace
- requiring MCP for routine worker communication

## Shape

```text
cs CLI
  -> csd local daemon
      -> codex app-server child process
      -> local state store
      -> git worktrees
      -> optional GitHub integration
```

## Core records

- project: repository root, default branch, config path
- worker: local ID, project, worktree, branch, status
- codex thread: app-server thread ID, model, last event timestamp
- task: prompt, issue/PR links, lifecycle status, report summary

## Operating model

Local state is authoritative. The machine-global `codex-swarm` state file is
the source of truth for workers, lifecycle, events, claims, issue links, and
reports. GitHub issue markers are a synchronization and audit surface only:
they can exchange claim snapshots across machines and leave an operator-visible
trail, but they do not replace local state or make GitHub the coordination
database.

Codex session identity and workspace isolation are separate concerns. Forking or
resuming a Codex app-server thread gives a worker a distinct conversation
lineage, but it does not make filesystem writes safe. Parallel mutation still
requires explicit branch and worktree isolation, and the CLI must keep warning
when a worker has a session without a distinct worktree.

The daemon keeps broad operational surfaces read-only, while a narrow set of
loopback-only mutations use explicit request IDs and idempotent replay:
dispatch, messages, file touches, and completion forwarding. These routes do
not expose arbitrary commands, Git mutations, or filesystem writes. Messages
are durable before delivery is attempted, and conflict records are warnings,
not locks.

Daemon API contracts that are intended to be consumed outside the handler live
in `internal/protocol`. Versioned `/v1/*` mutation paths return typed JSON
errors, and new daemon write APIs should use explicit request IDs with stable
replay behavior before they are exposed. The read-only `/v1/events` endpoint
returns worker event envelopes for dashboards and autonomous workers without
changing local state.

Worker handoff context has two surfaces. `cs transcript <worker>` renders the
durable event timeline for human or JSON consumers. `cs workpacket --worker
<worker>` emits a structured startup packet with repo, worktree, branch, issue,
thread, claims, recent events, report, and next action. `cs worker check` is a
warning-only ownership readback for repo, issue, worktree, thread, and active
claim risks; it does not turn claims into locks.

Event stream snapshot example:

```powershell
Invoke-WebRequest http://127.0.0.1:8787/v1/events?worker=w-123
Invoke-WebRequest http://127.0.0.1:8787/v1/events?format=ndjson
```

## Initial commands

```text
cs status
cs spawn --repo . --prompt "..."
cs send <worker> "..."
cs resume <worker>
cs report <worker> done
```

## MVP slice

The first demoable slice supports both a deterministic mock worker and a real `codex app-server` engine behind the same operator commands:

- `spawn --engine appserver` initializes `codex app-server`, starts a thread, sends the first turn, waits for `turn/completed`, and stores the real thread and turn IDs.
- `send` resumes the stored app-server thread, starts another turn, waits for completion, and appends the event.
- `resume` verifies that the stored app-server thread can be reattached.
- `show` prints worker details and the event timeline.
- `report` records done/failed/idle state and a human-readable report.
- `status` lists current workers from local durable state.
- `--engine mock` uses the same state model without live Codex calls.

SQLite stores compatibility records plus normalized messages, per-recipient
deliveries, and recent file touches. An active CLI-owned app-server turn is
persisted immediately after `turn/start`; the existing connection polls queued
deliveries and uses `turn/steer`. This provides live delivery without requiring
the daemon to open a second app-server process. Non-steerable deliveries remain
queued for the next turn.

Next real-worker slice:

1. Move remaining spawn/send/report control into the daemon where it improves recovery.
2. Stream more assistant deltas and tool intents into the worker event model.
3. Add precise pre-edit read intents when Codex exposes a stable hook.
4. Keep the DM/subtree/conflict/completion vocabulary small.

## Dependency policy

Start with the Go standard library. Add dependencies only when they reduce operational risk, testing cost, or cross-platform edge cases enough to justify the extra moving part.

Expected future triggers:

- SQLite schema: extend only when a normalized query or transactional boundary
  is proven by an operator workflow.
- GitHub client library: adopt when fake-`gh` coverage becomes harder to
  maintain than typed API tests, or when issue sync needs API features the `gh`
  boundary cannot express cleanly.
- Service manager/helper package: adopt when `csd install` becomes a real
  Windows service, launchd, and systemd surface instead of explicit stubs.
- CLI framework: adopt only when stdlib `flag` makes help text, subcommand
  routing, or shell completion measurably harder to maintain.
