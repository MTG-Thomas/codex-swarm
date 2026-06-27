# Best Of Jcode AO Sandcastle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `cs` into a practical local Codex swarm runner that combines jcode-style interagent protocol, AO-style lifecycle/state discipline, and Sandcastle-style workspace/session safety.

**Architecture:** Keep `cs` small and local-first. Local locked state is authoritative; GitHub issues are queue/audit/sync surfaces, not the database. Worktree/session isolation must be explicit so subagent fan-out cannot accidentally share a mutable workspace.

**Tech Stack:** Go 1.26, standard library first, `gh` CLI boundary for GitHub until fake-`gh` tests prove pain, `codex app-server` JSON-RPC through existing appserver package.

---

## Source Lessons To Encode

- **jcode:** durable swarm plan/member state, idempotent mutation responses, explicit live resume/takeover rules, evented interagent communication.
- **AO:** canonical lifecycle state/reason, locked atomic metadata, stale runtime reconciliation, worktree restore discipline, GitHub as tracker/integration rather than source of truth.
- **Sandcastle:** branch/worktree isolation, completion-timeout handling for hung agent CLIs, provider-owned session storage, explicit warning that session fork is not workspace isolation.

## File Structure

- Modify `internal/store/json.go`: add file locking and atomic write guarantees for the machine-global state file.
- Modify `internal/store/json_test.go`: cover concurrent mutation and corrupted/temp-file behavior.
- Modify `internal/claims/claims.go`: add claim lifecycle reason, worker validation hooks, and merge/import conflict metadata.
- Modify `cmd/cs/issue.go`: add `issue pull --dry-run`, richer conflict output, and issue marker revision metadata.
- Modify `cmd/cs/issue_test.go`: add fake-`gh` tests for marker create/update/pull/report behavior.
- Modify `internal/worktree/worktree.go`: add Sandcastle-inspired managed worktree locks, stale lock cleanup, safer reuse rules, and branch collision diagnostics.
- Modify `internal/worktree/worktree_test.go`: cover lock contention, stale lock cleanup, dirty reuse, external branch collision, and Windows path normalization.
- Create `internal/lifecycle/lifecycle.go`: canonical worker lifecycle with session/runtime/claim state and reason fields.
- Create `internal/lifecycle/lifecycle_test.go`: cover status derivation and terminal marker clearing.
- Modify `internal/daemon/server.go`: expose read-only swarm/claim/lifecycle status endpoints for local dashboards and subagents.
- Modify `internal/daemon/server_test.go`: cover daemon status shape and stale runtime reconciliation.
- Modify `internal/appserver/client.go`: add launch/send completion handling hooks where app-server calls can hang or return partial thread metadata.
- Modify `internal/appserver/client_test.go`: cover completion signal, timeout, and partial metadata preservation with fake JSON-RPC.
- Modify `cmd/cs/main.go`, `cmd/cs/claims.go`, `cmd/cs/agents.go`: wire new CLI commands/flags without introducing a CLI framework yet.
- Modify `README.md`, `docs/design.md`, `docs/roadmap.md`, `docs/maturity.md`: document the new operating model and the remaining complexity budget.

---

### Task 1: Locked Atomic State Writes

**Files:**
- Modify: `internal/store/json.go`
- Test: `internal/store/json_test.go`

- [x] **Step 1: Write failing tests for locked writes**

Add tests that run concurrent state mutations against the same temp state path and assert the final JSON is parseable and contains all non-conflicting updates. Add a test that leaves a stale `.tmp` file beside the state and verifies load ignores it.

Run:

```powershell
go test ./internal/store -run TestJSONStore
```

Expected: new tests fail because writes are not locked strongly enough.

- [x] **Step 2: Implement a package-local lock**

Use a sidecar lock file, for example `<state>.lock`, with atomic create on platforms where possible. Keep the implementation standard-library only. The lock should time out with a clear error after a short bounded wait.

- [x] **Step 3: Make writes temp-file plus rename under the lock**

Write to `<state>.tmp.<pid>`, close the file, then rename to the target while holding the lock. Never expose partial JSON.

- [x] **Step 4: Verify**

Run:

