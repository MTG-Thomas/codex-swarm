package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/appserver"
	bf "github.com/MTG-Thomas/codex-swarm/internal/bifrost"
	"github.com/MTG-Thomas/codex-swarm/internal/coordination"
	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/launchbundle"
	"github.com/MTG-Thomas/codex-swarm/internal/ownership"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/remoteworkspace"
	"github.com/MTG-Thomas/codex-swarm/internal/repohints"
	"github.com/MTG-Thomas/codex-swarm/internal/snapshot"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
	"github.com/MTG-Thomas/codex-swarm/internal/transcript"
	"github.com/MTG-Thomas/codex-swarm/internal/workpacket"
	"github.com/MTG-Thomas/codex-swarm/internal/worktree"
)

type cli struct {
	out             io.Writer
	err             io.Writer
	now             func() time.Time
	appserverRunner appserverRunner
	remoteWorkspace remoteWorkspacePreparer
	bifrostRecords  bf.RecordStore
	bifrostRunner   bf.CommandRunner
}

type remoteWorkspacePreparer interface {
	Prepare(context.Context, remoteworkspace.Spec) (remoteworkspace.Result, error)
}

type appserverRunner interface {
	RunTurn(ctx context.Context, cwd, prompt string) (appserver.RunResult, error)
	SendTurn(ctx context.Context, cwd, threadID, prompt string) (appserver.RunResult, error)
	Resume(ctx context.Context, cwd, threadID string) (appserver.RunResult, error)
}

type appserverObservedRunner interface {
	RunTurnObserved(context.Context, string, string, appserver.TurnObserver) (appserver.RunResult, error)
	SendTurnObserved(context.Context, string, string, string, appserver.TurnObserver) (appserver.RunResult, error)
}

type appserverCoordinatedRunner interface {
	RunTurnCoordinated(context.Context, string, string, appserver.TurnObserver, appserver.SteeringPolicy) (appserver.RunResult, error)
	SendTurnCoordinated(context.Context, string, string, string, appserver.TurnObserver, appserver.SteeringPolicy) (appserver.RunResult, error)
}

func main() {
	c := cli{
		out: os.Stdout,
		err: os.Stderr,
		now: time.Now,
	}
	if err := c.run(os.Args[1:]); err != nil {
		fmt.Fprintf(c.err, "cs: %v\n", err)
		os.Exit(1)
	}
}

func (c cli) run(args []string) error {
	if len(args) == 0 {
		c.printUsage()
		return nil
	}

	switch args[0] {
	case "doctor":
		return c.doctor(args[1:])
	case "status":
		return c.status(args[1:])
	case "attention":
		return c.attention(args[1:])
	case "operation":
		return c.operation(args[1:])
	case "tasks":
		return c.codexTasks(args[1:])
	case "decision":
		return c.decision(args[1:])
	case "spawn":
		return c.spawn(args[1:])
	case "attach":
		return c.attach(args[1:])
	case "send":
		return c.send(args[1:])
	case "message":
		return c.message(args[1:])
	case "inbox":
		return c.inbox(args[1:])
	case "touch":
		return c.touch(args[1:])
	case "handoff":
		return c.handoff(args[1:])
	case "workpacket":
		return c.workpacket(args[1:])
	case "worker":
		return c.worker(args[1:])
	case "claim":
		return c.claim(args[1:])
	case "bifrost":
		return c.bifrost(args[1:])
	case "trace":
		return c.trace(args[1:])
	case "janitor":
		return c.janitor(args[1:])
	case "version":
		return c.version(args[1:])
	case "gate":
		return c.gate(args[1:])
	case "validate":
		return c.validate(args[1:])
	case "issue":
		return c.issue(args[1:])
	case "agent":
		return c.agent(args[1:])
	case "legacy":
		return c.legacy(args[1:])
	case "schedule":
		return c.schedule(args[1:])
	case "repo":
		return c.repo(args[1:])
	case "pr":
		return c.pr(args[1:])
	case "report":
		return c.report(args[1:])
	case "close":
		return c.closeWorker(args[1:])
	case "resume":
		return c.resume(args[1:])
	case "inspect-thread":
		return c.inspectThread(args[1:])
	case "transcript":
		return c.transcript(args[1:])
	case "show":
		return c.show(args[1:])
	default:
		c.printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (c cli) doctor(args []string) error {
	fs := c.flagSet("doctor")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	checkAppServer := fs.Bool("appserver", false, "start codex app-server and verify JSON-RPC initialize")
	daemonURL := fs.String("daemon", "", "daemon base URL to probe")
	timeout := fs.Duration("timeout", 15*time.Second, "app-server initialization timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	failures := 0
	check := func(name string, err error, detail string) {
		if err != nil {
			failures++
			fmt.Fprintf(c.out, "FAIL %s: %v\n", name, err)
			return
		}
		if detail != "" {
			fmt.Fprintf(c.out, "OK   %s: %s\n", name, detail)
			return
		}
		fmt.Fprintf(c.out, "OK   %s\n", name)
	}

	if path, err := exec.LookPath("go"); err != nil {
		check("go", err, "")
	} else {
		check("go", nil, path)
	}
	if path, err := exec.LookPath("git"); err != nil {
		check("git", err, "")
	} else {
		check("git", nil, path)
	}
	if path, err := exec.LookPath("codex"); err != nil {
		check("codex", err, "")
	} else {
		check("codex", nil, path)
	}

	stateDir := filepath.Dir(*statePath)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		check("state", err, "")
	} else {
		check("state", nil, *statePath)
	}

	baseURL := configuredDaemonURL(*daemonURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8787"
	}
	daemonCtx, daemonCancel := context.WithTimeout(context.Background(), 2*time.Second)
	daemonStatus, daemonErr := (daemon.Client{BaseURL: baseURL}).Status(daemonCtx)
	daemonCancel()
	if daemonErr != nil {
		fmt.Fprintf(c.out, "SKIP daemon: not reachable at %s (%v)\n", baseURL, daemonErr)
	} else {
		check("daemon", nil, daemonStatus.String())
	}

	repoRoot := ""
	repoOK := false
	resolvedRepo, err := filepath.Abs(*repo)
	if err != nil {
		check("repo", err, "")
	} else if info, statErr := os.Stat(resolvedRepo); statErr != nil {
		check("repo", statErr, "")
	} else if !info.IsDir() {
		check("repo", fmt.Errorf("not a directory: %s", resolvedRepo), "")
	} else {
		repoRoot = resolvedRepo
		repoOK = true
		check("repo", nil, repoRoot)
	}

	if *checkAppServer && repoOK {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		if err := (appserver.Runner{}).Check(ctx, repoRoot); err != nil {
			check("codex app-server", err, "")
		} else {
			check("codex app-server", nil, "initialize succeeded")
		}
	} else if *checkAppServer {
		fmt.Fprintln(c.out, "SKIP codex app-server: repo check failed")
	} else {
		fmt.Fprintln(c.out, "SKIP codex app-server: pass --appserver to start and initialize it")
	}

	if failures > 0 {
		return fmt.Errorf("doctor found %d failure(s)", failures)
	}
	return nil
}

func (c cli) status(args []string) error {
	return c.statusWorkers(args)
}

func (c cli) statusIssues(statePath string, detail bool) error {
	st := store.NewJSONStore(statePath)
	workers, err := st.ListWorkers()
	if err != nil {
		return err
	}
	claims, err := st.ListClaims()
	if err != nil {
		return err
	}
	now := c.now().UTC()
	rows := issueStatusRows(workers, now)
	activeClaims := activeDashboardClaims(claims)
	staleCount := 0
	for _, row := range rows {
		if row.Stale {
			staleCount++
		}
	}
	fmt.Fprintf(c.out, "issues=%d active_workers=%d stale_workers=%d active_claims=%d state=%s\n", countDashboardIssues(rows), len(rows), staleCount, len(activeClaims), statePath)
	for _, row := range rows {
		if !detail && !row.Stale && row.Next == "resume" {
			continue
		}
		fmt.Fprintf(c.out, "issue=%s worker=%s status=%s stale=%t next=%s\n", row.Issue, row.WorkerID, row.Status, row.Stale, row.Next)
	}
	for _, claim := range activeClaims {
		fmt.Fprintf(c.out, "claim=%s issue=%s worker=%s scope=%s next=release-or-sync\n", claim.ID, emptyDash(claim.Issue), emptyDash(claim.WorkerID), emptyDash(claim.Scope))
	}
	return nil
}

type issueDashboardRow struct {
	Issue    string
	WorkerID string
	Status   string
	Stale    bool
	Next     string
}

func issueStatusRows(workers []store.Worker, now time.Time) []issueDashboardRow {
	rows := []issueDashboardRow(nil)
	for _, worker := range workers {
		if strings.TrimSpace(worker.Issue) == "" || isTerminalWorker(worker) {
			continue
		}
		stale := workerIsStale(worker, now)
		rows = append(rows, issueDashboardRow{
			Issue:    worker.Issue,
			WorkerID: worker.ID,
			Status:   displayWorkerStatus(worker),
			Stale:    stale,
			Next:     issueDashboardNext(worker, stale),
		})
	}
	return rows
}

func activeDashboardClaims(claims []store.Claim) []store.Claim {
	active := []store.Claim(nil)
	for _, claim := range claims {
		if claim.Status == store.ClaimActive {
			active = append(active, claim)
		}
	}
	return active
}

func countDashboardIssues(rows []issueDashboardRow) int {
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Issue] = true
	}
	return len(seen)
}

