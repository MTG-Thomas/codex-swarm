# Readiness Dispatch Daemon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a daemon-ready issue readiness and dispatch path that can safely decide whether GitHub issue work is runnable, then explicitly create local workers from that decision.

**Architecture:** Start with a read-only readiness model in the CLI, then move the same model behind `csd` once the contract is stable. Keep dispatch explicit, idempotent, and local-first; do not let the daemon mutate GitHub or spawn workers from schedules until readiness and dispatch are both inspectable.

**Tech Stack:** Go standard library, existing `gh` CLI boundary, existing JSON store, existing `cs`/`csd` command layout, optional `golang.org/x/sys/windows/svc` only when implementing Windows service mode.

---

## Service Pattern Decision

Use `golang.org/x/sys/windows/svc` for the Windows service implementation when `csd install` moves past the current stub. It is the primary Go package for Windows services, includes service detection, debug mode, service-manager helpers, and event log support. Keep it isolated behind Windows build tags so Linux/macOS builds do not absorb Windows-only code.

Do not add `github.com/kardianos/service` yet. It is popular and cross-platform, but the repo's own dependency policy says to add a service manager package only when platform helpers stop being small. For this project, Windows is the immediate target, and launchd/systemd can remain future platform files.

Sources checked:
- `golang.org/x/sys/windows/svc`: package docs say it provides what is required to build Windows services and includes debug/eventlog/mgr helpers.
- `golang.org/x/sys/windows/svc/example`: official example demonstrates install, remove, stop, start, pause, continue, and event log usage.
- `github.com/kardianos/service`: supports Windows, Linux service managers, and launchd, but would add a broad abstraction before we need it.

## Target Contract

Readiness answers: "Can this issue be dispatched on this machine right now?"

Dispatch answers: "Create the local worker set for this ready issue now."

Daemon answers: "Keep the readiness/dispatch loop alive, visible, restartable, and eventually schedulable."

Windows service answers: "Start `csd` at login/boot with predictable shutdown and local status readback."

## Files

- Modify `cmd/cs/issue.go`: add `issue ready` command and report rendering.
- Modify `cmd/cs/issue_test.go`: fake `gh` tests for readiness cases.
- Create `internal/readiness/readiness.go`: pure readiness model and evaluator.
- Create `internal/readiness/readiness_test.go`: deterministic model tests.
- Modify `internal/github/issues.go`: add or reuse issue metadata fetch helpers.
- Modify `internal/repohints/hints.go`: expose quality gates already parsed by repo hints.
- Modify `cmd/cs/validate.go`: make dispatch reuse the existing validator pair shape.
- Create `internal/dispatch/dispatch.go`: explicit dispatch plan and idempotency helpers.
- Create `internal/dispatch/dispatch_test.go`: plan/idempotency tests.
- Modify `internal/daemon/server.go`: expose readiness and dispatch preview endpoints after CLI contract is stable.
- Modify `cmd/csd/main.go`: add context-aware serve loop and prepare service runner seam.
- Create `cmd/csd/service_windows.go`: Windows service entrypoint using `x/sys/windows/svc`.
- Create `cmd/csd/service_stub.go`: non-Windows service stubs.
- Modify `README.md` and `docs/roadmap.md`: document readiness, dispatch, daemon, and Windows service phases.

---

### Task 1: Issue Readiness CLI

**Files:**
- Create: `internal/readiness/readiness.go`
- Create: `internal/readiness/readiness_test.go`
- Modify: `cmd/cs/issue.go`
- Modify: `cmd/cs/issue_test.go`

- [ ] **Step 1: Write readiness model tests**

Add tests that build a readiness report from in-memory inputs:

```go
func TestEvaluateReadyIssue(t *testing.T) {
	report := readiness.Evaluate(readiness.Input{
		Issue: readiness.Issue{
			Ref:   "MTG-Thomas/codex-swarm#18",
			Title: "Add issue dispatch readiness",
			Body:  "Acceptance criteria",
		},
		Repo: "C:/repo",
		Gates: []readiness.Gate{{ID: "test", Command: "go test ./..."}},
	})
	if !report.Ready {
		t.Fatalf("Ready = false, blockers = %#v", report.Blockers)
	}
}
```

Expected blockers:
- missing issue title/body
- repo path missing
- malformed issue ref
- active claim on the issue
- no quality gates configured

- [ ] **Step 2: Implement `internal/readiness`**

Create simple structs:

```go
type Issue struct {
	Ref   string `json:"ref"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type Gate struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Scope   string `json:"scope,omitempty"`
}

