# codex-swarm

`codex-swarm` is a thin local orchestration layer for Codex. It is intended to own the small amount of state around projects, workers, Codex app-server threads, Git worktrees, and issue-linked tasks without adopting a heavy orchestrator runtime.

[![ci](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml/badge.svg)](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml)

The first implementation target is deliberately narrow:

- wrap `codex app-server` over local JSON-RPC
- track workers, thread IDs, worktree paths, and task status
- expose a small CLI for spawn, send, resume, report, and status
- keep GitHub, scheduling, and daemon service installation behind explicit commands and package boundaries

CI verifies Linux, macOS, and Windows for the CLI and daemon binaries. Windows release executables include product, file-version, company, description, and original-filename metadata in their PE resources; they are not currently Authenticode-signed.

## Commands

Planned binaries:

- `cs`: operator CLI
- `csd`: local daemon/service process

Current scaffold:

```powershell
go test ./...
go run ./cmd/cs status
go run ./cmd/cs status --issues
go run ./cmd/cs status --daemon http://127.0.0.1:8787
go run ./cmd/cs version
go run ./cmd/csd
go run ./cmd/csd version
```

## Friend-demo MVP

The current MVP can drive a real `codex app-server` thread and keeps a deterministic mock engine for tests or offline demos.

App-server workers can also use isolated Git branches on an SSH host. The
remote host owns Git and Codex authentication; swarm stores only routing and
workspace identity:

```powershell
cs spawn --engine appserver --repo . --worktree `
  --remote-host user@remote-host --remote-jump user@jump-host `
  --remote-repo-url git@github.com:owner/repo.git `
  --remote-codex /home/user/.local/bin/codex `
  --prompt "Implement, commit, and push the assigned branch."
```

See [docs/remote-sessions.md](docs/remote-sessions.md) for the Git/Bifrost
boundary and prerequisites.

```powershell
go run ./cmd/cs spawn --engine appserver --repo . --prompt "reply with exactly: codex-swarm-ok"
go run ./cmd/cs spawn --repo . --prompt "isolated mock worker" --worktree
go run ./cmd/cs spawn --repo . --role reviewer --parent <worker-id> --prompt "review this worker"
go run ./cmd/cs spawn --repo . --issue MTG-Thomas/codex-swarm#42 --prompt "work this issue"
go run ./cmd/cs status
go run ./cmd/cs status --issues
go run ./cmd/cs doctor
go run ./cmd/cs doctor --appserver
go run ./cmd/cs send <worker-id> "continue with tests and docs"
go run ./cmd/cs message <from-worker-id> <to-worker-id> "please review this"
go run ./cmd/cs message --subtree <from-worker-id> <root-worker-id> "coordinate this subtree"
go run ./cmd/cs inbox --queued <worker-id>
go run ./cmd/cs touch --worker <worker-id> --repo . --path internal/store/store.go --intent "edit store"
go run ./cmd/cs handoff <from-worker-id> <to-worker-id> "ready for review"
go run ./cmd/cs trace start "Release swarm" --key release-swarm
go run ./cmd/cs trace into "Watch CI" --key watch-ci
go run ./cmd/cs trace done "CI passed"
go run ./cmd/cs trace status
go run ./cmd/cs claim create --repo . --scope internal/store --worker <worker-id> --issue MTG-Thomas/codex-swarm#42 --note "editing store claims"
go run ./cmd/cs claim conflicts --repo . --scope internal/store/json.go
go run ./cmd/cs claim export --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs gate list --repo .
go run ./cmd/cs gate record --repo . --worker <worker-id> --gate test --exit-code 0 --output "go test ./... passed"
go run ./cmd/cs validate start --repo . --issue MTG-Thomas/codex-swarm#42 --prompt "implement issue #42" --gate test
go run ./cmd/cs issue export --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs issue ready --issue MTG-Thomas/codex-swarm#42 --repo .
go run ./cmd/cs issue dispatch --issue MTG-Thomas/codex-swarm#42 --repo . --prompt "implement issue #42" --gate test
go run ./cmd/cs issue sync --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs issue pull --issue MTG-Thomas/codex-swarm#42
go run ./cmd/cs issue report --issue MTG-Thomas/codex-swarm#42 --worker <worker-id>
go run ./cmd/cs pr attach --worker <worker-id> --url https://github.com/MTG-Thomas/codex-swarm/pull/42
go run ./cmd/cs pr status <worker-id>
go run ./cmd/cs agent register --name "codex-thread" --role implementer
go run ./cmd/cs legacy import-coordinator
go run ./cmd/cs janitor stale
go run ./cmd/cs janitor release --apply --note "stale cleanup"
go run ./cmd/cs schedule add --repo . --cron "0 8 * * 1" --prompt "weekly repo check"
go run ./cmd/cs schedule list
go run ./cmd/cs repo hints --repo .
go run ./cmd/cs resume <worker-id>
go run ./cmd/cs inspect-thread <worker-id>
go run ./cmd/cs show <worker-id>
go run ./cmd/cs show --snapshot <worker-id>
go run ./cmd/cs show --snapshot --json <worker-id>
go run ./cmd/cs attach --repo . --thread <thread-id> --prompt "track this task"
go run ./cmd/cs report --note "demo completed" <worker-id> done
go run ./cmd/cs close --note "verified and complete" <worker-id>
```