func workerIsStale(worker store.Worker, now time.Time) bool {
	if worker.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(worker.UpdatedAt.UTC()) > 24*time.Hour
}

func issueDashboardNext(worker store.Worker, stale bool) string {
	if stale {
		return "resume-or-release"
	}
	switch displayWorkerStatus(worker) {
	case string(store.WorkerRunning), "working":
		return "monitor"
	case string(store.WorkerPending), string(store.WorkerIdle):
		return "resume"
	default:
		return "inspect"
	}
}

func isTerminalWorker(worker store.Worker) bool {
	switch displayWorkerStatus(worker) {
	case string(store.WorkerDone), string(store.WorkerFailed):
		return true
	default:
		return false
	}
}

func (c cli) spawn(args []string) error {
	fs := c.flagSet("spawn")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	prompt := fs.String("prompt", "", "worker prompt")
	engine := fs.String("engine", "mock", "worker engine: mock or appserver")
	role := fs.String("role", "", "worker role, for example implementer, reviewer, tester, or docs")
	parentID := fs.String("parent", "", "parent worker id")
	issueValue := fs.String("issue", "", "GitHub issue reference, for example owner/repo#123")
	createWorktree := fs.Bool("worktree", false, "create the worker Git worktree")
	remoteHost := fs.String("remote-host", "", "SSH target for a remote app-server workspace")
	remoteJump := fs.String("remote-jump", "", "optional SSH jump host")
	remoteRoot := fs.String("remote-root", "", "remote workspace root under the remote user's home")
	remoteRepoURL := fs.String("remote-repo-url", "", "repository URL used by the remote host (defaults to local origin)")
	remoteCodex := fs.String("remote-codex", "codex", "Codex executable on the remote host")
	remoteBase := fs.String("remote-base", "main", "Git base ref for the remote worktree")
	timeout := fs.Duration("timeout", 2*time.Minute, "app-server request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*prompt) == "" {
		return errors.New("spawn requires --prompt")
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	now := c.now().UTC()
	id, err := newWorkerID(now)
	if err != nil {
		return fmt.Errorf("generate worker id: %w", err)
	}
	managedName := id
	managedWorktree := filepath.Join(repoRoot, ".codex-swarm", "worktrees", managedName)
	managedBranch := "cs/" + managedName
	if *engine != "mock" && *engine != "appserver" {
		return fmt.Errorf("unknown engine %q", *engine)
	}
	remoteRequested := strings.TrimSpace(*remoteHost) != ""
	if remoteRequested && (*engine != "appserver" || !*createWorktree) {
		return errors.New("--remote-host requires --engine appserver --worktree")
	}
	if !remoteRequested && (strings.TrimSpace(*remoteJump) != "" || strings.TrimSpace(*remoteRoot) != "" || strings.TrimSpace(*remoteRepoURL) != "" || *remoteCodex != "codex" || *remoteBase != "main") {
		return errors.New("remote workspace flags require --remote-host")
	}
	issue := ""
	if strings.TrimSpace(*issueValue) != "" {
		ref, err := gh.ParseIssueRef(*issueValue)
		if err != nil {
			return err
		}
		issue = ref.String()
	}

	threadID := fmt.Sprintf("mock-thread-%s", id)
	turnID := ""
	lastMessage := mockSummary(*prompt)
	status := store.WorkerIdle
	var appserverResult appserver.RunResult
	activePersisted := false
	events := []store.Event{{At: now, Type: "spawned", Message: "worker created"}}
	worker := store.Worker{
		ID:          id,
		ParentID:    *parentID,
		Role:        *role,
		Issue:       issue,
		ProjectRoot: repoRoot,
		ThreadID:    threadID,
		TurnID:      turnID,
		Engine:      *engine,
		Status:      status,
		Prompt:      *prompt,
		LastMessage: lastMessage,
		CreatedAt:   now,
		UpdatedAt:   now,
		Events:      events,
	}
	if worker.Engine == "appserver" {
		worker.RuntimeOwner = store.RuntimeOwnerCS
	}

	worktreeReady := false
	if *createWorktree {
		worker.Worktree = managedWorktree
		worker.Branch = managedBranch
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		if remoteRequested {
			repoURL := strings.TrimSpace(*remoteRepoURL)
			if repoURL == "" {
				var err error
				repoURL, err = gitConfigValue(ctx, repoRoot, "remote.origin.url")
				if err != nil {
					return fmt.Errorf("resolve remote repository URL: %w", err)
				}
			}
			gitName, _ := gitConfigValue(ctx, repoRoot, "user.name")
			gitEmail, _ := gitConfigValue(ctx, repoRoot, "user.email")
			if gitName == "" {
				gitName = "Codex Swarm"
			}
			if gitEmail == "" {
				gitEmail = "codex-swarm@local.invalid"
			}
			provider := c.remoteWorkspace
			if provider == nil {
				provider = remoteworkspace.SSH{Target: *remoteHost, Jump: *remoteJump}
			}
			result, err := provider.Prepare(ctx, remoteworkspace.Spec{
				WorkerID: worker.ID,
				RepoURL:  repoURL,
				BaseRef:  *remoteBase,
				Branch:   worker.Branch,
				Root:     *remoteRoot,
				GitName:  gitName,
				GitEmail: gitEmail,
			})
			if err != nil {
				return err
			}
			worker.Worktree = result.Path
			worker.Remote = &store.RemoteExecution{
				Host:        *remoteHost,
				JumpHost:    *remoteJump,
				CodexBinary: *remoteCodex,
				RepoURL:     result.RepoURL,
				BaseRef:     result.BaseRef,
			}
			worker.Events = append(worker.Events, store.Event{At: now, Type: "remote.worktree.created", Message: worker.Worktree})
		} else {
			result, err := (worktree.Git{}).Create(ctx, worktree.Spec{
				RepoRoot: repoRoot,
				Branch:   worker.Branch,
				Path:     worker.Worktree,
			})
			if err != nil {
				return fmt.Errorf("create worktree: %w", err)
			}
			for _, warning := range result.Warnings {
				worker.Events = append(worker.Events, store.Event{At: now, Type: "worktree.warning", Message: warning})
			}
			worker.Events = append(worker.Events, store.Event{At: now, Type: "worktree.created", Message: worker.Worktree})
		}
		worktreeReady = true
	}

	if *engine == "appserver" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		appserverPrompt := *prompt
		if worker.Issue != "" {
			bundle, err := launchbundle.BuildIssue(ctx, launchbundle.Input{
				Worker:          worker,
				UserPrompt:      *prompt,
				Store:           store.NewJSONStore(*statePath),
				IncludeWorktree: worktreeReady,
			})
			if err != nil {
				worker.ApplyStatusAt(store.WorkerFailed, now)
				worker.LastMessage = "app-server launch bundle failed: " + err.Error()
				worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.launch.bundle.failed", Message: worker.LastMessage})
				if saveErr := store.NewJSONStore(*statePath).SaveWorker(worker); saveErr != nil {
					return errors.Join(
						fmt.Errorf("build app-server launch bundle for worker=%s thread=%s repo=%s worktree=%s issue=%s: %w", worker.ID, worker.ThreadID, worker.ProjectRoot, worker.Worktree, worker.Issue, err),
						fmt.Errorf("save failed app-server worker: %w", saveErr),
					)
				}
				return fmt.Errorf("build app-server launch bundle for worker=%s thread=%s repo=%s worktree=%s issue=%s: %w", worker.ID, worker.ThreadID, worker.ProjectRoot, worker.Worktree, worker.Issue, err)
			}
			appserverPrompt = bundle.Prompt
			worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.launch.bundle", Message: bundle.Source})
		}
		result, err := c.runTurnObserved(c.runnerFor(worker), ctx, workerExecutionRoot(worker), appserverPrompt, func(started appserver.RunResult) error {
			worker.ThreadID = started.ThreadID
			worker.TurnID = started.TurnID
			worker.ApplyStatusAt(store.WorkerRunning, c.now().UTC())
			worker.LastMessage = fmt.Sprintf("app-server turn active: thread=%s turn=%s", started.ThreadID, started.TurnID)
			worker.Events = append(worker.Events, store.Event{At: c.now().UTC(), Type: "appserver.turn.started", Message: worker.LastMessage})
			if err := store.NewJSONStore(*statePath).SaveWorker(worker); err != nil {
				return err
			}
			activePersisted = true
			return nil
		}, c.workerSteeringPolicy(*statePath, worker.ID))
		if err != nil {
			failedAt := c.now().UTC()
			failure := "app-server spawn failed: " + err.Error()
			var saveErr error
			if activePersisted {
				_, saveErr = store.NewJSONStore(*statePath).UpdateWorker(worker.ID, func(current *store.Worker) error {
					current.ApplyStatusAt(store.WorkerFailed, failedAt)
					current.LastMessage = failure
					current.Events = append(current.Events, store.Event{At: failedAt, Type: "appserver.spawn.failed", Message: failure})
					return nil
				})
			} else {
				worker.ApplyStatusAt(store.WorkerFailed, failedAt)
				worker.LastMessage = failure
				worker.Events = append(worker.Events, store.Event{At: failedAt, Type: "appserver.spawn.failed", Message: failure})
				saveErr = store.NewJSONStore(*statePath).SaveWorker(worker)
			}
			if saveErr != nil {
				return errors.Join(fmt.Errorf("run app-server worker: %w", err), fmt.Errorf("save failed app-server worker: %w", saveErr))
			}
			return fmt.Errorf("run app-server worker: %w", err)
		}
		completedAt := c.now().UTC()
		threadID = result.ThreadID
		turnID = result.TurnID
		status = workerStatusFromTurn(result.Status)
		lastMessage = fmt.Sprintf("app-server turn submitted: thread=%s turn=%s status=%s", result.ThreadID, result.TurnID, result.Status)
		if !activePersisted {
			worker.Events = append(worker.Events, store.Event{At: completedAt, Type: "appserver.turn.started", Message: lastMessage})
		}
		worker.Events = appendAppserverWarnings(worker.Events, completedAt, result.Warnings)
		worker.Events = append(worker.Events, store.Event{At: completedAt, Type: "appserver.turn.completed", Message: lastMessage})
		worker.Events = appendAppserverFinalMessage(worker.Events, completedAt, result.FinalMessage)
		appserverResult = result
		now = completedAt
	} else {
		worker.Events = append(worker.Events, store.Event{At: now, Type: "mock.turn.completed", Message: lastMessage})
	}
	worker.ThreadID = threadID
	worker.TurnID = turnID
	worker.Status = status
	worker.LastMessage = lastMessage
	worker.ApplyStatusAt(status, now)

	st := store.NewJSONStore(*statePath)
	if activePersisted {
		updated, err := st.UpdateWorker(worker.ID, func(current *store.Worker) error {
			current.ThreadID = worker.ThreadID
			current.TurnID = worker.TurnID
			current.ApplyStatusAt(worker.Status, now)
			current.LastMessage = worker.LastMessage
			current.Events = appendAppserverWarnings(current.Events, now, appserverResult.Warnings)
			current.Events = append(current.Events, store.Event{At: now, Type: "appserver.turn.completed", Message: worker.LastMessage})
			current.Events = appendAppserverFinalMessage(current.Events, now, appserverResult.FinalMessage)
			return nil
		})
		if err != nil {
			return err
		}
		worker = updated
	} else if err := st.SaveWorker(worker); err != nil {
		return err
	}
	saved, err := st.GetWorker(worker.ID)
	if err != nil {
		return err
	}
	worker = saved
	if worker.Engine == "appserver" && len(appserverResult.FileChanges) > 0 {
		if err := c.recordAppserverFileChanges(*statePath, worker, appserverResult.FileChanges); err != nil {
			return fmt.Errorf("record app-server file changes for worker %s: %w", worker.ID, err)
		}
	}
	fmt.Fprintf(c.out, "spawned %s engine=%s thread=%s status=%s\n", worker.ID, worker.Engine, worker.ThreadID, displayWorkerStatus(worker))
	if worker.Role != "" || worker.ParentID != "" {
		fmt.Fprintf(c.out, "swarm: role=%s parent=%s\n", emptyDash(worker.Role), emptyDash(worker.ParentID))
	}
	if worker.Issue != "" {
		fmt.Fprintf(c.out, "issue: %s\n", worker.Issue)
	}
	if err := c.printRepoHints(repoRoot); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s\n", worker.LastMessage)
	if worker.Engine == "appserver" {
		fmt.Fprintf(c.out, "codex thread: %s\n", worker.ThreadID)
		fmt.Fprintf(c.out, "inspect: cs inspect-thread --state %s %s\n", *statePath, worker.ID)
		fmt.Fprintln(c.out, "note: Codex app visibility can lag briefly, especially on mobile.")
		printWarnings(c.out, appserverWarnings(worker.Events))
	}
	if *createWorktree {
		fmt.Fprintf(c.out, "worktree: %s branch=%s\n", worker.Worktree, worker.Branch)
		if worker.Remote != nil {
			fmt.Fprintf(c.out, "remote: host=%s jump=%s base=%s\n", worker.Remote.Host, emptyDash(worker.Remote.JumpHost), worker.Remote.BaseRef)
		}
		for _, event := range worker.Events {
			if event.Type == "worktree.warning" {
				fmt.Fprintf(c.out, "warning: %s\n", event.Message)
			}
		}
	}
	return nil
}

