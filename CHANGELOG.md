# Changelog

All notable changes to codex-swarm are documented here.

## [Unreleased]

## [0.7.2] - 2026-07-23

### Added

- A per-user NSIS installer for Windows amd64 and arm64 releases,
  including Installed Apps registration, optional user PATH integration,
  in-place upgrades, and clean uninstall behavior.

## [0.7.1] - 2026-07-22

### Fixed

- Detached caller-owned app-server runtimes no longer inherit the spawning
  CLI's stderr pipe. On Windows this allows `cs spawn --engine appserver` to
  return after durable task identity is recorded instead of remaining blocked
  until the first turn completes.

## [0.7.0] - 2026-07-22

### Added

- `cs attention` derives a scriptable human or JSON open-loop view from the
  authoritative SQLite records for queued messages, blocked claims, stale
  workers, validator rejections, failed gates, and pull-request next actions.
- `cs operation list|show` derives stable issue-first or root-worker operation
  keys across workers, claims, message deliveries, gate evidence, recorded pull
  requests, and linked Codex tasks, with keyless degraded records for broken
  ancestry or unsupported links.
- `cs decision record|list|show|supersede` preserves explicit rationale,
  evidence references, dissent, author identity, provenance gaps, and atomic
  supersession history against the derived logical-operation scope.
- `cs tasks ingest|list|status` maintains a durable, host-scoped Codex task
  index with stable pagination, unread and lifecycle metadata, coordinator
  classifications, and explicit missing or tombstone state.
- `cs tasks collect page|status|finish` gives each Codex host a resumable,
  idempotent way to contribute paged task snapshots without starting another
  app-server process or inferring task lifecycle.
- Daemon-owned app-server spawn: `cs spawn --engine appserver` now returns after
  durable host, thread, turn, and worktree readback while `csd` continues the
  first turn and reports completion asynchronously.
- Privileged daemons refuse to launch Codex. When the installed daemon runs as
  Windows LocalSystem or root, `cs` uses a detached, listener-free `csd`
  runtime under the caller's identity and sends the prompt over stdin.

### Fixed

- A caller timeout no longer marks an already-created Codex task failed.
  Worker-bound replay returns the original task identity and refuses prompt
  changes instead of creating a duplicate task.
- Daemon shutdown records an active app-server task as detached and resumable
  rather than treating runtime cancellation as a terminal task failure.
- A closed parent response pipe no longer cancels a detached app-server first
  turn after durable task identity has been recorded.

### Security

- GitHub Actions are pinned to canonical commit SHAs, with their tag targets
  and upstream provenance documented and verified before this release.
- Task-collection status readback now enforces the same loopback-only boundary
  as collection page and finish mutations.

## [0.6.1] - 2026-07-22

### Fixed

- Daemon worker summaries now preserve active turn, host, and runtime-owner
  identity instead of dropping the turn ID needed for native delivery.
- Message timeline mutations explicitly preserve attached worker thread, turn,
  execution-root, branch, remote, engine, and lifecycle identity.

### Changed

- `cs message` now returns a first-class native-steering request for an active
  externally owned Codex task. The owning Codex host injects the prompt and
  then uses guarded, idempotent `cs message confirm-steered` readback; `cs`
  never claims that a competing app-server connection delivered the message.
- Attached and `cs`-owned app-server workers now record runtime ownership
  explicitly. The daemon remains queue-capable without launching agent
  processes as a SYSTEM or root service.

## [0.6.0] - 2026-07-22

### Added

- Append-only SQLite delivery transitions with state, error, and timestamp
  readback for every coordination message.
- `cs message --wait` for bounded delivery observation and `--json` output for
  both `cs message` and `cs inbox`.
- Durable final app-server agent responses in worker transcripts, allowing a
  recipient acknowledgement to be correlated with its message, request, and
  delivery identity.
- A repeatable live/queued messaging acceptance runbook for real Codex
  app-server sessions.

### Changed

- Human-readable message and inbox output now includes message/request/delivery
  IDs, delivery timestamps, replay status, and transition history.
- CLI tests explicitly ignore an operator's installed daemon unless the test
  opts into an isolated daemon, preventing machine-global state from slowing or
  contaminating local-state tests.

### Verified

- A unique DM was visibly injected into a real destination Codex task over the
  active turn's existing app-server connection and acknowledged by the agent.
- Queued next-turn delivery, idempotent request replay, and exact-once delivery
  history were verified against the SQLite ledger and CLI readback.

## [0.5.0] - 2026-07-21

### Added

- Provider-neutral Windows `VERSIONINFO` generation for `cs.exe` and `csd.exe`, including product and file versions for AMD64 and ARM64 release builds.
- Isolated Codex app-server workers over SSH with per-worker remote Git branches and checkouts while coordination state remains local.
- `cs attach` for registering existing tracker or app-server tasks without fabricating worktrees or branches.
- `cs close` for idempotent, transactional worker closeout with claim release, PR refresh recording, and parent completion forwarding.
- Typed `path`, `task`, and `live` claim scopes, including atomic multi-scope creation through repeated `--scope` flags.
- Filtered and JSON `cs status` output with capability-oriented coordination coverage metrics for workers, claims, messages, deliveries, touches, and conflicts.

### Changed

- SQLite worker, claim, gate, and metric reads now query their record kinds directly instead of decoding the entire compatibility table.
- Worker snapshots use their actual generation time and no longer borrow fabricated worktrees or another worker's gate evidence.
- Pull-request next actions distinguish open, merged, closed, and unknown states; CodeRabbit remains visible but is counted separately from required CI checks.
- Runtime behavior and protocol reporting derive stable capabilities from engine identity and durable worker evidence, following the architectural boundary in issue `#68` without adding another coordination store.

## [0.4.1] - 2026-07-21

### Fixed

- Windows services now honor the persisted `serve --addr ... --state ...` arguments in their ImagePath instead of silently falling back to the LocalSystem default state path.
- Explicit service state paths can therefore share the same SQLite coordination ledger as the interactive `cs` client across service restarts and machine reboots.

## [0.4.0] - 2026-07-21

### Added

- Durable SQLite `messages`, `message_deliveries`, and `file_touches` tables.
- Loopback daemon routes for DM/subtree delivery, inbox reads, file touches, and child completion forwarding.
- Active-turn delivery through Codex `turn/steer` over the worker's existing app-server connection, with queued next-turn fallback.
- `cs inbox`, `cs touch`, and `cs message --subtree` operator commands.
- Automatic bilateral conflict notices for overlapping peer writes and automatic parent reports when a child finishes.
- App-server capture of final agent text and file-change items.

### Changed

- App-server thread and turn IDs are persisted as soon as `turn/start` succeeds, making busy workers discoverable while they run.
- JSON-RPC client traffic is multiplexed so active-turn steering and completion notifications can share one connection safely.
- Coordination remains warning-only: conflict detection records and informs but never rejects an edit or Git operation.

### Compatibility

- Existing state files migrate in place and keep the historical `records` table.
- Existing `cs message`, direct-state mode, and mock-worker behavior remain supported.

## [0.3.0] - 2026-07-09

- Migrated the backing store to SQLite and expanded daemon, Bifrost, readiness, and service-install operations.

## [0.2.0] - 2026-06-24

- Added trace lanes, janitor cleanup, version stamping, and cross-platform release automation.

## [0.1.0] - 2026-06-23

- Initial local-first CLI and daemon release.
