package main

import (
	"context"
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
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type cli struct {
	out io.Writer
	err io.Writer
	now func() time.Time
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	workers, err := store.NewJSONStore(*statePath).ListWorkers()
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "workers=%d state=%s\n", len(workers), *statePath)
	for _, worker := range workers {
		fmt.Fprintf(c.out, "%s\t%s\t%s\t%s\t%s\n", worker.ID, worker.Status, worker.Engine, worker.ThreadID, short(worker.Prompt, 60))
	}
	return nil
}

func (c cli) spawn(args []string) error {
	fs := c.flagSet("spawn")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	prompt := fs.String("prompt", "", "worker prompt")
	engine := fs.String("engine", "mock", "worker engine: mock or appserver")
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
	id := fmt.Sprintf("w-%s", now.Format("20060102-150405"))
	if *engine != "mock" && *engine != "appserver" {
		return fmt.Errorf("unknown engine %q", *engine)
	}

	threadID := fmt.Sprintf("mock-thread-%s", id)
	turnID := ""
	lastMessage := mockSummary(*prompt)
	status := store.WorkerIdle
	events := []store.Event{{At: now, Type: "spawned", Message: "worker created"}}
	if *engine == "appserver" {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		result, err := (appserver.Runner{}).RunTurn(ctx, repoRoot, *prompt)
		if err != nil {
			return fmt.Errorf("run app-server worker: %w", err)
		}
		threadID = result.ThreadID
		turnID = result.TurnID
		status = workerStatusFromTurn(result.Status)
		lastMessage = fmt.Sprintf("app-server turn submitted: thread=%s turn=%s status=%s", result.ThreadID, result.TurnID, result.Status)
		events = append(events, store.Event{At: now, Type: "appserver.turn.started", Message: lastMessage})
	} else {
		events = append(events, store.Event{At: now, Type: "mock.turn.completed", Message: lastMessage})
	}

	worker := store.Worker{
		ID:          id,
		ProjectRoot: repoRoot,
		Worktree:    filepath.Join(repoRoot, ".codex-swarm", "worktrees", id),
		Branch:      "cs/" + id,
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

	if err := store.NewJSONStore(*statePath).SaveWorker(worker); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "spawned %s engine=%s thread=%s status=%s\n", worker.ID, worker.Engine, worker.ThreadID, worker.Status)
	fmt.Fprintf(c.out, "%s\n", worker.LastMessage)
	if worker.Engine == "appserver" {
		fmt.Fprintf(c.out, "codex thread: %s\n", worker.ThreadID)
		fmt.Fprintf(c.out, "inspect: cs inspect-thread --state %s %s\n", *statePath, worker.ID)
		fmt.Fprintln(c.out, "note: Codex app visibility can lag briefly, especially on mobile.")
	}
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
	return c.updateWorker(*statePath, id, func(worker *store.Worker, now time.Time) {
		worker.Events = append(worker.Events, store.Event{At: now, Type: "message.sent", Message: message})
		if worker.Engine == "appserver" {
			ctx, cancel := context.WithTimeout(context.Background(), *timeout)
			defer cancel()
			result, err := (appserver.Runner{}).SendTurn(ctx, worker.ProjectRoot, worker.ThreadID, message)
			if err != nil {
				worker.Status = store.WorkerFailed
				worker.LastMessage = "app-server send failed: " + err.Error()
				worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.turn.failed", Message: worker.LastMessage})
				return
			}
			worker.TurnID = result.TurnID
			worker.Status = workerStatusFromTurn(result.Status)
			worker.LastMessage = fmt.Sprintf("app-server turn submitted: thread=%s turn=%s status=%s", result.ThreadID, result.TurnID, result.Status)
			worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.turn.started", Message: worker.LastMessage})
			return
		}
		worker.Status = store.WorkerIdle
		worker.LastMessage = mockSummary(message)
		worker.Events = append(worker.Events, store.Event{At: now, Type: "mock.turn.completed", Message: worker.LastMessage})
	}, func(worker store.Worker) {
		fmt.Fprintf(c.out, "sent %s status=%s\n%s\n", worker.ID, worker.Status, worker.LastMessage)
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
			worker.Status = store.WorkerDone
		case "failed":
			worker.Status = store.WorkerFailed
		default:
			worker.Status = store.WorkerIdle
		}
		worker.Report = report
		worker.Events = append(worker.Events, store.Event{At: now, Type: "reported", Message: report})
	}, func(worker store.Worker) {
		fmt.Fprintf(c.out, "reported %s status=%s report=%q\n", worker.ID, worker.Status, worker.Report)
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
	return c.updateWorker(*statePath, rest[0], func(worker *store.Worker, now time.Time) {
		if worker.Engine == "appserver" {
			ctx, cancel := context.WithTimeout(context.Background(), *timeout)
			defer cancel()
			result, err := (appserver.Runner{}).Resume(ctx, worker.ProjectRoot, worker.ThreadID)
			if err != nil {
				worker.Status = store.WorkerFailed
				worker.LastMessage = "app-server resume failed: " + err.Error()
				worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.resume.failed", Message: worker.LastMessage})
				return
			}
			worker.ThreadID = result.ThreadID
			worker.Status = store.WorkerIdle
			worker.LastMessage = "app-server thread resumed: " + result.ThreadID
			worker.Events = append(worker.Events, store.Event{At: now, Type: "appserver.thread.resumed", Message: worker.LastMessage})
			return
		}
		worker.Status = store.WorkerIdle
		worker.Events = append(worker.Events, store.Event{At: now, Type: "resume.requested", Message: "resume requested"})
	}, func(worker store.Worker) {
		fmt.Fprintf(c.out, "resume %s thread=%s status=%s\n", worker.ID, worker.ThreadID, worker.Status)
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
	result, err := (appserver.Runner{}).Resume(ctx, worker.ProjectRoot, worker.ThreadID)
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
	fmt.Fprintf(c.out, "id=%s\nstatus=%s\nengine=%s\nthread=%s\n", worker.ID, worker.Status, worker.Engine, worker.ThreadID)
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
	s := store.NewJSONStore(statePath)
	worker, err := s.GetWorker(id)
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", id)
		}
		return err
	}
	now := c.now().UTC()
	mutate(&worker, now)
	worker.UpdatedAt = now
	if err := s.SaveWorker(worker); err != nil {
		return err
	}
	print(worker)
	return nil
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
  cs doctor --appserver
  cs send <worker> "continue with tests"
  cs resume <worker>
  cs inspect-thread <worker>
  cs show <worker>
  cs report --note "summary" <worker> done`)
}

func defaultStatePath() string {
	if value := os.Getenv("CODEX_SWARM_STATE"); value != "" {
		return value
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