func (c cli) repo(args []string) error {
	if len(args) == 0 {
		return errors.New("repo requires <hints>")
	}
	switch args[0] {
	case "hints":
		return c.repoHints(args[1:])
	default:
		return fmt.Errorf("unknown repo command %q", args[0])
	}
}

func (c cli) repoHints(args []string) error {
	fs := c.flagSet("repo hints")
	repo := fs.String("repo", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	hints, source, ok, err := repohints.Load(repoRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "repo=%s\n", repoRoot)
	if !ok {
		fmt.Fprintf(c.out, "hints=0\nchecked=%s,%s\n", repohints.CommittedFile, repohints.LocalFile)
		return nil
	}
	fmt.Fprintf(c.out, "hints=1 source=%s local=%t\n", source.Path, source.Local)
	for _, line := range hints.Lines() {
		fmt.Fprintln(c.out, line)
	}
	return nil
}

func (c cli) printRepoHints(repoRoot string) error {
	hints, _, ok, err := repohints.Load(repoRoot)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for _, line := range hints.Lines() {
		fmt.Fprintln(c.out, line)
	}
	return nil
}

func randomSuffix(bytesLen int) (string, error) {
	if bytesLen <= 0 {
		return "", nil
	}
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newWorkerID(now time.Time) (string, error) {
	suffix, err := randomSuffix(4)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("w-%s-%s", now.UTC().Format("20060102-150405"), suffix), nil
}

func (c cli) requestID(value string, now time.Time) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" {
		return value, nil
	}
	suffix, err := randomSuffix(6)
	if err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}
	return fmt.Sprintf("r-%s-%s", now.UTC().Format("20060102-150405"), suffix), nil
}

