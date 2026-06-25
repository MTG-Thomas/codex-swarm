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

The current MVP can drive a real `codex app-server` thread and keeps a deterministic mock engine for tests or offline demos.

```powershell
go run ./cmd/cs spawn --engine appserver --repo . --prompt "reply with exactly: codex-swarm-ok"
go run ./cmd/cs spawn --repo . --prompt "isolated mock worker" --worktree
go run ./cmd/cs spawn --repo . --role reviewer --parent <worker-id> --prompt "review this worker"
go run ./cmd/cs spawn --repo . --issue MTG-Thomas/codex-swarm#42 --prompt "work this issue"
go run ./cmd/cs status
go run ./cmd/cs doctor
go run ./cmd/cs doctor --appserver
go run ./cmd/cs send <worker-id> "continue with tests and docs"
go run ./cmd/cs message <from-worker-id> <to-worker-id> "please review this"
go run ./cmd/cs handoff <from-worker-id> <to-worker-id> "ready for review"
go run ./cmd/cs schedule add --repo . --cron "0 8 * * 1" --prompt "weekly repo check"
go run ./cmd/cs schedule list
go run ./cmd/cs resume <worker-id>
go run ./cmd/cs inspect-thread <worker-id>
go run ./cmd/cs show <worker-id>
go run ./cmd/cs report --note "demo completed" <worker-id> done
```

State is written to `.codex-swarm/state.json` by default. Use `--state <path>` or `CODEX_SWARM_STATE` for disposable demos and tests.

`spawn --engine appserver` prints the Codex thread ID and a recovery command. Codex app visibility can lag briefly, especially on mobile; use `inspect-thread` to verify that the stored thread can still be resumed through app-server.

Pass `--worktree` to create a Git branch and worktree for the worker. The worktree path and branch are recorded on the worker and shown in command output.

Pass `--role` and `--parent` to record simple local swarm relationships. Use `message` and `handoff` to write directed communication events into both workers' local timelines without routing routine interagent traffic through MCP.

Pass `--issue owner/repo#123` to link a worker to a GitHub issue. Scheduling is currently a persisted control-plane record only; `schedule add` and `schedule list` do not execute scheduled workers yet.

Use `--engine mock` when the demo needs to avoid live Codex calls:

```powershell
go run ./cmd/cs spawn --engine mock --repo . --prompt "inspect this repo"
```

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
