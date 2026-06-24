# Go repo maturity baseline

This repo should mature in layers. Each layer should make the operator experience more reliable without turning the project into a framework.

## Present baseline

- public GitHub repository
- MIT license
- `go fmt`, `go vet`, `go test`, and build checks
- Windows and Linux CI
- explicit dependency policy
- package boundaries for app-server, daemon, store, worktree, config, and GitHub

## Near-term additions

- table-driven tests for command parsing and app-server JSON-RPC envelopes
- file-backed or SQLite-backed store once worker lifecycle exists
- structured logs once `csd` runs longer than a one-shot command
- integration test with a fake app-server process before driving real Codex
- release workflow once binaries are useful outside local development

## Complexity triggers

Add dependencies or infrastructure only when one of these is true:

- hand-written cross-platform code is becoming risky
- state must survive process or machine restarts
- operator-facing config needs validation and comments
- GitHub issue/PR flows need first-class API behavior
- daemon installation needs platform-specific service management
- tests need fakes or fixtures to keep app-server behavior deterministic

## Local prior art

When comparing nearby Go tools, prefer borrowing:

- command naming and help text conventions
- CI and release workflow shape
- config file locations
- service install/uninstall behavior
- logging and error presentation

Do not copy broad architecture unless it directly helps the Codex app-server wrapper.