func (c cli) schedule(args []string) error {
	if len(args) == 0 {
		return errors.New("schedule requires <add|list>")
	}
	switch args[0] {
	case "add":
		return c.scheduleAdd(args[1:])
	case "list":
		return c.scheduleList(args[1:])
	default:
		return fmt.Errorf("unknown schedule command %q", args[0])
	}
}

func (c cli) scheduleAdd(args []string) error {
	fs := c.flagSet("schedule add")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	prompt := fs.String("prompt", "", "scheduled prompt")
	cron := fs.String("cron", "", "cron expression")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*prompt) == "" {
		return errors.New("schedule add requires --prompt")
	}
	if strings.TrimSpace(*cron) == "" {
		return errors.New("schedule add requires --cron")
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	now := c.now().UTC()
	schedule := store.Schedule{
		ID:        fmt.Sprintf("s-%s", now.Format("20060102-150405")),
		Repo:      repoRoot,
		Prompt:    *prompt,
		Cron:      *cron,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.NewJSONStore(*statePath).SaveSchedule(schedule); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "schedule %s cron=%q repo=%s\n", schedule.ID, schedule.Cron, schedule.Repo)
	return nil
}

func (c cli) scheduleList(args []string) error {
	fs := c.flagSet("schedule list")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	schedules, err := store.NewJSONStore(*statePath).ListSchedules()
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "schedules=%d state=%s\n", len(schedules), *statePath)
	for _, schedule := range schedules {
		fmt.Fprintf(c.out, "%s\tenabled=%t\t%s\t%s\n", schedule.ID, schedule.Enabled, schedule.Cron, short(schedule.Prompt, 60))
	}
	return nil
}

func (c cli) message(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "confirm-steered":
			return c.updateNativeSteering(args[1:], true)
		case "steering-failed":
			return c.updateNativeSteering(args[1:], false)
		}
	}
	fs := c.flagSet("message")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	requestIDFlag := fs.String("request-id", "", "idempotency key for this mutation")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	subtree := fs.Bool("subtree", false, "deliver to the target worker and its descendants")
	wait := fs.Duration("wait", 0, "wait up to this duration for delivery readback")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 3 {
		return errors.New("message requires <from-worker> <to-worker> <message>")
	}
	fromID := rest[0]
	toID := rest[1]
	text := strings.Join(rest[2:], " ")
	if strings.TrimSpace(text) == "" {
		return errors.New("message text is required")
	}
	now := c.now().UTC()
	requestID, err := c.requestID(*requestIDFlag, now)
	if err != nil {
		return err
	}
	kind := store.MessageDirect
	if *subtree {
		kind = store.MessageSubtree
	}
	request := protocol.MessageRequest{RequestID: requestID, Kind: kind, From: fromID, To: toID, Body: text}
	var response protocol.MessageResponse
	if baseURL := configuredDaemonURL(*daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := daemon.Client{BaseURL: baseURL}
		response, err = client.Message(ctx, request)
		if err == nil && len(response.NativeSteering) > 0 && strings.TrimSpace(response.NativeSteering[0].StatePath) == "" {
			var status daemon.Status
			status, err = client.Status(ctx)
			if err == nil {
				for i := range response.NativeSteering {
					response.NativeSteering[i].StatePath = status.StatePath
				}
			}
		}
	} else {
		result, sendErr := (coordination.Service{Store: store.NewJSONStore(*statePath), Now: c.now}).Send(context.Background(), coordination.SendRequest{
			RequestID: request.RequestID, Kind: request.Kind, From: request.From, To: request.To, Body: request.Body,
		})
		err = sendErr
		response = protocol.MessageResponse{Message: result.Message, Deliveries: result.Deliveries, NativeSteering: result.NativeSteering, Replayed: result.Replayed}
		for i := range response.NativeSteering {
			response.NativeSteering[i].StatePath = *statePath
		}
	}
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("message worker not found: %w", err)
		}
		return err
	}
	if *wait < 0 {
		return errors.New("message --wait must not be negative")
	}
	if *wait > 0 && !response.Replayed {
		response.Deliveries, err = waitForDeliveryReadback(*statePath, *daemonURL, response.Message.ID, response.Deliveries, *wait)
		if err != nil {
			return err
		}
	}
	if *jsonOutput {
		return json.NewEncoder(c.out).Encode(response)
	}
	fmt.Fprintf(c.out, "message %s -> %s id=%s kind=%s request=%s deliveries=%d replayed=%t\n", fromID, toID, response.Message.ID, kind, response.Message.RequestID, len(response.Deliveries), response.Replayed)
	for _, delivery := range response.Deliveries {
		fmt.Fprintf(c.out, "delivery=%s\trecipient=%s\tstate=%s\tcreated=%s\tupdated=%s\terror=%s\n",
			delivery.ID, delivery.RecipientID, delivery.State, delivery.CreatedAt.Format(time.RFC3339Nano), delivery.UpdatedAt.Format(time.RFC3339Nano), emptyDash(delivery.LastError))
		for _, event := range delivery.History {
			fmt.Fprintf(c.out, "  transition=%d\tstate=%s\tat=%s\terror=%s\n", event.Sequence, event.State, event.CreatedAt.Format(time.RFC3339Nano), emptyDash(event.LastError))
		}
	}
	for _, request := range response.NativeSteering {
		fmt.Fprintf(c.out, "native_steering_required delivery=%s recipient=%s host=%s thread=%s turn=%s\n",
			request.DeliveryID, request.RecipientID, emptyDash(request.HostID), request.ThreadID, request.TurnID)
		prompt, _ := json.Marshal(request.Prompt)
		fmt.Fprintf(c.out, "  prompt=%s\n", prompt)
		fmt.Fprintf(c.out, "  after_success=cs message confirm-steered --state %q --worker %s --thread %s --turn %s %s\n",
			request.StatePath, request.RecipientID, request.ThreadID, request.TurnID, request.DeliveryID)
		fmt.Fprintf(c.out, "  after_failure=cs message steering-failed --state %q --worker %s --thread %s --turn %s --error <error> %s\n",
			request.StatePath, request.RecipientID, request.ThreadID, request.TurnID, request.DeliveryID)
	}
	return nil
}