```powershell
go test ./internal/store
go test ./...
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```powershell
git add internal/store
git commit -m "feat: lock and atomically write swarm state"
```

---

### Task 2: Canonical Lifecycle State

**Files:**
- Create: `internal/lifecycle/lifecycle.go`
- Create: `internal/lifecycle/lifecycle_test.go`
- Modify: `internal/claims/claims.go`
- Modify: `cmd/cs/main.go`

- [x] **Step 1: Write lifecycle tests**

Cover these cases:

- `working/task_in_progress` derives display status `working`.
- dead runtime with reason `runtime_lost` derives display status `stale`.
- `done/completed` is terminal.
- restoring a terminal worker clears `completed_at` and `terminated_at`.

Run:

```powershell
go test ./internal/lifecycle
```

Expected: package does not exist or tests fail.

- [x] **Step 2: Implement lifecycle package**

Define small structs:

```go
type SessionState string
type RuntimeState string
type Reason string

type Lifecycle struct {
	Version int `json:"version"`
	Session SessionLifecycle `json:"session"`
	Runtime RuntimeLifecycle `json:"runtime"`
}
```

Include `DeriveStatus`, `IsTerminal`, `ClearTerminalMarkersForNonTerminal`, and constructors for worker/orchestrator lifecycles.

- [x] **Step 3: Store lifecycle on workers without breaking old state**

Add optional lifecycle fields and default old workers into a synthesized lifecycle on read. Do not delete or rename existing status fields in this task.

- [x] **Step 4: Verify**

Run:

```powershell
go test ./internal/lifecycle ./internal/claims ./cmd/cs
go test ./...
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```powershell
git add internal/lifecycle internal/claims cmd/cs
git commit -m "feat: add canonical worker lifecycle"
```

---

### Task 3: Issue Sync Conflict Semantics

**Files:**
- Modify: `cmd/cs/issue.go`
- Modify: `cmd/cs/issue_test.go`
- Modify: `internal/github/issues.go`
- Test: `internal/github/issues_test.go`

- [x] **Step 1: Write fake-`gh` tests**

Cover:

- `issue sync` updates the latest existing marker instead of creating duplicates.
- `issue pull` skips older remote claims when local claim `UpdatedAt` is newer.
- `issue pull --force` overwrites newer local claims.
- `issue pull --dry-run` prints imported/skipped/conflicted counts and makes no state changes.
- `issue report` posts a worker report to the issue and includes worker ID.

Run:

```powershell
go test ./cmd/cs -run TestIssue
```

Expected: dry-run and fake-`gh` coverage fails until implemented.

- [x] **Step 2: Add marker revision metadata**

Extend hidden marker JSON with:

```json
{
  "schema": "codex-swarm:claims:v1",
  "snapshot_id": "...",
  "generated_at": "...",
  "machine_id": "...",
  "claims": []
}
```

Keep backward compatibility with existing marker comments that only contain claims.

- [x] **Step 3: Implement dry-run pull**

Dry-run must parse remote marker, compute the same merge plan as real pull, and print the plan without writing local state.

- [x] **Step 4: Verify**

Run:

