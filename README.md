# codex-swarm

`codex-swarm` is a thin local orchestration layer for Codex. It is intended to own the small amount of state around projects, workers, Codex app-server threads, Git worktrees, and issue-linked tasks without adopting a heavy orchestrator runtime.

[![ci](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml/badge.svg)](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml)

The first implementation target is deliberately narrow:

- wrap `codex app-server` over local JSON-RPC
- track workers, thread IDs, worktree paths, and task status
- expose a small CLI for spawn, send, resume, report, and status
- keep GitHub, scheduling, and daemon service installation behind explicit commands and package boundaries

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
go run ./cmd/cs claim create --repo . --scope internal/store --worker <worker-id> --issue MTG-Thomas/codex-swarm#42 --note "editing store claims"
go run ./cmd/cs claim conflicts --repo . --scope internal/store/json.go
go run ./cmd/cs claim export --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs issue export --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs issue sync --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs issue pull --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs issue report --issue MTG-Thomas/codex-swarm#42 --worker <worker-id>
go run ./cmd/cs agent register --name "codex-thread" --role implementer
go run ./cmd/cs legacy import-coordinator
go run ./cmd/cs schedule add --repo . --cron "0 8 * * 1" --prompt "weekly repo check"
go run ./cmd/cs schedule list
go run ./cmd/cs resume <worker-id>
go run ./cmd/cs inspect-thread <worker-id>
go run ./cmd/cs show <worker-id>
go run ./cmd/cs report --note "demo completed" <worker-id> done
```

State is written to a machine-global user config path by default, for example `%AppData%\codex-swarm\state.json` on Windows. Use `--state <path>` or `CODEX_SWARM_STATE` for disposable demos and tests.

`spawn --engine appserver` prints the Codex thread ID and a recovery command. Codex app visibility can lag briefly, especially on mobile; use `inspect-thread` to verify that the stored thread can still be resumed through app-server.

App-server runs use the normal `turn/completed` JSON-RPC event as their completion record. The internal completion policy also supports a separate text completion signal for shell-agent style runners: after that signal appears, `cs` waits briefly for trailing turn metadata and records a warning instead of failing the worker if finalization never arrives. No extra app-server completion flags are exposed while the default signal is empty.

Pass `--worktree` to create a Git branch and worktree for the worker. Managed branch names use the worker timestamp plus a random suffix, and the worktree path and branch are recorded on the worker and shown in command output.

Managed worktree creation uses repo-local branch locks under `.codex-swarm/locks/`. A live lock fails fast instead of handing two workers the same managed checkout; a stale lock whose PID is gone is pruned. If the intended managed worktree already exists on the requested branch, it is reused. Dirty managed worktrees are reused without refresh and print a warning so local changes are preserved. If the branch is checked out in the main repository or an external worktree, `spawn --worktree` fails with that location instead of reusing it.

Pass `--role` and `--parent` to record simple local swarm relationships. Use `message` and `handoff` to write directed communication events into both workers' local timelines without routing routine interagent traffic through MCP.

Pass `--issue owner/repo#123` to link a worker to a GitHub issue. Scheduling is currently a persisted control-plane record only; `schedule add` and `schedule list` do not execute scheduled workers yet.

Use `claim create`, `claim list`, `claim conflicts`, `claim show`, `claim block`, and `claim release` for warning-only coordination claims. Use `claim export --issue owner/repo#123` to print GitHub-ready claim markdown. Use `claim push --issue owner/repo#123` only when you intentionally want to post the current local claim summary as a GitHub issue comment through `gh`.

Use `issue export --issue owner/repo#123` to include a hidden `codex-swarm:claims:v1` JSON marker that other machines can parse. Use `issue sync --issue owner/repo#123` only when you intentionally want to create or update that marker comment through `gh`. Use `issue pull --issue owner/repo#123` to import the latest marker-backed claim set from GitHub into local state; by default it skips remote claims older than a local claim with the same ID. Use `issue pull --force --issue owner/repo#123` only when the issue marker should overwrite newer local claim state.

Use `issue report --issue owner/repo#123 --worker <worker-id>` only when you intentionally want to post that worker's current report or last message as a GitHub issue comment.

Use `agent register --name <name> --role <role>` to record the current local agent identity. Use `legacy import-coordinator` once per machine, or with `--include-expired` for audit work, to import active warning-only claims from the old PowerShell coordinator.

Set `CODEX_SWARM_DAEMON_URL=http://127.0.0.1:8787` to make `cs status` prefer a running daemon. `csd serve` starts the daemon, `csd status` checks it, and `csd install` / `csd uninstall` are explicit service-manager stubs until platform-specific installers are added.

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