func (c cli) updateNativeSteering(args []string, succeeded bool) error {
	command := "confirm-steered"
	if !succeeded {
		command = "steering-failed"
	}
	fs := c.flagSet("message " + command)
	statePath := fs.String("state", defaultStatePath(), "state file path")
	workerID := fs.String("worker", "", "recipient worker id")
	threadID := fs.String("thread", "", "thread id used for native steering")
	turnID := fs.String("turn", "", "turn id used for native steering")
	steeringError := fs.String("error", "", "native steering error; required for steering-failed")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*workerID) == "" || strings.TrimSpace(*threadID) == "" || strings.TrimSpace(*turnID) == "" {
		return fmt.Errorf("message %s requires --worker, --thread, --turn, and <delivery-id>", command)
	}
	if !succeeded && strings.TrimSpace(*steeringError) == "" {
		return errors.New("message steering-failed requires --error")
	}
	deliveryID := fs.Arg(0)
	st := store.NewJSONStore(*statePath)
	worker, err := st.GetWorker(*workerID)
	if err != nil {
		return fmt.Errorf("confirm native steering worker %s: %w", *workerID, err)
	}
	if worker.ThreadID != *threadID || worker.TurnID != *turnID {
		return fmt.Errorf("refuse native steering confirmation for %s: worker runtime is thread=%s turn=%s, confirmation expected thread=%s turn=%s",
			deliveryID, emptyDash(worker.ThreadID), emptyDash(worker.TurnID), *threadID, *turnID)
	}
	item, err := findWorkerDelivery(st, worker.ID, deliveryID)
	if err != nil {
		return err
	}
	if !succeeded && item.Delivery.State != store.DeliveryQueued {
		return fmt.Errorf("refuse native steering failure for %s: delivery state is %s", deliveryID, item.Delivery.State)
	}
	if item.Delivery.State == store.DeliveryQueued {
		state := store.DeliverySteered
		lastError := ""
		if !succeeded {
			state = store.DeliveryQueued
			lastError = strings.TrimSpace(*steeringError)
		}
		if err := st.UpdateDelivery(deliveryID, state, lastError, c.now().UTC()); err != nil {
			return err
		}
		item, err = findWorkerDelivery(st, worker.ID, deliveryID)
		if err != nil {
			return err
		}
	}
	if *jsonOutput {
		return json.NewEncoder(c.out).Encode(item)
	}
	verb := "confirmed"
	if !succeeded {
		verb = "recorded failure for"
	}
	fmt.Fprintf(c.out, "%s native steering delivery=%s recipient=%s thread=%s turn=%s state=%s error=%s\n",
		verb, item.Delivery.ID, worker.ID, worker.ThreadID, worker.TurnID, item.Delivery.State, emptyDash(item.Delivery.LastError))
	return nil
}

func findWorkerDelivery(st *store.JSONStore, workerID, deliveryID string) (store.DeliveredMessage, error) {
	items, err := st.ListMessages(workerID)
	if err != nil {
		return store.DeliveredMessage{}, err
	}
	for _, item := range items {
		if item.Delivery.ID == deliveryID {
			return item, nil
		}
	}
	return store.DeliveredMessage{}, fmt.Errorf("delivery %s not found for worker %s", deliveryID, workerID)
}

func waitForDeliveryReadback(statePath, daemonURL, messageID string, original []store.Delivery, wait time.Duration) ([]store.Delivery, error) {
	deadline := time.Now().Add(wait)
	latest := original
	for {
		settled := len(latest) > 0
		for _, delivery := range latest {
			if delivery.State == store.DeliveryQueued && delivery.LastError == "" {
				settled = false
				break
			}
		}
		if settled || !time.Now().Before(deadline) {
			return latest, nil
		}
		time.Sleep(min(100*time.Millisecond, time.Until(deadline)))
		byID := make(map[string]store.Delivery, len(latest))
		for _, delivery := range latest {
			byID[delivery.ID] = delivery
		}
		for _, delivery := range latest {
			messages, err := loadInbox(statePath, daemonURL, delivery.RecipientID)
			if err != nil {
				return nil, fmt.Errorf("read delivery %s: %w", delivery.ID, err)
			}
			for _, item := range messages {
				if item.Message.ID == messageID && item.Delivery.ID == delivery.ID {
					byID[delivery.ID] = item.Delivery
					break
				}
			}
		}
		latest = latest[:0]
		for _, delivery := range original {
			latest = append(latest, byID[delivery.ID])
		}
	}
}

func (c cli) handoff(args []string) error {
	fs := c.flagSet("handoff")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	requestIDFlag := fs.String("request-id", "", "idempotency key for this mutation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 3 {
		return errors.New("handoff requires <from-worker> <to-worker> <summary>")
	}
	fromID := rest[0]
	toID := rest[1]
	summary := strings.Join(rest[2:], " ")
	if strings.TrimSpace(summary) == "" {
		return errors.New("handoff summary is required")
	}
	now := c.now().UTC()
	requestID, err := c.requestID(*requestIDFlag, now)
	if err != nil {
		return err
	}
	fingerprint := mutationFingerprint("handoff", fromID, toID, summary)
	s := store.NewJSONStore(*statePath)
	result, _, err := s.UpdateWorkersWithRequest(requestID, "handoff", fingerprint, []string{fromID, toID}, func(workers map[string]*store.Worker) (store.WorkerMutationResult, error) {
		from := workers[fromID]
		to := workers[toID]
		event := store.Event{At: now, Type: "handoff", Message: summary, From: from.ID, To: to.ID, Issue: eventIssue(from, to), WorkerID: from.ID, RequestID: requestID}
		from.ApplyStatus(store.WorkerIdle)
		from.Report = "handoff to " + to.ID + ": " + summary
		from.Events = append(from.Events, store.Event{At: now, Type: "handoff.sent", Message: from.Report, From: from.ID, To: to.ID, Issue: event.Issue, WorkerID: from.ID, RequestID: requestID})
		from.UpdatedAt = now
		to.Events = append(to.Events, store.Event{At: now, Type: "handoff.received", Message: fmt.Sprintf("from=%s %s", from.ID, summary), From: from.ID, To: to.ID, Issue: event.Issue, WorkerID: to.ID, RequestID: requestID})
		to.UpdatedAt = now
		return store.WorkerMutationResult{
			Fingerprint: fingerprint,
			Output:      fmt.Sprintf("handoff %s -> %s request=%s\n", from.ID, to.ID, requestID),
			Events:      []store.Event{event},
		}, nil
	})
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("handoff worker not found: %w", err)
		}
		return err
	}
	fmt.Fprint(c.out, result.Output)
	return nil
}

