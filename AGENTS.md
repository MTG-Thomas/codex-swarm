# Agent Instructions

Use this file before meaningful work in this repository.

## Repo Scope

`codex-swarm` is a local-first Go CLI and daemon for orchestrating Codex workers. Keep changes small, operator-readable, cross-platform, and compatible with the repo's declared Go version.

## Required Local Skills

Before editing Go code, read the relevant repo-local skills:

- `.agents/skills/codex-swarm-go-modern/SKILL.md` for language and standard-library choices.
- `.agents/skills/codex-swarm-go-cli-daemon/SKILL.md` for `cmd/cs`, `cmd/csd`, daemon, process, and app-server lifecycle work.
- `.agents/skills/codex-swarm-go-context-errors/SKILL.md` for context, cancellation, process I/O, JSON-RPC, Git, daemon, and operator-facing errors.
- `.agents/skills/codex-swarm-go-testing/SKILL.md` for tests, fakes, integration boundaries, and race-test decisions.
- `.agents/skills/codex-swarm-go-dependency-security/SKILL.md` before adding dependencies, command execution, path handling, GitHub integration, daemon security, or destructive operations.

Do not install upstream skill packs globally for this repo. Use `docs/skills.md` and `skill-bookshelf/manifest.yaml` for provenance.

## Engineering Defaults

- Prefer the Go standard library until a dependency removes real cross-platform risk or stabilizes a durable boundary.
- Keep `main` packages thin; behavior should live in `internal/*`.
- Keep CLI output scriptable. Send normal output to stdout and diagnostics to stderr.
- Keep local daemon APIs narrow and treat them as privileged, even on loopback.
- Preserve worker/thread IDs, repo paths, worktree paths, and issue refs in errors and handoffs.
- Do not mutate GitHub, schedules, worktrees, branches, or Codex sessions unless the command name and user intent are explicit.
- Treat `cs claim` records as warning-only coordination, not hard locks.
- Do not copy broad architecture from mature projects. Borrow small release, CI, service, and operator UX patterns only when `codex-swarm` has reached that complexity.

## Current Architecture Invariants

- The machine-global local state is authoritative. Its compatibility filename may be `state.json`, but current stores are SQLite databases.
- Keep worker engine identity for diagnostics, but make coordination decisions through stable runtime capabilities rather than engine-name checks.
- Claims and conflict messages are warning-only. Git, GitHub, Bifrost, and other target systems retain authority for their own mutations and conflicts.
- `close` is the preferred terminal lifecycle operation because it releases claims and preserves pull-request and completion evidence atomically.
- Keep the daemon loopback-only by default. New mutation routes require narrow inputs, explicit request IDs, idempotent replay, and durable readback.

## Documentation Discipline

- Keep `README.md` as the concise operator entrypoint: purpose, guarantees, installation, core workflows, and safety boundaries.
- Keep `docs/design.md` focused on current architecture and invariants, `docs/maturity.md` on the verified baseline, and `docs/roadmap.md` on unfinished work.
- Treat `docs/superpowers/plans/` as historical implementation plans, not current operating instructions.
- When behavior changes, update the smallest relevant current document and verify every documented command against the CLI or repository source.
- Avoid chronological MVP language, exhaustive command dumps, sales framing, and examples that imply unimplemented behavior.

## Verification

For routine Go edits, run:

```powershell
gofmt -w <changed-go-files>
go test ./...
```

Before pushing or calling work complete, run:

```powershell
$files = gofmt -l .; if ($files) { $files; exit 1 }
go vet ./...
go test ./...
go build -trimpath ./cmd/cs
go build -trimpath ./cmd/csd
```

Run `go test -race ./...` when changing daemon concurrency, worker lifecycle, JSON-RPC multiplexing, store access, goroutines, or cancellation behavior.

Run `govulncheck ./...` when dependencies, GitHub integration, daemon security boundaries, command execution, or path handling change.
