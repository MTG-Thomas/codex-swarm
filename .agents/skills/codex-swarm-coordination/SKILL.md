---
name: codex-swarm-coordination
description: "Use for substantial local Codex coordination: workers, warning-only claims, messages, callbacks, handoffs, evidence, open-loop readback, and terminal closeout."
---

# Codex Swarm Coordination

Use `cs` as the machine-local coordination protocol for substantial repository,
platform, and live-operations work. Git, GitHub, Codex, and the operated system
remain authoritative for their own state; swarm makes ownership, communication,
evidence, and closeout durable in one local SQLite ledger.

## 1. Verify the installed surface

Run before relying on coordination behavior:

```powershell
cs version
cs doctor
```

Use `cs doctor --appserver` only when live Codex app-server access is part of the
task. If installed help disagrees with this skill, treat `cs <command> --help`
and the checked-out repository as current evidence, report the version skew, and
do not pretend an unsupported command succeeded.

The default machine-global state path may end in `state.json`, but current state
is SQLite. Use `--state` or `CODEX_SWARM_STATE` only for an intentionally
isolated ledger. Set `CODEX_SWARM_DAEMON_URL=http://127.0.0.1:8787` when the CLI
should use the matching user-session daemon.

## 2. Establish worker identity

Attach an existing Codex task instead of fabricating a second execution:

```powershell
cs attach --repo "<absolute-repo>" --thread "<thread-id>" `
  --prompt "<short task summary>"
```

Create a worker when this task does not already have one:

```powershell
cs spawn --repo "<absolute-repo>" --role "<role>" `
  --prompt "<short task summary>"
```

For work dispatched or resumed by another task, preserve the coordinator's
worker and Codex host/thread identity. Create the worker with
`--parent <coordinator-worker-id>` so completion has a durable route back.

Use `--engine appserver` only to create a real Codex task. Add `--worktree` only
when filesystem isolation is required; conversation isolation does not isolate
files. App-server spawn may continue asynchronously after durable host, thread,
turn, and worktree identity is recorded. Inspect the worker before retrying a
timed-out spawn so a resumable task is not duplicated.

Use remote `spawn` options only for an explicitly authorized SSH workspace.
Remote Git state remains authoritative on the remote host while the coordination
ledger remains local.

## 3. Check and claim exact scope

Before meaningful edits or mutations:

```powershell
cs claim conflicts --repo "<absolute-repo>" --scope "<path-or-task-scope>"
cs claim create --repo "<absolute-repo>" --scope "<path-or-task-scope>" `
  --worker "<worker-id>" --note "<intent>"
```

Use repeated `--scope` flags when the operation spans several exact paths or
live resources. Add `--issue owner/repo#123` for issue-backed work. Claims and
touch conflicts are warnings, never locks or permission grants. Report live
conflicts clearly, but do not let stale claims block useful work.

Use `cs touch` for file-level write intent when several active workers are near
the same files. Never add coordination state to the repository.

## 4. Communicate through durable messages

```powershell
cs message --json --wait 5s <from-worker-id> <to-worker-id> "<message>"
cs inbox --json <worker-id>
cs handoff <from-worker-id> <to-worker-id> "<operator-ready handoff>"
```

Messages are stored before delivery. Preserve message, request, delivery,
worker, host, thread, and turn IDs in errors and handoffs.

When JSON returns `native_steering`, use the owning Codex host's native task
message tool with the returned host, thread, turn, and exact prompt. Then record
the outcome:

```powershell
cs message confirm-steered --state "<state-path>" --worker "<worker-id>" `
  --thread "<thread-id>" --turn "<turn-id>" "<delivery-id>"

cs message steering-failed --state "<state-path>" --worker "<worker-id>" `
  --thread "<thread-id>" --turn "<turn-id>" --error "<tool error>" `
  "<delivery-id>"