func (c cli) send(args []string) error {
	fs := c.flagSet("send")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	timeout := fs.Duration("timeout", 2*time.Minute, "app-server request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("send requires <worker> <message>")
	}
	id := rest[0]
	message := strings.Join(rest[1:], " ")
	var appserverResult appserver.RunResult
	var appserverErr error
	st := store.NewJSONStore(*statePath)
	initial, err := st.GetWorker(id)
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", id)
		}
		return err
	}
	queued, err := st.ListQueuedMessages(id)
	if err != nil {
		return fmt.Errorf("load queued messages for worker %s: %w", id, err)
	}
	deliveryPrompt := queuedMessagePrompt(queued)
	if deliveryPrompt != "" {
		message = deliveryPrompt + "\n\nUSER_MESSAGE\n" + message
	}
	if initial.Engine == "appserver" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		prompt := message
		snap, snapErr := workerSnapshot(st, initial, c.now().UTC())
		if snapErr != nil {
			return snapErr
		}
		prompt = appserverPromptWithSnapshot(snap, message)
		appserverResult, appserverErr = c.sendTurnObserved(c.runnerFor(initial), ctx, workerExecutionRoot(initial), initial.ThreadID, prompt, func(started appserver.RunResult) error {
			_, err := st.UpdateWorker(id, func(worker *store.Worker) error {
				at := c.now().UTC()
				worker.ThreadID = started.ThreadID
				worker.TurnID = started.TurnID
				worker.ApplyStatusAt(store.WorkerRunning, at)
				worker.LastMessage = fmt.Sprintf("app-server turn active: thread=%s turn=%s", started.ThreadID, started.TurnID)
				worker.Events = append(worker.Events, store.Event{At: at, Type: "appserver.turn.started", Message: worker.LastMessage})
				return nil
			})
			return err
		}, c.workerSteeringPolicy(*statePath, id, queuedDeliveryIDs(queued)...))
	}
	err = c.updateWorker(*statePath, id, func(worker *store.Worker, now time.Time) {
		worker.Events = append(worker.Events, store.Event{At: now, Type: "message.sent", Message: message})
		if worker.Engine == "appserver" {
			if appserverErr != nil {
				worker.ApplyStatusAt(store.WorkerFailed, now)
				worker.LastMessage = "app-server send failed: " + appserverErr.Error()
				worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.turn.failed", Message: worker.LastMessage})
				return
			}
			worker.TurnID = appserverResult.TurnID
			worker.ApplyStatusAt(workerStatusFromTurn(appserverResult.Status), now)
			worker.LastMessage = fmt.Sprintf("app-server turn submitted: thread=%s turn=%s status=%s", appserverResult.ThreadID, appserverResult.TurnID, appserverResult.Status)
			worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.turn.completed", Message: worker.LastMessage})
			worker.Events = appendAppserverWarnings(worker.Events, now, appserverResult.Warnings)
			worker.Events = appendAppserverFinalMessage(worker.Events, now, appserverResult.FinalMessage)
			return
		}
		worker.ApplyStatus(store.WorkerIdle)
		worker.LastMessage = mockSummary(message)
		worker.Events = append(worker.Events, store.Event{At: now, Type: "mock.turn.completed", Message: worker.LastMessage})
	}, func(worker store.Worker) {
		fmt.Fprintf(c.out, "sent %s status=%s\n%s\n", worker.ID, displayWorkerStatus(worker), worker.LastMessage)
		printWarnings(c.out, appserverResult.Warnings)
	})
	if err != nil {
		return err
	}
	if initial.Engine != "appserver" || appserverErr == nil {
		if err := markMessagesDelivered(st, queued, c.now().UTC()); err != nil {
			return fmt.Errorf("mark queued messages delivered for worker %s: %w", id, err)
		}
	}
	if initial.Engine == "appserver" && appserverErr == nil && len(appserverResult.FileChanges) > 0 {
		updated, err := st.GetWorker(id)
		if err != nil {
			return err
		}
		if err := c.recordAppserverFileChanges(*statePath, updated, appserverResult.FileChanges); err != nil {
			return fmt.Errorf("record app-server file changes for worker %s: %w", id, err)
		}
	}
	return nil
}

func appserverPromptWithSnapshot(snap snapshot.Snapshot, message string) string {
	return snap.Text() + "\n\nUSER_MESSAGE\n" + strings.TrimSpace(message)
}

func (c cli) report(args []string) error {
	fs := c.flagSet("report")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	note := fs.String("note", "", "report note")
	daemonURL := fs.String("daemon", "", "daemon base URL")
	requestIDFlag := fs.String("request-id", "", "idempotency key for completion forwarding")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("report requires <worker> <done|blocked|failed>")
	}
	id := rest[0]
	report := strings.Join(rest[1:], " ")
	if *note != "" {
		report = report + ": " + *note
	}
	err := c.updateWorker(*statePath, id, func(worker *store.Worker, now time.Time) {
		switch rest[1] {
		case "done", "completed":
			worker.ApplyStatusAt(store.WorkerDone, now)
			if worker.Role == "validator" {
				worker.ValidationStatus = ValidationApproved
			}
		case "failed":
			worker.ApplyStatusAt(store.WorkerFailed, now)
			if worker.Role == "validator" {
				worker.ValidationStatus = ValidationRejected
			}
		default:
			worker.ApplyStatus(store.WorkerIdle)
		}
		worker.Report = report
		worker.Events = append(worker.Events, store.Event{At: now, Type: "reported", Message: report})
	}, func(worker store.Worker) {
		fmt.Fprintf(c.out, "reported %s status=%s report=%q\n", worker.ID, displayWorkerStatus(worker), worker.Report)
	})
	if err != nil {
		return err
	}
	if rest[1] != "done" && rest[1] != "completed" && rest[1] != "failed" {
		return nil
	}
	requestID, err := c.requestID(*requestIDFlag, c.now().UTC())
	if err != nil {
		return err
	}
	return c.forwardCompletion(*statePath, *daemonURL, requestID, id, report)
}

func (c cli) resume(args []string) error {
	fs := c.flagSet("resume")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	timeout := fs.Duration("timeout", 2*time.Minute, "app-server request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("resume requires <worker>")
	}
	var appserverResult appserver.RunResult
	var appserverErr error
	initial, err := store.NewJSONStore(*statePath).GetWorker(rest[0])
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", rest[0])
		}
		return err
	}
	if initial.Engine == "appserver" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		appserverResult, appserverErr = c.runnerFor(initial).Resume(ctx, workerExecutionRoot(initial), initial.ThreadID)
	}
	return c.updateWorker(*statePath, rest[0], func(worker *store.Worker, now time.Time) {
		if worker.Engine == "appserver" {
			if appserverErr != nil {
				worker.ApplyStatusAt(store.WorkerFailed, now)
				worker.LastMessage = "app-server resume failed: " + appserverErr.Error()
				worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.resume.failed", Message: worker.LastMessage})
				return
			}
			worker.ThreadID = appserverResult.ThreadID
			worker.ApplyStatus(store.WorkerIdle)
			worker.LastMessage = "app-server thread resumed: " + appserverResult.ThreadID
			worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.thread.resumed", Message: worker.LastMessage})
			return
		}
		worker.ApplyStatus(store.WorkerIdle)
		worker.Events = append(worker.Events, store.Event{At: now, Type: "resume.requested", Message: "resume requested"})
	}, func(worker store.Worker) {
		fmt.Fprintf(c.out, "resume %s thread=%s status=%s\n", worker.ID, worker.ThreadID, displayWorkerStatus(worker))
	})
}

func (c cli) inspectThread(args []string) error {
	fs := c.flagSet("inspect-thread")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	timeout := fs.Duration("timeout", 2*time.Minute, "app-server request timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("inspect-thread requires <worker>")
	}
	worker, err := store.NewJSONStore(*statePath).GetWorker(rest[0])
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", rest[0])
		}
		return err
	}
	if worker.Engine != "appserver" {
		fmt.Fprintf(c.out, "worker %s uses engine=%s; no Codex app-server thread to inspect\n", worker.ID, worker.Engine)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := c.runnerFor(worker).Resume(ctx, workerExecutionRoot(worker), worker.ThreadID)
	if err != nil {
		return fmt.Errorf("inspect app-server thread %s: %w", worker.ThreadID, err)
	}
	fmt.Fprintf(c.out, "thread=%s status=%s worker=%s\n", result.ThreadID, result.Status, worker.ID)
	fmt.Fprintln(c.out, "Codex app visibility can lag briefly after thread creation or updates.")
	return nil
}

