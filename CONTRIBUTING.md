# Contributing

`codex-swarm` is early-stage software. Keep changes small, observable, and easy to unwind.

## Local checks

Run these before pushing:

```powershell
go fmt ./...
go vet ./...
go test ./...
go build ./cmd/cs
go build ./cmd/csd
```

If `make` is available:

```sh
make check build
```

## Dependency policy

Prefer the Go standard library until a dependency clearly reduces operational risk or maintenance cost. Good reasons to add one:

- durable state: SQLite or another embedded store once daemon resume is real
- human-edited config: YAML/TOML once JSON becomes hostile
- GitHub API work: a maintained client once shelling to `gh` is not enough
- service install: platform helpers once `csd` needs install/uninstall commands
- CLI complexity: a command framework once `flag` boilerplate hides behavior

Every new dependency should include:

- why it is needed now
- what code it replaces
- whether it works on Windows, macOS, and Linux
- how it is verified in CI

## Design bias

- Keep `internal/appserver` as the narrow Codex JSON-RPC boundary.
- Keep daemon transport separate from worker/task state.
- Keep GitHub and scheduler behavior optional at the edges.
- Avoid MCP for routine local worker coordination unless a specific integration requires it.