type Claim struct {
	ID       string `json:"id"`
	WorkerID string `json:"worker_id,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Status   string `json:"status"`
}

type Report struct {
	Ready    bool     `json:"ready"`
	Issue    Issue    `json:"issue"`
	Repo     string   `json:"repo"`
	Gates    []Gate   `json:"gates,omitempty"`
	Claims   []Claim  `json:"claims,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
}
```

`Evaluate` must be deterministic, side-effect free, and return `Ready=false` when any blocker exists.

- [ ] **Step 3: Add `cs issue ready`**

Command shape:

```powershell
cs issue ready --issue MTG-Thomas/codex-swarm#18 --repo .
cs issue ready --json --issue MTG-Thomas/codex-swarm#18 --repo .
```

Implementation rules:
- Fetch issue title/body using `gh issue view --json title,body`.
- Load local claims from the JSON store.
- Load repo hints and quality gates.
- Never mutate GitHub.
- Print text output with stable first line: `ready=true issue=... repo=... blockers=0`.
- Print JSON when `--json` is set.

- [ ] **Step 4: Extend fake `gh` tests**

Add fake `gh issue view` handling for `--json title,body`.

Test cases:
- ready issue with title/body and gates.
- missing body produces blocker.
- active issue claim produces blocker.
- malformed issue ref returns command error.

- [ ] **Step 5: Verify**

Run:

```powershell
go test -count=1 ./internal/readiness ./cmd/cs ./internal/repohints
go test ./...
```

---

### Task 2: Explicit Dispatch Plan

**Files:**
- Create: `internal/dispatch/dispatch.go`
- Create: `internal/dispatch/dispatch_test.go`
- Modify: `cmd/cs/issue.go`
- Modify: `cmd/cs/validate.go`
- Modify: `README.md`

- [ ] **Step 1: Write dispatch plan tests**

The dispatch package should convert a ready report into a plan without writing state:

```go
plan, err := dispatch.Plan(dispatch.Input{
	Readiness: readyReport,
	Prompt: "implement issue #18",
	Gates: []string{"test"},
})
```

Expected:
- rejects readiness reports with `Ready=false`.
- emits implementer role.
- emits validator role.
- includes issue ref and gate IDs.
- includes an idempotency key based on issue, repo, prompt, and gates.

- [ ] **Step 2: Add explicit command**

Command shape:

```powershell
cs issue dispatch --issue MTG-Thomas/codex-swarm#18 --repo . --prompt "implement issue #18" --gate test
```

Implementation:
- Reuse Task 1 readiness.
- If not ready, print blockers and exit non-zero.
- If ready, call the same worker creation helper used by `cs validate start`.
- Do not post to GitHub.
- Do not schedule future work.

- [ ] **Step 3: Add idempotency**

Persist a dispatch event on both created workers:

```text
Type: issue.dispatch
Message: issue=<ref> request=<key>
```

If the same request key was already completed, print the original implementer/validator IDs instead of creating duplicates.

- [ ] **Step 4: Verify**

Run:

```powershell
go test -count=1 ./internal/dispatch ./cmd/cs
go test ./...
```

---

### Task 3: Daemon Readiness API

**Files:**
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/server_test.go`
- Modify: `cmd/csd/main.go`
- Modify: `README.md`

- [ ] **Step 1: Add read-only endpoint tests**

Add:

```text
GET /readiness?issue=MTG-Thomas/codex-swarm%2318&repo=C:/repo
```

Expected JSON body matches the Task 1 readiness report.

- [ ] **Step 2: Implement endpoint**

Keep endpoint read-only. It may call the same issue metadata helper as the CLI, but tests should use an injectable provider so daemon unit tests do not shell out to live `gh`.

- [ ] **Step 3: Add daemon client method**

Add:

```go
func (c Client) Readiness(ctx context.Context, issue, repo string) (readiness.Report, error)
```

- [ ] **Step 4: Wire optional CLI daemon mode**

If `CODEX_SWARM_DAEMON_URL` is set, `cs issue ready` may call the daemon. If the daemon call fails, return the daemon error; do not silently fall back for readiness because operators need to know which control plane answered.

- [ ] **Step 5: Verify**

Run:

```powershell
go test -count=1 ./internal/daemon ./cmd/cs
go test ./...
```

---

### Task 4: Daemon Dispatch API

