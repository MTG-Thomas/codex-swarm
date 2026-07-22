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
cs attention --repo .
cs attention --json
cs show --snapshot <worker-id>
cs worker check <worker-id> --repo .
cs janitor stale
```

`status` reports both engine identity and capability coverage. Snapshots,
transcripts, and work packets provide progressively richer handoff context.
`attention` is a read-only projection over the same SQLite authority. It
surfaces queued messages, blocked claims, stale non-terminal workers, validator
rejections, current failed gates, and recorded pull-request next actions. It
does not refresh GitHub or create a second open-loop ledger; use `cs pr status`
when a recorded pull-request state needs live readback.

### Retain Codex task discovery beyond the host window

Codex hosts can submit each `list_threads` observation to a durable discovery
index. This preserves task identity after a task falls outside a host's newest
task window without attaching the task as a swarm worker. For recurring
coordinator heartbeats, stage each host-owned page as it arrives and finish the
observation without waiting on any task:

```powershell
cs tasks collect page --host desktop --observation heartbeat-20260722T1800Z `
  --page 1 --next-cursor page-2 --file page-1.json
cs tasks collect status --host desktop --observation heartbeat-20260722T1800Z
cs tasks collect page --host desktop --observation heartbeat-20260722T1800Z `
  --page 2 --cursor page-2 --file page-2.json
cs tasks collect finish --host desktop --observation heartbeat-20260722T1800Z `
  --coverage window
cs tasks list --limit 100 --json
cs tasks status --stale-for 24h
```

Each page file uses a deliberately narrow metadata-only shape:

```json
{
  "tasks": [
    {
      "thread_id": "019f84c9-84e0-7b43-ab2f-a0de6287c627",
      "title": "Coordinator",
      "project": "codex-swarm",
      "cwd": "C:\\Users\\ThomasBray\\src\\codex-swarm",
      "status": "active",
      "unread": false,
      "wait_cursor": "opaque-host-cursor",
      "coordinator": true
    }
  ]
}
```

`--host` may come from `CODEX_HOST_ID`; connected hosts must use distinct,
stable IDs. For example, a collector running on the connected remote
workstation uses its own identity and loopback daemon:

```powershell
$env:CODEX_HOST_ID = "codex-remote-workstation-01"
$env:CODEX_SWARM_DAEMON_URL = "http://127.0.0.1:8787"
cs tasks collect page --observation remote-heartbeat-20260722T1800Z `
  --page 1 --file remote-page-1.json
cs tasks collect status --observation remote-heartbeat-20260722T1800Z
```

Do not expose `csd` beyond loopback to centralize collection. A connected host
either submits to its own local daemon or passes the same metadata-only page to
the coordinator, which records it under that host's stable ID.

The first page fixes the observation timestamp. Page and finish
retries are idempotent, while changed content under the same observation/page
identity is rejected. `finish` validates contiguous pages, the opaque cursor
chain, duplicate task IDs, and the 1,000-task bound before one atomic ingest.
Use `coverage: window` whenever the owning host has only a bounded view. Use
`complete` only after the final page returned no next cursor; only then can
absence mark an older task missing. Explicit `tombstoned: true` remains the
only deletion-like observation.

The collector is a hook for the Codex host that already owns `list_threads`.
It does not call Codex, scrape session files, launch app-server, or infer task
lifecycle from swarm worker state. Existing `tasks list` and `tasks status`
provide immediate nonblocking daemon readback after `finish`;
`tasks collect status` returns the staged page count and exact next cursor after
an interrupted heartbeat. Opaque listing and wait cursors are preserved
byte-for-byte.
Unfinished collections older than seven days are pruned when a new observation
starts, and the daemon refuses to exceed 1,000 simultaneously open
observations.

For bootstrap, classification updates, or another trusted producer that
already owns a complete envelope, direct ingestion remains available:

```powershell
cs tasks ingest --file codex-task-snapshot.json
```

### Group related coordination as one operation

`cs operation` derives one logical view across existing worker ancestry, issue
links, claims, message deliveries, gate evidence, recorded pull requests, and
indexed Codex tasks:

```powershell
cs operation list
cs operation list --issue MTG-Thomas/codex-swarm#75 --json
cs operation show issue:mtg-thomas/codex-swarm#75
```

Issue-backed work uses a case-normalized `issue:owner/repo#number` key. Other
worker trees use `worker:<root-worker-id>`. Missing parents, parent cycles,
invalid issue references, and unlinked records remain visible and keyless; the
projection never guesses an operation from a repository path or task title.
It is read-only and does not create an operation ledger or change claim
warning semantics.

The metadata-only snapshot contract is:

```json
{
  "request_id": "local-20260722T170000Z",
  "host_id": "local",
  "source": "codex.list_threads",
  "observed_at": "2026-07-22T17:00:00Z",
  "coverage": "window",
  "tasks": [
    {
      "thread_id": "019f84c9-84e0-7b43-ab2f-a0de6287c627",
      "title": "Coordinator",
      "project": "codex-swarm",
      "cwd": "C:\\Users\\ThomasBray\\src\\codex-swarm",
      "status": "active",
      "unread": false,
      "wait_cursor": "opaque-host-cursor",
      "coordinator": true,
      "classification": {
        "tier": "P0",
        "last_meaningful_outcome": "Implementation is under review",
        "unresolved_loop": "PR has not merged",
        "smallest_next_action": "Read review findings"
      }
    }
  ]
}
```

Use `coverage: "window"` for bounded `list_threads` results. Absence from a
window never means complete or deleted. `coverage: "complete"` may mark an
older record `missing_since`, and an explicit task `tombstoned: true` records a
host-observed tombstone. The index keeps titles, paths, status, unread state,
timestamps, discovery source, and opaque cursors; it does not accept prompt,
final-message, or transcript bodies. Optional coordinator classification keeps
P0-P3 tier, last outcome, unresolved loop, operator decision, smallest next
action, and classification time. Omitting `wait_cursor` preserves the last
cursor; supplying an empty string clears it. Walk every result page with
`next_cursor`; the default page size is 50, but the retained inventory is not
limited to 50.

The cache begins with what a host explicitly ingests. It prevents future
forgetting and can bootstrap older known task IDs, but it does not scrape Codex
session files or claim to discover pre-index history on its own.

Indexed `status` is the Codex host's observation, independent of an attached
swarm worker lifecycle. A task may therefore remain `active` and resumable even
when a synchronous launch request timed out and its worker record says failed.

When `CODEX_SWARM_DAEMON_URL` or `--daemon` is set, the CLI uses the typed
loopback API. Collector pages and finish requests use
`POST /v1/codex-tasks/collections/pages` and
`POST /v1/codex-tasks/collections/finish`. Trusted producers may still post a
full envelope to `POST /v1/codex-tasks/ingest`. Readback uses
`GET /v1/codex-tasks` or `GET /v1/codex-tasks/status`. All mutation routes are
loopback-only. Collector and direct-ingest replay records have bounded
retention.

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
| Codex task discovery | `tasks ingest`, `tasks list`, `tasks status` |
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