```powershell
go test ./cmd/cs ./internal/github
go test ./...
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```powershell
git add cmd/cs/issue.go cmd/cs/issue_test.go internal/github
git commit -m "feat: add issue sync conflict planning"
```

---

### Task 4: Worker And Claim Validation

**Files:**
- Modify: `cmd/cs/claims.go`
- Modify: `cmd/cs/legacy_test.go`
- Modify: `internal/claims/claims.go`
- Modify: `internal/claims/claims_test.go`

- [x] **Step 1: Write validation tests**

Cover:

- `claim create --worker missing` fails with a clear message.
- `claim create --worker <id>` succeeds for a known worker.
- legacy imported claims may keep unknown historical worker IDs but are marked `external`.

Run:

```powershell
go test ./cmd/cs ./internal/claims
```

Expected: missing-worker validation test fails.

- [x] **Step 2: Add validation path**

Before creating a normal claim, load state and verify the worker exists. Keep an explicit bypass only for legacy import code paths.

- [x] **Step 3: Verify**

Run:

```powershell
go test ./cmd/cs ./internal/claims
go test ./...
```

Expected: all tests pass.

- [x] **Step 4: Commit**

```powershell
git add cmd/cs/claims.go cmd/cs/legacy_test.go internal/claims
git commit -m "feat: validate claim worker ids"
```

---

### Task 5: Managed Worktree Locks And Safe Reuse

**Files:**
- Modify: `internal/worktree/worktree.go`
- Modify: `internal/worktree/worktree_test.go`
- Modify: `cmd/cs/main.go`
- Modify: `README.md`

- [x] **Step 1: Write worktree safety tests**

Cover:

- two workers targeting the same managed branch cannot receive the same worktree concurrently;
- stale lock with dead PID is pruned;
- dirty managed worktree is reused with warning and no refresh;
- branch checked out in the main repo or an external worktree fails with a clear message;
- Windows-style and Git forward-slash paths compare correctly.

Run:

```powershell
go test ./internal/worktree
```

Expected: new lock and collision tests fail.

- [x] **Step 2: Implement worktree lock files**

Use `.codex-swarm/locks/<safe-name>.lock` or an equivalent repo-local managed directory. Include PID, branch, path, and acquired timestamp. Acquire with atomic create; stale-detect dead PID; fail fast on live owner.

- [x] **Step 3: Harden branch naming**

Add timestamp plus random suffix for generated branches so concurrent runs cannot collide inside the same second.

- [x] **Step 4: Verify**

Run:

```powershell
go test ./internal/worktree
go test ./...
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```powershell
git add internal/worktree cmd/cs/main.go README.md
git commit -m "feat: lock managed worktrees"
```

---

### Task 6: Launch Completion And Hang Semantics

**Files:**
- Modify: `internal/appserver/client.go`
- Modify: `internal/appserver/client_test.go`
- Modify: `cmd/cs/main.go`
- Modify: `README.md`

- [x] **Step 1: Write tests for partial completion**

Cover:

- completion signal observed before process/session finalization records success plus warning;
- no completion signal still times out as failure;
- trailing usage/thread metadata after completion signal is preserved when received before the grace window expires.

Run:

```powershell
go test ./internal/appserver
```

Expected: tests fail until completion timeout exists.

- [x] **Step 2: Add completion timeout options**

Add a small internal struct for launch/run options:

```go
type CompletionPolicy struct {
	Signal string
	IdleTimeout time.Duration
	CompletionTimeout time.Duration
}
```

Default signal can be empty for current app-server calls; expose flags only where they are useful for spawned shell-agent support.

- [x] **Step 3: Preserve metadata on warning-success**

If a completion signal is seen, return a successful result with a warning instead of discarding thread/session data because the process did not exit cleanly.

- [x] **Step 4: Verify**

Run:

```powershell
go test ./internal/appserver
go test ./...
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```powershell
git add internal/appserver cmd/cs/main.go README.md
git commit -m "feat: handle agent completion timeouts"
```

---

### Task 7: Durable Swarm Events And Idempotent Mutations

**Files:**
- Modify: `internal/claims/claims.go`
- Modify: `internal/claims/claims_test.go`
- Modify: `cmd/cs/agents.go`
- Modify: `cmd/cs/main_test.go`

- [x] **Step 1: Write event/mutation tests**

Cover:

- repeated `message` with the same request ID records one event;
- repeated `handoff` with the same request ID returns the original result;
- event history is bounded and ordered;
- events include from, to, issue, worker, request ID, and timestamp.

Run:

```powershell
go test ./cmd/cs ./internal/claims
```

Expected: idempotent request behavior fails.

- [x] **Step 2: Add mutation request IDs**

Add optional `--request-id` to machine-oriented commands. Generate one when omitted. Store completed mutation summaries long enough to replay repeated requests.

- [x] **Step 3: Add bounded event log**

Keep event log local state compact. A fixed cap such as 500 events is enough for MVP.

- [x] **Step 4: Verify**

Run:

```powershell
go test ./cmd/cs ./internal/claims
go test ./...
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```powershell
git add internal/claims cmd/cs
git commit -m "feat: make swarm messages idempotent"
```

