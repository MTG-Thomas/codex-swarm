# Changelog

All notable changes to codex-swarm are documented here.

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
