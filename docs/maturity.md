# Go repo maturity baseline

This repo should mature in layers. Each layer should make the operator experience more reliable without turning the project into a framework.

## Present baseline

- public GitHub repository
- MIT license
- `go fmt`, `go vet`, `go test`, and build checks
- non-mutating `gofmt` check in CI
- `-trimpath` builds
- Windows and Linux CI
- `govulncheck` workflow
- explicit dependency policy
- package boundaries for app-server, daemon, store, worktree, config, and GitHub
- disposable PowerShell and Bash friend-demo smoke scripts for the local mock swarm flow

## Near-term additions

- table-driven tests for command parsing and app-server JSON-RPC envelopes
- file-backed or SQLite-backed store once worker lifecycle exists
- structured logs once `csd` runs longer than a one-shot command
- integration test with a fake app-server process before driving real Codex
- release workflow once binaries are useful outside local development
- optional Makefile smoke target once version metadata or real daemon startup exists
- repo-local agent entrypoint in `AGENTS.md`
- version metadata command before the first binary release
- macOS CI once daemon service or IPC behavior appears

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

- `discordo`: Makefile targets for `fmt-check`, `test`, `build`, `smoke`, `lint`, and `vulncheck`; use `-trimpath` for builds.
- `tickgit`: compact OS-matrix CI plus GoReleaser path once distribution matters.
- `terraform-provider-definednetworking`: minimal single-purpose module shape.
- `netbirdio/netbird`: CLI/daemon service command boundaries, pinned CI/release maturity, and command-file decomposition once command handlers grow.
- `slackhq/nebula`: explicit Makefile targets, version stamping, cross-build discipline, and smoke-test layering.

Do not copy broad architecture unless it directly helps the Codex app-server wrapper.

See `docs/mature-go-cli-lessons.md` for current borrow/defer/avoid guidance.