**Files:**
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/server_test.go`
- Modify: `internal/store/json.go`
- Modify: `cmd/cs/issue.go`
- Modify: `README.md`

- [ ] **Step 1: Define local mutation rules**

Dispatch over daemon must require:
- loopback daemon URL only.
- explicit request ID.
- readiness report is ready.
- idempotent replay by request ID.

- [ ] **Step 2: Add endpoint**

Endpoint:

```text
POST /dispatch
```

Body:

```json
{
  "request_id": "r-...",
  "issue": "MTG-Thomas/codex-swarm#18",
  "repo": "C:/repo",
  "prompt": "implement issue #18",
  "gates": ["test"]
}
```

Response:

```json
{
  "request_id": "r-...",
  "implementer": "w-...",
  "validator": "w-...",
  "replayed": false
}
```

- [ ] **Step 3: CLI opt-in**

`cs issue dispatch` should use the daemon only when `CODEX_SWARM_DAEMON_URL` is set or `--daemon` is provided.

- [ ] **Step 4: Verify**

Run:

```powershell
go test -race ./internal/daemon ./internal/store ./cmd/cs
go test ./...
```

---

### Task 5: Context-Aware `csd serve`

**Files:**
- Modify: `cmd/csd/main.go`
- Create: `cmd/csd/run.go`
- Create: `cmd/csd/run_test.go`

- [ ] **Step 1: Extract daemon runner**

Create a function:

```go
func runServer(ctx context.Context, addr, statePath string, out io.Writer) error
```

It should:
- construct `http.Server`.
- call `Shutdown` on context cancellation.
- return non-nil errors from failed listen/start.
- treat `http.ErrServerClosed` as clean shutdown.

- [ ] **Step 2: Add signal handling**

Use `signal.NotifyContext` in `serve`.

- [ ] **Step 3: Test shutdown**

Unit test with `127.0.0.1:0` and a cancelled context. Assert the server exits.

- [ ] **Step 4: Verify**

Run:

```powershell
go test -count=1 ./cmd/csd
go test ./...
```

---

### Task 6: Windows Service Mode

**Files:**
- Modify: `go.mod`
- Create: `cmd/csd/service_windows.go`
- Create: `cmd/csd/service_stub.go`
- Modify: `cmd/csd/main.go`
- Modify: `README.md`

- [ ] **Step 1: Add `x/sys` dependency**

Run:

```powershell
go get golang.org/x/sys@latest
go mod tidy
```

Review `go.mod` and `go.sum`.

- [ ] **Step 2: Implement service runner behind build tags**

`service_windows.go`:
- use `svc.IsWindowsService` to decide service vs console execution.
- use `svc.Run` for service mode.
- use `debug.Run` for debug console mode if useful.
- log important lifecycle events to Windows Event Log when available.
- call the `runServer(ctx, ...)` function from Task 5.

`service_stub.go`:
- return the current friendly "not implemented on this platform" message for non-Windows install/uninstall/service commands.

- [ ] **Step 3: Install/uninstall commands**

Use `golang.org/x/sys/windows/svc/mgr` to:
- connect to service manager.
- create service named `codex-swarm-daemon`.
- set display name `Codex Swarm Daemon`.
- set description.
- pass executable path and `serve`.
- remove service on uninstall.

- [ ] **Step 4: Windows smoke**

On Windows, from an elevated shell:

```powershell
go build -trimpath ./cmd/csd
.\csd.exe install
Start-Service codex-swarm-daemon
csd status
Stop-Service codex-swarm-daemon
.\csd.exe uninstall
```

- [ ] **Step 5: Verify**

Run:

```powershell
go test ./...
go build -trimpath ./cmd/csd
```

On non-Windows CI, ensure build tags keep Windows service code out of normal builds.

---

## Risks And Guardrails

- Do not auto-dispatch from schedules in this plan. Schedules can call readiness later.
- Do not make GitHub the source of truth. Readiness reads GitHub; local state owns dispatch.
- Do not add daemon mutation APIs without request IDs and replay behavior.
- Do not add `kardianos/service` unless Windows, launchd, and systemd code all become complex enough to justify a cross-platform service abstraction.
- Keep service install privileged, explicit, and reversible.
- Keep daemon HTTP bound to loopback by default.

## Self-Review

Spec coverage:
- Issue #18 readiness is Task 1.
- Dispatch daemon direction is Tasks 2-4.
- Windows Go service patterns are captured in the service pattern decision and Task 6.

Placeholder scan:
- No unresolved placeholder markers.
- Every task has explicit files, command shapes, and verification commands.

Type consistency:
- Readiness package owns read-only preflight.
- Dispatch package owns explicit worker creation planning.
- Daemon stays read-only until Task 4 adds a bounded mutation surface.