State is written to a machine-global user config path by default, for example `%AppData%\codex-swarm\state.json` on Windows. The compatibility path keeps its historical name, but current state files are SQLite databases; a migrated legacy JSON file is retained with a `.legacy.json` suffix. Use `--state <path>` or `CODEX_SWARM_STATE` for disposable demos and tests.

`spawn --engine appserver` prints the Codex thread ID and a recovery command. When the worker is linked to an issue, the initial app-server turn receives a concise `ISSUE_LAUNCH_BUNDLE` with issue metadata, repo/worktree/branch context, active issue claims, repo hints, required verification commands, and explicit forbidden actions. Non-issue app-server spawns keep the raw prompt. Codex app visibility can lag briefly, especially on mobile; use `inspect-thread` to verify that the stored thread can still be resumed through app-server.

Use `attach --repo <path> --thread <thread-id> --prompt <summary>` to bring an already-running Codex task into the ledger without inventing a worktree or branch. The default `tracker` engine records identity and queued communication; use `--worker <id> --engine appserver --turn <turn-id>` when attaching an active app-server turn that can receive live steering. Engine identity remains visible, but runtime behavior is projected through stable capabilities: `live_message`, `resume`, `managed_worktree`, `automatic_completion`, and `external_tracker`.

Use `show --snapshot <worker-id>` to print a compact deterministic worker state snapshot for resume, validation, and handoff context. Add `--json` for the parseable `codex-swarm.worker-snapshot.v1` form. Snapshots include their real generation time, show worktree data only after managed worktree creation, and include only gate evidence recorded for that worker. App-server `send` turns include the same snapshot before the user message so resumed Codex threads get current local state without replaying the full timeline.

App-server runs use the normal `turn/completed` JSON-RPC event as their completion record. The internal completion policy also supports a separate text completion signal for shell-agent style runners: after that signal appears, `cs` waits briefly for trailing turn metadata and records a warning instead of failing the worker if finalization never arrives. No extra app-server completion flags are exposed while the default signal is empty.

Pass `--worktree` to create a Git branch and worktree for the worker. Managed branch names use the worker timestamp plus a random suffix, and the worktree path and branch are recorded on the worker and shown in command output.

Managed worktree creation uses repo-local branch locks under `.codex-swarm/locks/`. A live lock fails fast instead of handing two workers the same managed checkout; a stale lock whose PID is gone is pruned. If the intended managed worktree already exists on the requested branch, it is reused. Dirty managed worktrees are reused without refresh and print a warning so local changes are preserved. If the branch is checked out in the main repository or an external worktree, `spawn --worktree` fails with that location instead of reusing it.

