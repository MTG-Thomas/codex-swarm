# codex-swarm

`codex-swarm` is a thin local orchestration layer for Codex. It is intended to own the small amount of state around projects, workers, Codex app-server threads, Git worktrees, and issue-linked tasks without adopting a heavy orchestrator runtime.

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

## Complexity Budget

Dependencies should be added when they remove cross-platform risk or stabilize a durable boundary:

- SQLite: when worker/thread state must survive real daemon restarts
- config parser: when JSON is too clumsy for hand-edited operator config
- GitHub client: when `gh` shelling becomes hard to test or too slow
- service helper: when installing as Windows service, launchd agent, or systemd unit
- CLI framework: when command parsing starts hiding real behavior in `flag` boilerplate

Until then, prefer standard library code and narrow interfaces.