---

### Task 8: Daemon Read-Only Status Surface

**Files:**
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/server_test.go`
- Modify: `cmd/csd/main.go`
- Modify: `README.md`

- [x] **Step 1: Write daemon API tests**

Cover:

- `GET /status` returns daemon version and state path;
- `GET /workers` returns worker IDs, lifecycle status, issue, worktree, and thread ID;
- `GET /claims` returns claims and conflicts;
- all endpoints are read-only.

Run:

```powershell
go test ./internal/daemon
```

Expected: new endpoint tests fail.

- [x] **Step 2: Implement read-only handlers**

Avoid mutation through daemon in this task. The CLI remains the mutation entry point.

- [x] **Step 3: Wire `cs status` preference**

Keep existing `CODEX_SWARM_DAEMON_URL` behavior, but include lifecycle and conflict counts from daemon response.

- [x] **Step 4: Verify**

Run:

```powershell
go test ./internal/daemon ./cmd/cs ./cmd/csd
go test ./...
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```powershell
git add internal/daemon cmd/cs cmd/csd README.md
git commit -m "feat: expose read-only swarm daemon status"
```

---

### Task 9: Friend-Demo Swarm Script

**Files:**
- Create: `scripts/demo-swarm.ps1`
- Create: `scripts/demo-swarm.sh`
- Modify: `README.md`
- Modify: `docs/maturity.md`

- [x] **Step 1: Write smoke-testable demo scripts**

Scripts should:

1. create a temp state file;
2. register coordinator and two workers;
3. create an issue-linked claim;
4. send a worker message;
5. create one worktree-backed mock worker;
6. print `cs status`;
7. clean only temp state and temp demo worktrees it created.

- [x] **Step 2: Run PowerShell demo**

Run:

```powershell
.\scripts\demo-swarm.ps1
```

Expected: exits 0 and prints worker/claim/status summary.

- [x] **Step 3: Run Bash demo where available**

Run:

```powershell
bash ./scripts/demo-swarm.sh
```

Expected: exits 0 on systems with Bash; if Bash is unavailable on Windows, document that only PowerShell was verified.

- [x] **Step 4: Commit**

```powershell
git add scripts README.md docs/maturity.md
git commit -m "docs: add local swarm demo scripts"
```

---

### Task 10: Final Verification And Roadmap Update

**Files:**
- Modify: `docs/roadmap.md`
- Modify: `docs/design.md`
- Modify: `docs/mature-go-cli-lessons.md`

- [x] **Step 1: Update design docs**

Document:

- local state is authoritative;
- GitHub issue markers are sync/audit only;
- session fork does not imply worktree isolation;
- daemon is read-only until mutation API needs a stronger auth model;
- future dependency triggers: SQLite, GitHub client, service manager, CLI framework.

- [x] **Step 2: Run full verification**

Run:

```powershell
gofmt -w .
go vet ./...
go test ./...
go build -trimpath ./cmd/cs
go build -trimpath ./cmd/csd
govulncheck ./...
```

Expected: all pass. If `govulncheck` is unavailable, install/use the existing local toolchain path or record the exact blocker.

- [x] **Step 3: Commit**

```powershell
git add docs README.md
git commit -m "docs: record swarm orchestration operating model"
```

---

## Execution Notes

- Prefer one task per branch or at least one commit per task.
- Do not introduce SQLite, Cobra, a GitHub SDK, or a service helper unless a task cannot be completed cleanly without it.
- Do not move GitHub issue comments into the source-of-truth role.
- Do not promise safe concurrent fan-out unless each worker has a distinct worktree/branch.
- Keep Windows behavior first-class: PowerShell examples, path normalization tests, no Unix-only locking assumptions.

## Self-Review

- Spec coverage: the plan maps jcode to idempotent events/mutations, AO to lifecycle/locked state/daemon status, and Sandcastle to worktree/session/completion safety.
- Placeholder scan: no implementation step depends on a later unspecified design; each task has files, test intent, verification commands, and commit boundary.
- Type consistency: lifecycle, completion policy, marker revision, and mutation request IDs are introduced once and referenced consistently.
