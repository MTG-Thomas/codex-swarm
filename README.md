# codex-swarm

`codex-swarm` is a thin local orchestration layer for Codex. It is intended to own the small amount of state around projects, workers, Codex app-server threads, Git worktrees, and issue-linked tasks without adopting a heavy orchestrator runtime.

[![ci](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml/badge.svg)](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml)

The first implementation target is deliberately narrow:

- wrap `codex app-server` over local JSON-RPC
- track workers, thread IDs, worktree paths, and task status
- expose a small CLI for spawn, send, resume, report, and status
- leave GitHub, scheduling, and daemon service installation behind explicit package boundaries

## Commands

Planned binaries:

- `cs`: operator CLI
- `csd`: local daemon/service process

Current scaffold:

```powershell
go test ./...
go run ./cmd/cs status
go run ./cmd/csd
```

## Friend-demo MVP

The current MVP uses a deterministic mock worker so the operator flow is easy to demo without depending on a live Codex app-server session.

```powershell
go run ./cmd/cs spawn --repo . --prompt "inspect this repo and suggest the next useful slice"
go run ./cmd/cs status
go run ./cmd/cs send <worker-id> "continue with tests and docs"
go run ./cmd/cs show <worker-id>
go run ./cmd/cs report --note "demo completed" <worker-id> done
```

State is written to `.codex-swarm/state.json` by default. Use `--state <path>` or `CODEX_SWARM_STATE` for disposable demos and tests.

Local maturity checks:

```powershell
go fmt ./...
test -z "$(gofmt -l .)" # bash/sh
go vet ./...
go test ./...
go build -trimpath ./cmd/cs
go build -trimpath ./cmd/csd
```

## Complexity Budget

Dependencies should be added when they remove cross-platform risk or stabilize a durable boundary:

- SQLite: when worker/thread state must survive real daemon restarts
- config parser: when JSON is too clumsy for hand-edited operator config
- GitHub client: when `gh` shelling becomes hard to test or too slow
- service helper: when installing as Windows service, launchd agent, or systemd unit
- CLI framework: when command parsing starts hiding real behavior in `flag` boilerplate

Until then, prefer standard library code and narrow interfaces.