Pass `--role` and `--parent` to record simple local swarm relationships. The communication vocabulary is deliberately small: `message` sends a DM, `message --subtree` reaches a worker and its descendants, and the system creates conflict warnings and child completion reports. `inbox` reads durable per-recipient delivery state. When a recipient has an active app-server turn, its existing connection polls the SQLite queue and uses `turn/steer`; otherwise the message stays queued and is injected into the next `send` turn. `handoff` remains the explicit ownership-transfer record.

Use `touch --worker <id> --repo <root> --path <file>` to record a read or write intent. Overlapping writes by different workers in the same file and line range create bilateral `conflict` messages. These are warnings only: the touch is always recorded and no edit, command, or Git operation is rejected. App-server `fileChange` item events are recorded automatically after a turn.

When a child worker with `--parent` is reported `done` or `failed`, `report` automatically enqueues a `completion` message for the parent. The report mutation succeeds only with durable forwarding readback; reusing `--request-id` is idempotent. Prefer `close` for terminal operator closeout: it atomically marks the worker terminal, releases every active claim, records refreshed attached-PR state, clears blocker/next fields, and forwards the parent completion. `report` remains the lower-level lifecycle mutation when claims must stay open.

Use `trace start`, `trace into`, `trace log`, `trace done`, `trace back`, and `trace status` for a lightweight nested execution stack per local agent. `--key` makes `start` and `into` idempotent across retries. Agent lanes default to `CODEX_SWARM_TRACE_AGENT`, then `DETOUR_AGENT`, then `default`; pass `--agent` when multiple local threads need separate stacks. `trace merge` prints active lanes as a handoff snapshot without changing state.

Pass `--issue owner/repo#123` to link a worker to a GitHub issue. Scheduling is currently a persisted control-plane record only; `schedule add` and `schedule list` do not execute scheduled workers yet.

Use `claim create`, `claim list`, `claim conflicts`, `claim show`, `claim block`, and `claim release` for warning-only coordination claims. Scopes are typed as `path`, `task`, or `live`: path and live-resource scopes overlap hierarchically within their own kind, while task scopes match exactly. Repeat `--scope` to claim several resources atomically; comma-packed scopes are rejected so resource boundaries stay unambiguous. Use `claim export --issue owner/repo#123` to print GitHub-ready claim markdown. Use `claim push --issue owner/repo#123` only when you intentionally want to post the current local claim summary as a GitHub issue comment through `gh`.

Use `issue export --issue owner/repo#123` to include a hidden `codex-swarm:claims:v1` JSON marker that other machines can parse. Use `issue sync --issue owner/repo#123` only when you intentionally want to create or update that marker comment through `gh`. Use `issue pull --issue owner/repo#123` to import the latest marker-backed claim set from GitHub into local state; by default it skips remote claims older than a local claim with the same ID. Use `issue pull --force --issue owner/repo#123` only when the issue marker should overwrite newer local claim state.

Use `issue ready --issue owner/repo#123 --repo <path>` to run a read-only
dispatch preflight. It reads the issue title/body, local issue-linked claims,
and repo quality gates from `codex-swarm.hints.json`, then prints a scriptable
`ready=<bool>` summary plus blockers. Add `--json` for a parseable readiness
report. This command does not mutate GitHub or create workers. By default,
GitHub issue metadata is read through `gh`. For daemon/service contexts where
`gh` is not authenticated, configure GitHub App auth with
`CODEX_SWARM_GITHUB_APP_ID` or `CODEX_SWARM_GITHUB_APP_CLIENT_ID` plus
`CODEX_SWARM_GITHUB_APP_PRIVATE_KEY_FILE`, or use
`CODEX_SWARM_GITHUB_APP_PRIVATE_KEY` when an inline PEM value is unavoidable.
`CODEX_SWARM_GITHUB_APP_INSTALLATION_ID` is optional; if omitted,
`codex-swarm` discovers the installation for the target repository.
`CODEX_SWARM_GITHUB_API_URL` overrides `https://api.github.com` for GitHub
Enterprise Server. Set `CODEX_SWARM_DAEMON_URL=http://127.0.0.1:8787` or pass
`--daemon http://127.0.0.1:8787` to have the CLI ask a running daemon for the
same readiness report; daemon errors are returned directly instead of falling
back to local mode.

