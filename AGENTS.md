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
