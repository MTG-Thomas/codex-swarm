# codex-swarm roadmap

`codex-swarm` should mature as a local-first Codex orchestration tool, not a general agent platform. The priority is a dependable CLI and daemon that make real Codex threads easier to run, resume, coordinate, and connect to issue-driven work.

## Phase 0: polished friend-demo MVP

- Make app-server thread IDs and recovery commands obvious in command output.
- Add `cs doctor` for local prerequisites: Go, Git, Codex CLI, writable state path, and optional app-server initialization.
- Add `cs inspect-thread <worker>` to prove a stored app-server thread can still be resumed.
- Default state to a machine-global user config path, with `CODEX_SWARM_STATE` for isolated ledgers.
- Add `cs agent register/current/list` for durable local agent identity.
- Add `cs legacy import-coordinator` to migrate active PowerShell coordinator claims into `codex-swarm`.
- Keep the mock engine deterministic for tests and demos.

Exit criteria: a new user can spawn a real Codex worker, find the thread in the Codex app, resume it, and diagnose local setup failures without reading source.

## Phase 1: daemon-owned runtime

- Move app-server process ownership into `csd`.
- Let `cs` call the daemon for status, spawn, send, resume, and report.
- Use `CODEX_SWARM_DAEMON_URL` to opt `cs` into daemon-first status checks.
- Keep `csd serve` and `csd status` as the current daemon contract.
- Keep `csd install` and `csd uninstall` as explicit stubs until platform-specific service helpers exist.
- Keep direct CLI state mode available until daemon mode is proven.
- Persist daemon events in the same worker event model.
- Recover cleanly after daemon restart.

Exit criteria: repeated `cs` commands do not start a new app-server process each time, and `csd` can be restarted without losing worker identity.

## Phase 2: worktree isolation

- Create branches and worktrees per worker.
- Surface branch, base commit, dirty status, and worktree path in `cs status` and `cs show`.
- Add safe cleanup with explicit confirmation-oriented command names.
- Integrate local Codex coordination claims without committing coordination state to repos.

Exit criteria: parallel workers can operate on one repository without trampling each other's branches or user changes.

## Phase 3: local swarm primitives

- Track parent/child workers.
- Add local message and handoff records.
- Add role labels such as implementer, reviewer, tester, and docs.
- Add bounded fan-out controls for max workers, turns, and wall-clock time.
- Keep interagent communication in the daemon/store instead of MCP by default.

Exit criteria: one operator command can start a small set of role-based workers and leave an inspectable local trail of their communication and reports.

## Phase 4: GitHub issue integration

- Import issue metadata through `gh` first.
- Link workers to issues and PRs.
- Link warning-only claims to GitHub issues.
- Export or explicitly push claim summaries as issue comments.
- Use `cs issue export/sync/pull` marker blocks to exchange claim state through GitHub issues across machines.
- Post reports or status comments only on explicit commands.
- Add optional labels for running, blocked, and done states.

Exit criteria: an issue can become a worker-backed local task and receive a concise final report without clickops.

## Phase 5: scheduling

- Add persisted schedules owned by `csd`.
- Support run-now, list, disable, and missed-run policy.
- Add concurrency limits.
- Support scheduled GitHub issue queries once issue linkage is stable.

Exit criteria: routine repo hygiene or issue triage agents can run on a schedule without manual babysitting.

## Phase 6: maturity and distribution

- Move from JSON state to SQLite when concurrent daemon writes or query complexity justify it.
- Add release builds for Windows, macOS, and Linux.
- Add service install helpers for Windows service, launchd, and systemd.
- Add config validation once hand-edited config grows beyond simple flags.
- Maintain CI, vulnerability scanning, and small package boundaries.

Exit criteria: the tool can be installed on another machine and used without cloning the repo or running `go run`.