Use `issue dispatch --issue owner/repo#123 --repo <path> --prompt <task> --gate <id>` to run the same readiness preflight and, only when ready, create a local implementer/validator pair. Dispatch is explicit and local-only: it does not post to GitHub or schedule future work. The request key is derived from issue, repo, prompt, and gate IDs; rerunning the same request prints the original worker IDs with `replayed=true` instead of creating duplicates.

Set `CODEX_SWARM_DAEMON_URL=http://127.0.0.1:8787` or pass `--daemon http://127.0.0.1:8787` to have `issue dispatch` perform the same explicit mutation through the daemon. Daemon dispatch requires loopback access and an idempotent request ID derived by the CLI.

Use `issue report --issue owner/repo#123 --worker <worker-id>` only when you intentionally want to post that worker's current report or last message as a GitHub issue comment. When the worker's repo advertises quality gates, `issue report` fails closed unless the local proof cache has fresh successful evidence for each gate. Cached proof is reusable only when gate id, repo path, configured command, current HEAD, and worker freshness match. It does not run gate commands for you; refresh evidence with `cs gate record --repo <path> --worker <worker-id> --gate <id> --exit-code <code> --output <summary>`. Use `--bypass-gates` only for an intentional exception; the command prints `bypassed_gates=true` before mutating GitHub.

Use `pr attach --worker <worker-id> --url <pr-url>` to explicitly link a pull request to a worker. Use `pr status <worker-id>` to refresh that PR through `gh pr view`, store the latest state on the worker, append a timeline event, and print a compact handoff summary with PR state, base/head branch, check counts, review decision, CodeRabbit status, and next action. It never merges or mutates the PR. Open-PR next actions include `wait`, `fix-review`, `fix-ci`, `merge-ready`, and `blocked`; merged and closed PRs report `complete` and `closed`, and an unknown state reports `unknown`. CodeRabbit is reported separately as advisory automation instead of inflating required check counts.

Use `validate start --issue owner/repo#123 --prompt <task> --gate <id>` to
create an issue-linked implementer and validator pair. The validator gets fresh
issue, repo, worktree, branch, and gate context without inheriting the
implementer's transcript. Use `cs gate record` to attach proof to the validator,
then `cs report --note "approved: ..." <validator> done` or
`cs report --note "rejected: ..." <validator> failed` to make the outcome
visible locally and in later `issue report` output.

Use `repo hints --repo <path>` to print opt-in execution guidance advertised by a repository. `cs` checks `codex-swarm.hints.json` first for committed project guidance, then `.codex-swarm/repo-hints.json` for local-only guidance. When hints exist, `spawn` prints the same advisory lines so agents see preferred execution surfaces before starting work. Hints are advisory only; they do not block local execution or inject secrets.

Example committed hint file:

```json
{
  "remote_devcontainer": {
    "command": "just talos-dev-run \"just --list\"",
    "image": "ghcr.io/mtg-thomas/bifrost-devcontainer:devcontainer-main-172fb07bd73f",
    "docs": "docs/devcontainer.md",
    "note": "No secrets are injected by default."
  }
}
```

Generic command hints can be used when a repo has a preferred local or live-ops
entry point but no remote devcontainer lane:

```json
{
  "commands": [
    {
      "name": "scoped sync",
      "command": "pwsh -NoProfile -File .\\scripts\\bifrost-local-sync.ps1 <scoped-authored-path>",
      "docs": "docs/FIRST_30_MINUTES.md",
      "note": "Use scoped sync after authored workspace edits."
    }
  ]
}
```

