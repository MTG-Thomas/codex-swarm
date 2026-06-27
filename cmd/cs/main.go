package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/repohints"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
	"github.com/MTG-Thomas/codex-swarm/internal/worktree"
)

type cli struct {
	out             io.Writer
	err             io.Writer
	now             func() time.Time
	appserverRunner appserverRunner
}

type appserverRunner interface {
	RunTurn(ctx context.Context, cwd, prompt string) (appserver.RunResult, error)
	SendTurn(ctx context.Context, cwd, threadID, prompt string) (appserver.RunResult, error)
	Resume(ctx context.Context, cwd, threadID string) (appserver.RunResult, error)
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
	case "spawn":
		return c.spawn(args[1:])
	case "send":
		return c.send(args[1:])
	case "message":
		return c.message(args[1:])
	case "handoff":
		return c.handoff(args[1:])
	case "claim":
		return c.claim(args[1:])
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
	case "report":
		return c.report(args[1:])
	case "resume":
		return c.resume(args[1:])
	case "inspect-thread":
		return c.inspectThread(args[1:])
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
	fs := c.flagSet("status")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL, for example http://127.0.0.1:8787")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *daemonURL == "" {
		*daemonURL = os.Getenv("CODEX_SWARM_DAEMON_URL")
	}
	if *daemonURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := daemon.Client{BaseURL: *daemonURL}
		status, err := client.Status(ctx)
		if err != nil {
			return fmt.Errorf("daemon status: %w", err)
		}
		fmt.Fprintln(c.out, status.String())
		workers, err := client.Workers(ctx)
		if err != nil {
			return fmt.Errorf("daemon workers: %w", err)
		}
		for _, worker := range workers.Workers {
			fmt.Fprintf(c.out, "%s\t%s\t%s\t%s\t%s\n", worker.ID, worker.Status, emptyDash(worker.Issue), emptyDash(worker.Worktree), emptyDash(worker.ThreadID))
		}
		return nil
	}

	workers, err := store.NewJSONStore(*statePath).ListWorkers()
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "workers=%d state=%s\n", len(workers), *statePath)
	for _, worker := range workers {
		fmt.Fprintf(c.out, "%s\t%s\t%s\t%s\t%s\n", worker.ID, displayWorkerStatus(worker), worker.Engine, worker.ThreadID, short(worker.Prompt, 60))
	}
	return nil
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
	if *engine != "mock" && *engine != "appserver" {
		return fmt.Errorf("unknown engine %q", *engine)
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
	events := []store.Event{{At: now, Type: "spawned", Message: "worker created"}}
	worker := store.Worker{
		ID:          id,
		ParentID:    *parentID,
		Role:        *role,
		Issue:       issue,
		ProjectRoot: repoRoot,
		Worktree:    filepath.Join(repoRoot, ".codex-swarm", "worktrees", managedName),
		Branch:      "cs/" + managedName,
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

	if *createWorktree {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
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

	if *engine == "appserver" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		result, err := c.runner().RunTurn(ctx, workerExecutionRoot(worker), *prompt)
		if err != nil {
			worker.ApplyStatusAt(store.WorkerFailed, now)
			worker.LastMessage = "app-server spawn failed: " + err.Error()
			worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.spawn.failed", Message: worker.LastMessage})
			if saveErr := store.NewJSONStore(*statePath).SaveWorker(worker); saveErr != nil {
				return errors.Join(fmt.Errorf("run app-server worker: %w", err), fmt.Errorf("save failed app-server worker: %w", saveErr))
			}
			return fmt.Errorf("run app-server worker: %w", err)
		}
		threadID = result.ThreadID
		turnID = result.TurnID
		status = workerStatusFromTurn(result.Status)
		lastMessage = fmt.Sprintf("app-server turn submitted: thread=%s turn=%s status=%s", result.ThreadID, result.TurnID, result.Status)
		worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.turn.started", Message: lastMessage})
		worker.Events = appendAppserverWarnings(worker.Events, now, result.Warnings)
	} else {
		worker.Events = append(worker.Events, store.Event{At: now, Type: "mock.turn.completed", Message: lastMessage})
	}
	worker.ThreadID = threadID
	worker.TurnID = turnID
	worker.Status = status
	worker.LastMessage = lastMessage
	worker.ApplyStatusAt(status, now)

	if err := store.NewJSONStore(*statePath).SaveWorker(worker); err != nil {
		return err
	}
	saved, err := store.NewJSONStore(*statePath).GetWorker(worker.ID)
	if err != nil {
		return err
	}
	worker = saved
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
	fs := c.flagSet("message")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	requestIDFlag := fs.String("request-id", "", "idempotency key for this mutation")
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
	fingerprint := mutationFingerprint("message", fromID, toID, text)
	s := store.NewJSONStore(*statePath)
	result, _, err := s.UpdateWorkersWithRequest(requestID, "message", fingerprint, []string{fromID, toID}, func(workers map[string]*store.Worker) (store.WorkerMutationResult, error) {
		from := workers[fromID]
		to := workers[toID]
		event := store.Event{At: now, Type: "message", Message: text, From: from.ID, To: to.ID, Issue: eventIssue(from, to), WorkerID: from.ID, RequestID: requestID}
		from.Events = append(from.Events, store.Event{At: now, Type: "message.sent", Message: fmt.Sprintf("to=%s %s", to.ID, text), From: from.ID, To: to.ID, Issue: event.Issue, WorkerID: from.ID, RequestID: requestID})
		from.UpdatedAt = now
		to.Events = append(to.Events, store.Event{At: now, Type: "message.received", Message: fmt.Sprintf("from=%s %s", from.ID, text), From: from.ID, To: to.ID, Issue: event.Issue, WorkerID: to.ID, RequestID: requestID})
		to.UpdatedAt = now
		return store.WorkerMutationResult{
			Fingerprint: fingerprint,
			Output:      fmt.Sprintf("message %s -> %s request=%s\n", from.ID, to.ID, requestID),
			Events:      []store.Event{event},
		}, nil
	})
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("message worker not found: %w", err)
		}
		return err
	}
	fmt.Fprint(c.out, result.Output)
	return nil
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
	initial, err := store.NewJSONStore(*statePath).GetWorker(id)
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", id)
		}
		return err
	}
	if initial.Engine == "appserver" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		appserverResult, appserverErr = c.runner().SendTurn(ctx, workerExecutionRoot(initial), initial.ThreadID, message)
	}
	return c.updateWorker(*statePath, id, func(worker *store.Worker, now time.Time) {
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
			worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.turn.started", Message: worker.LastMessage})
			worker.Events = appendAppserverWarnings(worker.Events, now, appserverResult.Warnings)
			return
		}
		worker.ApplyStatus(store.WorkerIdle)
		worker.LastMessage = mockSummary(message)
		worker.Events = append(worker.Events, store.Event{At: now, Type: "mock.turn.completed", Message: worker.LastMessage})
	}, func(worker store.Worker) {
		fmt.Fprintf(c.out, "sent %s status=%s\n%s\n", worker.ID, displayWorkerStatus(worker), worker.LastMessage)
		printWarnings(c.out, appserverResult.Warnings)
	})
}

