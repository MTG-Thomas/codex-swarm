# Go repository maturity baseline

This document records the present verified baseline. Aspirational work belongs
in [roadmap.md](roadmap.md).

## Distribution

- Public MIT-licensed Go module.
- Versioned GitHub releases for Windows, macOS, and Linux on amd64 and arm64.
- `cs` and `csd` version stamping with commit and build date.
- Windows PE product, file-version, company, description, and original-name
  metadata. Executables are not currently Authenticode-signed.
- Native Windows service, macOS LaunchAgent, and Linux systemd install paths.

## Quality and security

- Linux, macOS, and Windows CI.
- Non-mutating repository-wide `gofmt` check.
- `go vet`, `go test`, and trimmed CLI and daemon builds.
- Race testing for daemon, lifecycle, store, message, and cancellation changes.
- `govulncheck` workflow and local dependency verification guidance.
- Standard-library-first dependency policy and reviewed repo-local Go skills.

## Architecture

- Thin `cmd/cs` and `cmd/csd` entrypoints with behavior in `internal/*`.
- SQLite-backed machine-global authority with legacy JSON migration.
- Normalized messages, deliveries, append-only delivery transitions, file
  touches, claims, gates, and events.
- Derived logical-operation grouping plus request-ID-idempotent, evidence-backed
  decision records with atomic supersession history and visible provenance gaps.
- Capability-based runtime behavior independent of engine identity.
- Durable app-server thread and turn identity, including live steering.
- Managed local worktrees and isolated remote Git sessions over SSH.
- Loopback daemon with read-only operational APIs and narrow idempotent
  mutation routes.
- Explicit GitHub issue, pull-request, validation, and Bifrost boundaries.

## Operator experience

- `cs doctor` for prerequisites, state, repository, and daemon health.
- Scriptable status, snapshots, transcripts, and work packets.
- Scriptable human and JSON attention views derived from queued messages,
  blocked claims, stale workers, validator rejections, failed gates, and
  recorded pull-request next actions.
- Scriptable derived operation views that group issue-backed or root-worker
  coordination across workers, claims, messages, gates, recorded pull requests,
  and linked Codex tasks while exposing broken links as unscoped records.
- Warning-only ownership claims, file-touch conflicts, and worker checks.
- Durable direct and subtree messages with JSON/readback evidence, handoffs,
  and parent completion.
- Product-visible live steering for `cs`-owned turns plus a guarded native-host
  bridge for attached Codex tasks, queue fallback, and durable final-agent
  acknowledgement capture.
- Durable metadata-only Codex task discovery plus a host-owned paginated
  collector hook; coordinator heartbeats can stage more than 50 tasks and read
  the resulting status immediately without blocking on child turns.
- Atomic `close` with claim release and pull-request refresh.
- Repository hints and commit-bound validation evidence.
- Read-only stale-state inspection plus explicit janitor application.
- Disposable PowerShell and Bash smoke scripts for isolated mock-ledger checks.

## Maturity constraints

- Scheduling records are persisted but do not yet execute workers.
- Daemon ownership of `cs`-spawned app-server runtime and restart recovery is
  incomplete; externally owned turns require their Codex host to execute the
  native-steering request and confirm it.
- Exact pre-edit intent depends on runtime hooks; file-change evidence is more
  complete than pre-write evidence.
- GitHub synchronization remains explicit and local state remains authoritative.
- Windows binaries have metadata but no production signing chain.
- Remote worktree cleanup is intentionally not automatic.

## Complexity triggers

Add a new dependency or subsystem only when at least one of these is proven:

- hand-written cross-platform code creates operational risk;
- a normalized query or transaction cannot remain clear in the current store;
- typed external API behavior is safer than the existing executable boundary;
- daemon recovery needs a durable process-ownership abstraction;
- command routing or help maintenance is materially constrained by `flag`; or
- tests need a stable fake boundary that current package seams cannot provide.

See [mature-go-cli-lessons.md](mature-go-cli-lessons.md) for the current
borrow/defer/avoid review of nearby Go projects.
