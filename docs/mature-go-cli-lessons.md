# Mature Go CLI Lessons

This note records patterns worth borrowing from mature CLI-first Go projects without turning `codex-swarm` into a networking daemon clone.

Reviewed sources:

- NetBird: `netbirdio/netbird`
- Nebula: `slackhq/nebula`

## Borrow Now

- Keep build and verification commands boring and local. Nebula's Makefile keeps explicit `bin`, `test`, `vet`, cross-build, smoke, and release targets; our smaller equivalent should stay in `Makefile` and CI.
- Stamp builds when distribution starts. Both projects carry version/build metadata into binaries. For `codex-swarm`, add `version`, `commit`, and `date` ldflags when release binaries become useful.
- Split CLI command files as commands grow. NetBird's client command package has separate files for `status`, `service`, `up`, `down`, `login`, `version`, and platform-specific files. For us, split `cmd/cs/main.go` once command handlers stop fitting in one readable screen.
- Keep platform-specific behavior isolated. NetBird and Nebula both isolate Windows and Unix differences. For us, service installation, shell quoting, file replacement, and daemon IPC should stay behind small packages.
- Add smoke tests only around durable operator workflows. Nebula has Docker/Vagrant smoke targets because packet flow matters. For us, smoke should mean daemon starts, status responds, a mock worker spawns, and state survives restart.
- Treat release packaging as a later layer. NetBird's GoReleaser setup is powerful but far beyond our current needs. Start with plain GitHub release binaries, then add GoReleaser only when Homebrew/Scoop/packages matter.

## Borrow Later

- Cross-build matrix expansion. Nebula builds many OS/architecture combinations because network agents run everywhere. `codex-swarm` should add macOS CI first, then release cross-builds for Windows, Linux, and macOS.
- Service installers. NetBird's CLI includes service control/install surfaces. `codex-swarm` should add Windows service, launchd, and systemd helpers only after `csd` owns long-running work.
- Pinned GitHub Actions. NetBird pins actions by SHA. This is good for supply-chain maturity, but the repo can defer it until release automation or external users raise the risk.
- Lint on changed files. NetBird's Makefile has fast changed-file lint and slow full lint. We should add `golangci-lint` only when `go vet` stops catching enough or local style drift appears.
- Integration matrices. NetBird tests storage engines and subsystems separately; we should do that only when SQLite/GitHub/Codex integration tests become slow enough to shard.

## Avoid For Now

- Large GoReleaser configs, package repositories, Docker publishing, universal binaries, or distro packages.
- Broad architecture migration to Cobra/Viper or other CLI frameworks before stdlib `flag` becomes a real cost.
- Complex CI cache and dependency installation paths while the repo has no `go.sum` and no third-party dependencies.
- Heavy e2e suites that require live Codex account state by default.

## Adoption Triggers

- Add `version` command and ldflags when publishing the first binary release.
- Add macOS CI when `csd` gains service or IPC behavior.
- Add `go test -race ./...` CI once daemon worker execution becomes concurrent.
- Add GoReleaser when manual GitHub release assets are painful twice.
- Add service install helpers when `csd` can run and resume useful work across restarts.
