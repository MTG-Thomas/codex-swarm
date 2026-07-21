# Changelog

All notable changes to codex-swarm are documented here.

## [Unreleased]

### Added

- Provider-neutral Windows `VERSIONINFO` generation for `cs.exe` and `csd.exe`, including product and file versions for AMD64 and ARM64 release builds.
- `cs attach` for registering existing tracker or app-server tasks without fabricating worktrees or branches.
- `cs close` for idempotent, transactional worker closeout with claim release, PR refresh recording, and parent completion forwarding.
- Typed `path`, `task`, and `live` claim scopes, including atomic multi-scope creation through repeated `--scope` flags.
- Filtered and JSON `cs status` output with coordination coverage metrics for workers, claims, messages, deliveries, touches, and conflicts.

### Changed

- SQLite worker, claim, gate, and metric reads now query their record kinds directly instead of decoding the entire compatibility table.
- Worker snapshots use their actual generation time and no longer borrow fabricated worktrees or another worker's gate evidence.
- Pull-request next actions distinguish open, merged, closed, and unknown states; CodeRabbit remains visible but is counted separately from required CI checks.

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