Quality gates define repo-owned verification commands that agents can reference
and record as local proof. `gate list` reads these definitions; `gate record`
stores observed evidence and appends a `quality.gate` event to the worker
timeline. Evidence includes timestamp, exit code, output summary, commit, and
provenance worker id so later commands can reuse fresh local proof without
rerunning expensive checks. Recording evidence does not execute the command yet.

```json
{
  "quality_gates": [
    {
      "id": "test",
      "command": "go test ./...",
      "scope": "repo",
      "description": "unit test suite"
    }
  ]
}
```

For proof-sensitive Talos/ARC remote execution, prefer immutable image tags over mutable tags such as `latest`, `main`, or `devcontainer-main`.

Use `agent register --name <name> --role <role>` to record the current local agent identity. Use `legacy import-coordinator` once per machine, or with `--include-expired` for audit work, to import active warning-only claims from the old PowerShell coordinator.

Set `CODEX_SWARM_DAEMON_URL=http://127.0.0.1:8787` or pass `cs status --daemon http://127.0.0.1:8787` to make `cs status` prefer a running daemon. `cs status` defaults to workers updated in the last 24 hours plus older non-terminal work; use `--since`, `--repo`, `--status`, and `--limit` to narrow it, `--all` for historical output, or `--json` for automation. Local and daemon-backed status report coordination coverage by stable runtime capability rather than engine-specific fields, plus current steerability, active claims, message delivery state, recent file touches, and conflict messages. Each worker row retains engine identity and exposes its derived capability list in JSON.

Use `cs status --issues` for a compact read-only operations dashboard over local state. It summarizes issue-linked non-terminal workers, active claims, workers stale for more than 24 hours, and suggested next actions. By default it suppresses lower-priority fresh idle rows; add `--detail` to print every active issue-linked worker.

Use `cs janitor stale` for a read-only stale worker and releasable claim report. Use `cs janitor release --apply` to release only active claims that are expired, attached to a missing or terminal worker, or attached to a worker stale beyond the `--older` threshold. Without `--apply`, release is a dry run.

`csd serve` starts the daemon, `csd status` checks it, and `csd install` / `csd uninstall` install or remove the daemon service on supported platforms. `cs doctor` also probes the configured or default loopback daemon and distinguishes a healthy service from an absent optional service. Windows uses a service named `codex-swarm-daemon`; macOS installs a per-user LaunchAgent at `~/Library/LaunchAgents/codex-swarm-daemon.plist`; Linux installs a systemd unit at `/etc/systemd/system/codex-swarm-daemon.service` and should be run with sufficient privilege, for example through `sudo`. The daemon exposes read-only HTTP status surfaces with `GET /status`, `GET /workers`, `GET /claims`, `GET /v1/messages?worker=<id>`, and `GET /readiness?issue=owner/repo%23123&repo=<path>`. Loopback-only, idempotent mutation routes are `POST /dispatch`, `POST /v1/messages`, `POST /v1/touches`, and `POST /v1/completions`.

Use `--engine mock` when the demo needs to avoid live Codex calls:

```powershell
go run ./cmd/cs spawn --engine mock --repo . --prompt "inspect this repo"
```

Run the local friend-demo smoke script when you want a disposable end-to-end walkthrough without touching machine-global swarm state or GitHub:

```powershell
.\scripts\demo-swarm.ps1
```

```bash
./scripts/demo-swarm.sh
```

The demo registers a coordinator identity, creates a coordinator plus two mock workers, links one claim to `MTG-Thomas/codex-swarm#9`, sends a worker-to-worker message, creates one managed mock worktree, prints `cs status`, and then removes only its temporary state file and managed demo worktree/branch.

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

- SQLite schema changes: when normalized queries or transactional coordination justify a migration beyond the current messages, deliveries, file touches, and compatibility records
- config parser: when JSON is too clumsy for hand-edited operator config
- GitHub client: when `gh` shelling becomes hard to test or too slow
- service helper: when installing as Windows service, launchd agent, or systemd unit
- CLI framework: when command parsing starts hiding real behavior in `flag` boilerplate

Until then, prefer standard library code and narrow interfaces.