func (c cli) show(args []string) error {
	fs := c.flagSet("show")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	printSnapshot := fs.Bool("snapshot", false, "print compact worker state snapshot")
	jsonOutput := fs.Bool("json", false, "print snapshot as JSON; requires --snapshot")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("show requires <worker>")
	}
	st := store.NewJSONStore(*statePath)
	worker, err := st.GetWorker(rest[0])
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", rest[0])
		}
		return err
	}
	if *jsonOutput && !*printSnapshot {
		return errors.New("show --json requires --snapshot")
	}
	if *printSnapshot {
		snap, err := workerSnapshot(st, worker, c.now().UTC())
		if err != nil {
			return err
		}
		if *jsonOutput {
			data, err := json.MarshalIndent(snap, "", "  ")
			if err != nil {
				return fmt.Errorf("encode worker snapshot: %w", err)
			}
			fmt.Fprintln(c.out, string(data))
			return nil
		}
		fmt.Fprintln(c.out, snap.Text())
		return nil
	}
	fmt.Fprintf(c.out, "id=%s\nstatus=%s\nengine=%s\nthread=%s\n", worker.ID, displayWorkerStatus(worker), worker.Engine, worker.ThreadID)
	if worker.RuntimeOwner != "" {
		fmt.Fprintf(c.out, "runtime_owner=%s\n", worker.RuntimeOwner)
	}
	if worker.HostID != "" {
		fmt.Fprintf(c.out, "host=%s\n", worker.HostID)
	}
	if worker.Role != "" {
		fmt.Fprintf(c.out, "role=%s\n", worker.Role)
	}
	if worker.ParentID != "" {
		fmt.Fprintf(c.out, "parent=%s\n", worker.ParentID)
	}
	if worker.Issue != "" {
		fmt.Fprintf(c.out, "issue=%s\n", worker.Issue)
	}
	if worker.ValidationOf != "" {
		fmt.Fprintf(c.out, "validation_of=%s\n", worker.ValidationOf)
	}
	if worker.ValidationStatus != "" {
		fmt.Fprintf(c.out, "validation_status=%s\n", worker.ValidationStatus)
	}
	if worker.TurnID != "" {
		fmt.Fprintf(c.out, "turn=%s\n", worker.TurnID)
	}
	fmt.Fprintf(c.out, "repo=%s\nprompt=%s\n", worker.ProjectRoot, worker.Prompt)
	if worker.Report != "" {
		fmt.Fprintf(c.out, "report=%s\n", worker.Report)
	}
	for _, event := range worker.Events {
		fmt.Fprintf(c.out, "%s\t%s\t%s\n", event.At.Format(time.RFC3339), event.Type, event.Message)
	}
	return nil
}

func (c cli) transcript(args []string) error {
	fs := c.flagSet("transcript")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	jsonOutput := fs.Bool("json", false, "print transcript as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("transcript requires <worker>")
	}
	worker, err := store.NewJSONStore(*statePath).GetWorker(rest[0])
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", rest[0])
		}
		return err
	}
	doc := transcript.Build(worker, c.now().UTC())
	if *jsonOutput {
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fmt.Errorf("encode transcript: %w", err)
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprint(c.out, doc.Text())
	return nil
}

func (c cli) workpacket(args []string) error {
	fs := c.flagSet("workpacket")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	workerID := fs.String("worker", "", "worker id")
	jsonOutput := fs.Bool("json", false, "print work packet as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*workerID) == "" {
		rest := fs.Args()
		if len(rest) == 1 {
			*workerID = rest[0]
		}
	}
	if strings.TrimSpace(*workerID) == "" {
		return errors.New("workpacket requires --worker")
	}
	st := store.NewJSONStore(*statePath)
	worker, err := st.GetWorker(*workerID)
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", *workerID)
		}
		return err
	}
	claimList, err := st.ListClaims()
	if err != nil {
		return err
	}
	packet := workpacket.Build(workpacket.Input{
		Worker: worker,
		Claims: claimList,
		Now:    c.now().UTC(),
	})
	if *jsonOutput {
		data, err := json.MarshalIndent(packet, "", "  ")
		if err != nil {
			return fmt.Errorf("encode work packet: %w", err)
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprint(c.out, packet.Text())
	return nil
}

func (c cli) worker(args []string) error {
	if len(args) == 0 {
		return errors.New("worker requires <check>")
	}
	switch args[0] {
	case "check":
		return c.workerCheck(args[1:])
	default:
		return fmt.Errorf("unknown worker command %q", args[0])
	}
}

func (c cli) workerCheck(args []string) error {
	fs := c.flagSet("worker check")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	worktree := fs.String("worktree", "", "expected worktree path")
	jsonOutput := fs.Bool("json", false, "print check report as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("worker check requires <worker>")
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	issue := strings.TrimSpace(*issueValue)
	if issue != "" {
		normalized, err := normalizeRequiredIssue(issue)
		if err != nil {
			return err
		}
		issue = normalized
	}
	st := store.NewJSONStore(*statePath)
	worker, err := st.GetWorker(rest[0])
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", rest[0])
		}
		return err
	}
	claimList, err := st.ListClaims()
	if err != nil {
		return err
	}
	report := ownership.CheckWorker(ownership.Input{
		Worker:   worker,
		Claims:   claimList,
		Repo:     repoRoot,
		Issue:    issue,
		Worktree: *worktree,
		Now:      c.now().UTC(),
	})
	if *jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode ownership report: %w", err)
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintf(c.out, "worker=%s ok=%t repo=%s issue=%s worktree=%s\n", report.WorkerID, report.OK, report.Repo, emptyDash(report.Issue), emptyDash(report.Worktree))
	for _, check := range report.Checks {
		if check.ClaimID != "" {
			fmt.Fprintf(c.out, "%s\t%s\tclaim=%s\t%s\n", check.Severity, check.Code, check.ClaimID, check.Message)
			continue
		}
		fmt.Fprintf(c.out, "%s\t%s\t%s\n", check.Severity, check.Code, check.Message)
	}
	return nil
}

func workerSnapshot(st *store.JSONStore, worker store.Worker, generatedAt time.Time) (snapshot.Snapshot, error) {
	claims, err := st.ListClaims()
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	gates, err := st.ListGateEvidence()
	if err != nil {
		return snapshot.Snapshot{}, err
	}
	return snapshot.Build(snapshot.Input{
		Worker:       worker,
		Claims:       claims,
		GateEvidence: gates,
		GeneratedAt:  generatedAt,
	}), nil
}

func (c cli) updateWorker(statePath, id string, mutate func(*store.Worker, time.Time), print func(store.Worker)) error {
	now := c.now().UTC()
	worker, err := store.NewJSONStore(statePath).UpdateWorker(id, func(worker *store.Worker) error {
		mutate(worker, now)
		worker.UpdatedAt = now
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", id)
		}
		return err
	}
	print(worker)
	return nil
}

func (c cli) runner() appserverRunner {
	if c.appserverRunner != nil {
		return c.appserverRunner
	}
	return appserver.Runner{}
}

func (c cli) runnerFor(worker store.Worker) appserverRunner {
	if c.appserverRunner != nil {
		return c.appserverRunner
	}
	if worker.Remote == nil {
		return appserver.Runner{}
	}
	return appserver.Runner{Process: appserver.SSHProcess{
		Target:      worker.Remote.Host,
		Jump:        worker.Remote.JumpHost,
		CodexBinary: worker.Remote.CodexBinary,
	}, Sandbox: "danger-full-access"}
}