func (c cli) report(args []string) error {
	fs := c.flagSet("report")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	note := fs.String("note", "", "report note")
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
	return c.updateWorker(*statePath, id, func(worker *store.Worker, now time.Time) {
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
		appserverResult, appserverErr = c.runner().Resume(ctx, workerExecutionRoot(initial), initial.ThreadID)
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
	result, err := c.runner().Resume(ctx, workerExecutionRoot(worker), worker.ThreadID)
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("show requires <worker>")
	}
	worker, err := store.NewJSONStore(*statePath).GetWorker(rest[0])
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", rest[0])
		}
		return err
	}
	fmt.Fprintf(c.out, "id=%s\nstatus=%s\nengine=%s\nthread=%s\n", worker.ID, displayWorkerStatus(worker), worker.Engine, worker.ThreadID)
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

func (c cli) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.err)
	return fs
}

func (c cli) printUsage() {
	fmt.Fprintln(c.out, `cs - Codex swarm operator CLI

Usage:
  cs status
  cs spawn --repo . --prompt "inspect this repo"
  cs spawn --engine appserver --repo . --prompt "summarize this repo in one sentence"
  cs spawn --issue owner/repo#123 --repo . --prompt "work this issue"
  cs doctor --appserver
  cs send <worker> "continue with tests"
  cs message --request-id <id> <from-worker> <to-worker> "note"
  cs handoff --request-id <id> <from-worker> <to-worker> "summary"
  cs claim create --repo . --scope internal/store --worker <worker>
  cs claim conflicts --repo . --scope internal/store
  cs claim push --issue owner/repo#123
  cs gate list --repo .
  cs gate record --repo . --worker <worker> --gate test --exit-code 0 --output "go test ./..."
  cs validate start --repo . --issue owner/repo#123 --prompt "implement this issue" --gate test
  cs issue export --issue owner/repo#123
  cs issue pull --issue owner/repo#123
  cs issue sync --issue owner/repo#123
  cs issue report --issue owner/repo#123 --worker <worker>
  cs agent register --name "codex-thread" --role implementer
  cs agent current
  cs legacy import-coordinator
  cs resume <worker>
  cs inspect-thread <worker>
  cs show <worker>
  cs schedule add --repo . --cron "0 8 * * 1" --prompt "weekly repo check"
  cs schedule list
  cs repo hints --repo .
  cs report --note "summary" <worker> done`)
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
