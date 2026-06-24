---
name: codex-swarm-go-cli-daemon
description: Use when implementing or reviewing codex-swarm Go CLI commands, daemon/service behavior, local process boundaries, stdout/stderr behavior, signal handling, configuration, or app-server worker lifecycle code.
---

# codex-swarm Go CLI and Daemon

Keep `cs` and `csd` small, predictable, and scriptable. Favor explicit behavior over framework ceremony until command parsing or service lifecycle complexity justifies a dependency.

## CLI

- Print machine-parseable status where practical.
- Send normal command output to stdout and diagnostics/errors to stderr.
- Return stable exit codes for operator workflows.
- Keep `main` thin: parse, call package code, translate errors to process output.
- Add CLI frameworks only when stdlib `flag` is hiding behavior or making tests hard.

## Daemon

- Own process lifecycle with `context.Context`, `signal.NotifyContext`, and explicit cancellation.
- Keep Codex app-server process management behind `internal/appserver`.
- Keep daemon API transport separate from worker/task state.
- Prefer `log/slog` before adding logging dependencies.
- Design shutdown so interrupted workers can be read back, resumed, or reported clearly.

## Verification

Run the narrow command plus:

```text
go vet ./...
go test ./...
go build -trimpath ./cmd/cs
go build -trimpath ./cmd/csd
```

Use `go test -race ./...` when editing daemon concurrency.

## Source Inspiration

Adapted from `samber/cc-skills-golang` CLI, project-layout, context, and observability guidance, with dependency-promoting recommendations removed.