```

Never open a competing app-server process to steer an externally owned active
turn.

When JSON returns `native_followup`, use the owning host's native task-message
tool to start the idle destination task's next turn with the exact prompt. Then
record success with `cs message confirm-followup`, or record the tool error with
`cs message followup-failed`. Do not finish while a returned native callback is
unacknowledged or silently stranded.

## 5. Keep coordinators nonblocking

Dispatch independent child work, preserve parent links, and remain available
for operator input. Prefer native completion callbacks, detached heartbeats,
and compact immediate snapshots over long foreground polling.

For current readback:

```powershell
cs status --repo "<absolute-repo>"
cs attention --repo "<absolute-repo>"
cs show --snapshot <worker-id>
cs workpacket --worker <worker-id>
cs transcript <worker-id>
cs worker check <worker-id> --repo "<absolute-repo>"
```

`attention` is a read-only projection of queued messages, blocked claims, stale
workers, validator rejections, failed gates, and recorded pull-request next
actions. It is not a second task ledger and does not refresh GitHub.

## 6. Preserve task, operation, decision, and validation evidence

Use the durable Codex task index when a coordinator owns task-list observations
that must outlive the host's newest-task window:

```powershell
cs tasks collect page --host "<stable-host-id>" --observation "<id>" `
  --page 1 --file "<metadata-only-page.json>"
cs tasks collect finish --host "<stable-host-id>" --observation "<id>" `
  --coverage window
cs tasks list --json
cs tasks status --stale-for 24h
```

Use `coverage window` for bounded host results. Use `complete` only after the
host proved the final page. Preserve opaque cursors byte-for-byte. Do not put
prompts, transcripts, final messages, or tool output into task snapshots.

Group existing records without creating another authority:

```powershell
cs operation list --issue owner/repo#123 --json
cs operation show issue:owner/repo#123
cs decision list --issue owner/repo#123 --current
```

Use `cs decision record` or `supersede` only for an explicit durable decision
with rationale and bounded evidence references. Broken ancestry or provenance
must remain visible; do not guess operation identity from a repo path or title.

Record verification that actually ran:

```powershell
cs repo hints --repo "<absolute-repo>"
cs gate list --repo "<absolute-repo>"
cs gate record --repo "<absolute-repo>" --worker "<worker-id>" `
  --gate <gate-id> --exit-code <code> --output "<concise result>"
```

Gate recording does not execute the command. For pull-request work, attach and
refresh exact live state with `cs pr attach` and `cs pr status`; GitHub remains
authoritative for checks, reviews, and merge state.

## 7. Close atomically and deliver callbacks

When `cs close --help` exposes JSON closeout, finish coordinated work with:

```powershell
cs close --json --note "<outcome, proof, and residual risk>" <worker-id>
```

On an older installed version without `--json`, use the supported
`cs close --note ... <worker-id>` form, report the version skew, and upgrade
before relying on native parent callbacks. Do not claim callback delivery from
human-readable close output.

`close` is preferred over `report`: it records terminal state, releases active
claims, refreshes attached pull requests, clears blockers, preserves evidence,
and forwards completion to the parent. Use `--status failed` only for a real
terminal failure. Use `report` only for a lifecycle update that must not release
claims.

Inspect the JSON response. Deliver every returned `native_steering` or
`native_followup` through the owning Codex host and acknowledge it with the
matching confirm command. If the native tool fails, record the matching failure
command. Closeout is not durable until every callback is confirmed or has a
recorded delivery error.

## Safety boundaries

- Do not create workers for tiny read-only checks.
- Do not mutate GitHub, schedules, worktrees, services, Codex tasks, remote
  hosts, or platform state unless the command and user intent are explicit.
- Keep `csd` loopback-only. Never expose it to centralize multiple hosts.
- Prefer runtime capabilities (`live_message`, `resume`, `managed_worktree`,
  `automatic_completion`, `external_tracker`) over engine-name assumptions.
- Keep secrets, tokens, private keys, prompt bodies, and customer data out of
  the ledger.
- Use `cs legacy import-coordinator` only for migration. Use the legacy
  PowerShell coordinator only for stale-claim inspection or migration fallback.