func gitConfigValue(ctx context.Context, repoRoot, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("git config --get %s in %s: %w", key, repoRoot, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c cli) runTurnObserved(runner appserverRunner, ctx context.Context, cwd, prompt string, observer appserver.TurnObserver, steering appserver.SteeringPolicy) (appserver.RunResult, error) {
	if coordinated, ok := runner.(appserverCoordinatedRunner); ok {
		return coordinated.RunTurnCoordinated(ctx, cwd, prompt, observer, steering)
	}
	if observed, ok := runner.(appserverObservedRunner); ok {
		return observed.RunTurnObserved(ctx, cwd, prompt, observer)
	}
	return runner.RunTurn(ctx, cwd, prompt)
}

func (c cli) sendTurnObserved(runner appserverRunner, ctx context.Context, cwd, threadID, prompt string, observer appserver.TurnObserver, steering appserver.SteeringPolicy) (appserver.RunResult, error) {
	if coordinated, ok := runner.(appserverCoordinatedRunner); ok {
		return coordinated.SendTurnCoordinated(ctx, cwd, threadID, prompt, observer, steering)
	}
	if observed, ok := runner.(appserverObservedRunner); ok {
		return observed.SendTurnObserved(ctx, cwd, threadID, prompt, observer)
	}
	return runner.SendTurn(ctx, cwd, threadID, prompt)
}

func (c cli) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.err)
	return fs
}

func (c cli) printUsage() {
	fmt.Fprintln(c.out, `cs - Codex swarm operator CLI

Usage:
  cs status [--since 24h] [--repo .] [--status working,idle] [--limit 50]
  cs status --all
  cs status --issues
  cs attention [--repo .] [--kind queued_message,blocked_claim] [--json]
  cs operation list [--key <operation-key> | --issue owner/repo#123 | --worker <worker>] [--json]
  cs operation show <operation-key> [--json]
  cs tasks list [--host local] [--status idle] [--limit 100] [--cursor <opaque>] [--json]
  cs tasks collect page --host local --observation <id> --page 1 --file page.json
  cs tasks collect finish --host local --observation <id> [--coverage window|complete]
  cs tasks collect status --host local --observation <id> [--json]
  cs tasks ingest --file snapshot.json [--request-id <id>] [--daemon http://127.0.0.1:8787]
  cs tasks status [--stale-for 24h] [--json]
  cs decision record --author <worker> --summary "decision" --rationale "why" [--operation <key>] [--repo .] [--issue owner/repo#123] [--evidence <ref>]
  cs decision list [--operation <key>] [--repo .] [--issue owner/repo#123] [--current] [--json]
  cs decision show [--json] <decision-id>
  cs decision supersede <decision-id> --request-id <id> --summary "replacement" --rationale "why"
  cs spawn --repo . --prompt "inspect this repo"
  cs spawn --engine appserver --repo . --prompt "summarize this repo in one sentence"
  cs spawn --engine appserver --repo . --worktree --remote-host user@host --prompt "work remotely"
  cs attach --repo . --thread <thread-id> --prompt "track this Codex task"
  cs attach --worker <worker-id> --engine appserver --thread <thread-id> --turn <turn-id>
  cs spawn --issue owner/repo#123 --repo . --prompt "work this issue"
  cs doctor --appserver
  cs send <worker> "continue with tests"
  cs message --request-id <id> <from-worker> <to-worker> "note"
  cs message --subtree <from-worker> <root-worker> "note"
  cs message confirm-steered --worker <worker> --thread <thread> --turn <turn> <delivery>
  cs message steering-failed --worker <worker> --thread <thread> --turn <turn> --error <error> <delivery>
  cs inbox --queued <worker>
  cs touch --worker <worker> --repo . --path internal/store/store.go --intent "edit store"
  cs handoff --request-id <id> <from-worker> <to-worker> "summary"
  cs workpacket --worker <worker>
  cs trace start "Fix deploy" --key fix-deploy
  cs trace into "Debug CI" --key debug-ci
  cs trace done "CI fixed"
  cs trace status
  cs janitor stale
  cs janitor release --apply
  cs version
  cs claim create --repo . --scope internal/store --worker <worker>
  cs worker check <worker> --repo .
  cs claim conflicts --repo . --scope internal/store
  cs claim push --issue owner/repo#123
  cs gate list --repo .
  cs gate record --repo . --worker <worker> --gate test --exit-code 0 --output "go test ./..."
  cs validate start --repo . --issue owner/repo#123 --prompt "implement this issue" --gate test
  cs issue export --issue owner/repo#123
  cs issue ready --issue owner/repo#123 --repo .
  cs issue dispatch --issue owner/repo#123 --repo . --prompt "implement this issue" --gate test
  cs issue pull --issue owner/repo#123
  cs issue sync --issue owner/repo#123
  cs issue report --issue owner/repo#123 --worker <worker> --gate test
  cs issue report --issue owner/repo#123 --worker <worker> --bypass-gates
  cs pr attach --worker <worker> --url https://github.com/owner/repo/pull/123
  cs pr status <worker>
  cs agent register --name "codex-thread" --role implementer
  cs agent current
  cs legacy import-coordinator
  cs resume <worker>
  cs inspect-thread <worker>
  cs transcript <worker>
  cs show <worker>
  cs show --snapshot <worker>
  cs show --snapshot --json <worker>
  cs schedule add --repo . --cron "0 8 * * 1" --prompt "weekly repo check"
  cs schedule list
  cs repo hints --repo .
  cs report --note "summary" <worker> done
  cs close --note "summary" <worker>`)
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func displayWorkerStatus(worker store.Worker) string {
	if worker.Lifecycle != nil {
		return string(worker.Lifecycle.DeriveStatus())
	}
	return string(worker.Status)
}

func defaultStatePath() string {
	if value := os.Getenv("CODEX_SWARM_STATE"); value != "" {
		return value
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "codex-swarm", "state.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".codex-swarm", "state.json")
	}
	return filepath.Join(".codex-swarm", "state.json")
}

func mockSummary(prompt string) string {
	return "mock worker accepted: " + short(prompt, 96)
}

func workerStatusFromTurn(status string) store.WorkerStatus {
	switch status {
	case "inProgress":
		return store.WorkerRunning
	case "failed":
		return store.WorkerFailed
	default:
		return store.WorkerIdle
	}
}

func workerExecutionRoot(worker store.Worker) string {
	if strings.TrimSpace(worker.Worktree) != "" {
		if worker.Remote != nil {
			return worker.Worktree
		}
		if info, err := os.Stat(worker.Worktree); err == nil && info.IsDir() {
			return worker.Worktree
		}
	}
	return worker.ProjectRoot
}

func appendAppserverWarnings(events []store.Event, at time.Time, warnings []string) []store.Event {
	for _, warning := range warnings {
		events = append(events, store.Event{At: at, Type: "appserver.warning", Message: warning})
	}
	return events
}

func appendAppserverFinalMessage(events []store.Event, at time.Time, message string) []store.Event {
	if message = strings.TrimSpace(message); message != "" {
		events = append(events, store.Event{At: at, Type: "appserver.agent.message", Message: message})
	}
	return events
}

func eventIssue(from, to *store.Worker) string {
	if from != nil && from.Issue != "" {
		return from.Issue
	}
	if to != nil {
		return to.Issue
	}
	return ""
}

func mutationFingerprint(command, from, to, payload string) string {
	sum := sha256.Sum256([]byte(command + "\x00" + from + "\x00" + to + "\x00" + payload))
	return hex.EncodeToString(sum[:])
}

func appserverWarnings(events []store.Event) []string {
	warnings := []string(nil)
	for _, event := range events {
		if event.Type == "appserver.warning" {
			warnings = append(warnings, event.Message)
		}
	}
	return warnings
}

func printWarnings(out io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
}

func short(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}
