# codex-swarm

`codex-swarm` is a local coordination layer for Codex work. It keeps worker
identity, ownership claims, messages, handoffs, validation evidence, GitHub
links, and task history in one machine-local SQLite ledger so parallel agents
can detect overlap before Git becomes the only place conflicts are visible.

[![ci](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml/badge.svg)](https://github.com/MTG-Thomas/codex-swarm/actions/workflows/ci.yml)

It does not replace Codex, Git, GitHub, or the systems being operated. It makes
their work inspectable and gives agents a small, durable coordination protocol.

## Why it exists

Several Codex tasks can share a repository, an issue queue, or a live system
without sharing a conversation. Git records the eventual source changes, but
it cannot answer important questions while work is in progress:

- Who is already working on this file, issue, or live resource?
- Can this worker receive a message now, or should it be queued?
- What thread, worktree, branch, issue, and pull request belong to this task?
- Which checks actually ran, against which commit?
- What should another agent know before resuming the work?
- Did closeout release the task's claims and preserve its final evidence?

`codex-swarm` records those answers locally. Claims and conflict notices are
warnings, not locks: they make overlap visible without turning coordination
state into a second permission system.

## Operating model

```text
Codex tasks and other workers
            |
          cs CLI
            |
     SQLite coordination ledger
            |
          csd daemon
       /       |        \
 messages   status API   Codex app-server
```

The `cs` CLI is the normal operator surface. `csd` keeps an optional loopback
service available for status, durable message delivery, file-touch events,
completion forwarding, readiness, and explicit idempotent dispatch.

The machine-global ledger is authoritative. Its compatibility filename is
`state.json`, but current state files are SQLite databases. On Windows the
default is `%AppData%\codex-swarm\state.json`. Use `--state` or
`CODEX_SWARM_STATE` only when an intentionally isolated ledger is required.

Every worker retains its engine identity, while runtime behavior is described
by stable capabilities:

- `live_message`: an active turn can receive steering immediately
- `resume`: the recorded execution can be resumed
- `managed_worktree`: swarm owns an isolated Git worktree for the worker
- `automatic_completion`: completion can be recorded by the runtime
- `external_tracker`: execution is owned by another system

This keeps coordination logic independent of any one agent runtime.

## Install

Download the archive for your operating system from the
[latest GitHub release](https://github.com/MTG-Thomas/codex-swarm/releases/latest),
extract `cs` and `csd`, and place them on `PATH`.

To build from source:

```powershell
go build -trimpath -o cs.exe ./cmd/cs
go build -trimpath -o csd.exe ./cmd/csd
```

Confirm the installation before relying on it:

```powershell
cs version
cs doctor
```

Release archives are published for Windows, macOS, and Linux on amd64 and
arm64. Windows executables include product and file-version metadata. They are
not currently Authenticode-signed.

## Start coordinating work

### Track an existing Codex task

Use `attach` when a Codex task already exists and should join the ledger:

```powershell
cs attach --repo . --thread <thread-id> --prompt "Refresh repository documentation"
```

The default tracker records identity and queued communication without claiming
that swarm owns the runtime. When an existing Codex task has an active turn,
attach its thread and turn IDs explicitly so `cs message` can use Codex-native
same-turn steering:

```powershell
cs attach --worker <worker-id> --engine appserver `
  --thread <thread-id> --turn <turn-id> --host-id <host-id>
```

### Start a worker

Use the deterministic mock engine for coordination tests and the app-server
engine for a real Codex thread:

```powershell
cs spawn --repo . --role reviewer --prompt "Review the current change"

cs spawn --engine appserver --repo . --worktree `
  --prompt "Implement the bounded change and run the repository checks"
```

`--worktree` creates and records an isolated branch and worktree. Conversation
isolation alone does not isolate filesystem writes.

### Claim scope before editing

```powershell
cs claim create --repo . --scope README.md --worker <worker-id> `
  --note "Rewrite operator documentation"

cs claim conflicts --repo . --scope README.md
```

Scopes can represent repository paths, tasks, or live resources. Conflicts are
reported, but they never reject an edit or Git operation.

### Communicate and hand off

```powershell
cs message --wait 5s <from-worker-id> <to-worker-id> "README is ready for review"
cs inbox <worker-id>
cs inbox --json <worker-id>
cs handoff <from-worker-id> <to-worker-id> "Checks pass; review the release notes"
cs workpacket --worker <worker-id>
cs transcript <worker-id>
```

Messages are stored before delivery. A worker owned by `cs` polls that durable
queue over its existing app-server connection. An active task owned by Codex
Desktop or another Codex host instead produces a `native_steering` request in
`cs message --json`, containing the ledger path, host, thread, turn, exact
prompt, and delivery ID. The owning Codex host injects that prompt with its native task
message tool, then records verified readback:

```powershell
cs message confirm-steered --state <state-path> --worker <worker-id> `
  --thread <thread-id> --turn <turn-id> <delivery-id>
```

Confirmation is refused if the worker, thread, or turn no longer matches and is
idempotent after success. Until confirmation, the delivery remains queued and
is returned again on an idempotent message replay. `--wait` performs bounded
readback so the sender sees the resulting `queued`, `steered`, or `delivered`
state. Inbox JSON includes the message ID, request ID, delivery ID, timestamps,
and append-only state history. A completed app-server response is also retained
in the worker transcript as acknowledgement evidence.

### Close the work

```powershell
cs close --note "Merged, released, and verified" <worker-id>
```

`close` is the normal terminal operation. It atomically marks the worker done
or failed, releases active claims, refreshes attached pull-request state,
clears blocker fields, and forwards completion to the parent worker. Use
`report` only when a lifecycle update is needed without releasing claims.

## Common workflows

### Inspect current activity

```powershell
cs status --repo .
cs status --issues
cs show --snapshot <worker-id>
cs worker check <worker-id> --repo .
cs janitor stale
```

`status` reports both engine identity and capability coverage. Snapshots,
transcripts, and work packets provide progressively richer handoff context.

### Coordinate issue and pull-request work

```powershell
cs issue ready --issue owner/repo#123 --repo .
cs issue dispatch --issue owner/repo#123 --repo . `
  --prompt "Implement issue 123" --gate test
cs pr attach --worker <worker-id> --url https://github.com/owner/repo/pull/456
cs pr status <worker-id>
```

GitHub reads and writes are explicit. Local state remains authoritative;
issue-marker comments are a synchronization and audit surface, not the
coordination database. `pr status` observes and records pull-request state but
does not merge it.

### Record validation evidence

Repositories can advertise commands and quality gates in
`codex-swarm.hints.json`:

```powershell
cs repo hints --repo .
cs gate list --repo .
cs gate record --repo . --worker <worker-id> --gate test `
  --exit-code 0 --output "go test ./... passed"
```

Recorded evidence includes the command, result, commit, timestamp, and worker.
Recording a gate does not run the command for you.

### Run the daemon

```powershell
csd serve --addr 127.0.0.1:8787
csd status --addr 127.0.0.1:8787
csd install
```

The daemon binds to loopback by default. Its broad status surfaces are
read-only. Mutation routes are deliberately narrow and require idempotency
keys. Service installation is explicit and uses the native Windows service,
macOS LaunchAgent, or Linux systemd surface.

The daemon persists messages but does not launch Codex on behalf of an HTTP
caller. Externally owned turns are steered only by their owning Codex host; this
avoids elevating agent execution into a SYSTEM or root service or opening a
competing app-server connection that cannot see the in-flight turn.

### Use remote or platform-specific execution

- [Remote Git sessions](docs/remote-sessions.md) run an isolated Codex and Git
  checkout on an SSH host while keeping the coordination ledger local.
- [Bifrost remote development](docs/bifrost.md) keeps Bifrost authoritative
  for workspace changesets, validation, activation, and platform conflicts.
  Swarm records ownership and evidence; it is not a second Bifrost editor.

## Command map

| Area | Commands |
| --- | --- |
| Health and identity | `doctor`, `version`, `agent`, `status` |
| Worker lifecycle | `spawn`, `attach`, `send`, `resume`, `show`, `close`, `report` |
| Coordination | `claim`, `message`, `inbox`, `touch`, `handoff`, `worker check` |
| Handoff context | `workpacket`, `transcript`, `show --snapshot` |
| GitHub | `issue`, `pr` |
| Validation | `repo hints`, `gate`, `validate` |
| Operations | `trace`, `janitor`, `schedule`, `legacy` |
| Platform adapters | `bifrost`, remote options on `spawn` |

Run `cs` or `cs <command> --help` for the current command contract.

## Boundaries and safety

- Local state is authoritative; GitHub is not used as a lock database.
- Claims and file-touch conflicts are warnings, never hard locks.
- Worktree creation, GitHub writes, service changes, and platform mutations
  require explicit commands.
- The daemon does not expose arbitrary command or filesystem execution.
- Tokens, private keys, and platform credentials do not belong in the ledger.
- Remote Git credentials remain on the remote host.
- Schedules are persisted control-plane records; they do not execute workers
  until daemon-owned scheduling is implemented.

## Development

Read [AGENTS.md](AGENTS.md) before changing the repository. The main design and
planning references are:

- [Architecture and invariants](docs/design.md)
- [Current maturity baseline](docs/maturity.md)
- [Roadmap](docs/roadmap.md)
- [Repo-local skill policy](docs/skills.md)

Routine verification:

```powershell
$files = gofmt -l .; if ($files) { $files; exit 1 }
go vet ./...
go test ./...
go build -trimpath ./cmd/cs
go build -trimpath ./cmd/csd
```

Run `go test -race ./...` for concurrency, lifecycle, daemon, store, message,
or cancellation changes. Run `govulncheck ./...` for dependency, command,
path, GitHub, or daemon-security changes.

The project prefers the Go standard library until a dependency removes real
cross-platform risk or stabilizes a durable boundary. See
[docs/mature-go-cli-lessons.md](docs/mature-go-cli-lessons.md) for the current
borrow/defer/avoid guidance.
