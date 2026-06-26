package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/appserver"
	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCLIWorkflow(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &errOut,
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if got := defaultStatePath(); strings.Contains(got, ".codex-swarm"+string(filepath.Separator)+"state.json") {
		t.Fatalf("defaultStatePath() = %q, want machine-level config path", got)
	}

	if err := c.run([]string{"agent", "register", "--state", state, "--name", "test-thread", "--role", "implementer"}); err != nil {
		t.Fatalf("agent register error = %v", err)
	}
	if !strings.Contains(out.String(), "agent a-20260624-120000") {
		t.Fatalf("agent register output = %q", out.String())
	}
	out.Reset()
	if err := c.run([]string{"agent", "current", "--state", state}); err != nil {
		t.Fatalf("agent current error = %v", err)
	}
	if !strings.Contains(out.String(), "test-thread") || !strings.Contains(out.String(), "current=true") {
		t.Fatalf("agent current output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--role", "implementer", "--issue", "MTG-Thomas/codex-swarm#42", "--prompt", "inspect repo and report"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	if !strings.Contains(out.String(), "spawned w-20260624-120000-") {
		t.Fatalf("spawn output = %q", out.String())
	}
	if !strings.Contains(out.String(), "issue: MTG-Thomas/codex-swarm#42") {
		t.Fatalf("spawn issue output = %q", out.String())
	}
	implementerID := mustFindWorkerByPrompt(t, state, "inspect repo and report").ID

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--role", "reviewer", "--parent", implementerID, "--prompt", "review the implementation"}); err != nil {
		t.Fatalf("spawn reviewer error = %v", err)
	}
	if !strings.Contains(out.String(), "swarm: role=reviewer parent="+implementerID) {
		t.Fatalf("reviewer spawn output = %q", out.String())
	}
	reviewerID := mustFindWorkerByPrompt(t, state, "review the implementation").ID

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"claim", "create", "--state", state, "--repo", ".", "--scope", "internal/store", "--worker", implementerID, "--issue", "MTG-Thomas/codex-swarm#42", "--note", "editing store claims"}); err != nil {
		t.Fatalf("claim create error = %v", err)
	}
	if !strings.Contains(out.String(), "claim c-20260624-120002") || !strings.Contains(out.String(), "conflicts=0") {
		t.Fatalf("claim create output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"claim", "conflicts", "--state", state, "--repo", ".", "--scope", "internal/store/json.go"}); err != nil {
		t.Fatalf("claim conflicts error = %v", err)
	}
	if !strings.Contains(out.String(), "conflicts=1") || !strings.Contains(out.String(), "c-20260624-120002") {
		t.Fatalf("claim conflicts output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "export", "--state", state, "--issue", "MTG-Thomas/codex-swarm#42"}); err != nil {
		t.Fatalf("claim export error = %v", err)
	}
	if !strings.Contains(out.String(), "codex-swarm claims for `MTG-Thomas/codex-swarm#42`") || !strings.Contains(out.String(), "c-20260624-120002") {
		t.Fatalf("claim export output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "block", "--state", state, "--reason", "waiting on review", "--next", "reviewer checks json store", "c-20260624-120002"}); err != nil {
		t.Fatalf("claim block error = %v", err)
	}
	if !strings.Contains(out.String(), "blocked c-20260624-120002") {
		t.Fatalf("claim block output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "release", "--state", state, "--note", "done", "c-20260624-120002"}); err != nil {
		t.Fatalf("claim release error = %v", err)
	}
	if !strings.Contains(out.String(), "released c-20260624-120002") {
		t.Fatalf("claim release output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"message", "--state", state, implementerID, reviewerID, "please review the store changes"}); err != nil {
		t.Fatalf("message error = %v", err)
	}
	if !strings.Contains(out.String(), "message "+implementerID+" -> "+reviewerID) {
		t.Fatalf("message output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"handoff", "--state", state, implementerID, reviewerID, "implementation ready for review"}); err != nil {
		t.Fatalf("handoff error = %v", err)
	}
	if !strings.Contains(out.String(), "handoff "+implementerID+" -> "+reviewerID) {
		t.Fatalf("handoff output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"schedule", "add", "--state", state, "--repo", ".", "--cron", "0 8 * * 1", "--prompt", "weekly repo check"}); err != nil {
		t.Fatalf("schedule add error = %v", err)
	}
	if !strings.Contains(out.String(), "schedule s-20260624-120006") {
		t.Fatalf("schedule add output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"schedule", "list", "--state", state}); err != nil {
		t.Fatalf("schedule list error = %v", err)
	}
	if !strings.Contains(out.String(), "schedules=1") || !strings.Contains(out.String(), "weekly repo check") {
		t.Fatalf("schedule list output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"send", "--state", state, implementerID, "continue with tests"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	if !strings.Contains(out.String(), "sent "+implementerID) {
		t.Fatalf("send output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"report", "--state", state, "--note", "demo complete", implementerID, "done"}); err != nil {
		t.Fatalf("report error = %v", err)
	}
	if !strings.Contains(out.String(), "status=done") {
		t.Fatalf("report output = %q", out.String())
	}
	reported, err := store.NewJSONStore(state).GetWorker(implementerID)
	if err != nil {
		t.Fatalf("GetWorker(reported) error = %v", err)
	}
	if reported.Status != store.WorkerDone {
		t.Fatalf("reported Status = %q, want %q", reported.Status, store.WorkerDone)
	}
	if reported.Lifecycle == nil {
		t.Fatal("reported Lifecycle = nil, want done lifecycle")
	}
	if got := reported.Lifecycle.DeriveStatus(); got != lifecycle.DisplayDone {
		t.Fatalf("reported Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayDone)
	}
	if reported.Lifecycle.Session.CompletedAt == nil || !reported.Lifecycle.Session.CompletedAt.Equal(now) {
		t.Fatalf("reported CompletedAt = %v, want %v", reported.Lifecycle.Session.CompletedAt, now)
	}
	if reported.Lifecycle.Session.TerminatedAt != nil {
		t.Fatalf("reported TerminatedAt = %v, want nil", reported.Lifecycle.Session.TerminatedAt)
	}

	out.Reset()
	if err := c.run([]string{"status", "--state", state}); err != nil {
		t.Fatalf("status error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "workers=2") || !strings.Contains(got, implementerID) || !strings.Contains(got, reviewerID) {
		t.Fatalf("status output = %q", got)
	}
}

func TestCLIReportFailedSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	now := time.Date(2026, 6, 26, 14, 30, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &errOut,
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-failed",
		ProjectRoot: "/repo",
		ThreadID:    "thread-failed",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "worker that fails",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	if err := c.run([]string{"report", "--state", state, "--note", "failed test", "w-failed", "failed"}); err != nil {
		t.Fatalf("report failed error = %v", err)
	}

	worker, err := store.NewJSONStore(state).GetWorker("w-failed")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if worker.Lifecycle == nil {
		t.Fatal("Lifecycle = nil, want failed lifecycle")
	}
	if worker.Lifecycle.Session.TerminatedAt == nil || !worker.Lifecycle.Session.TerminatedAt.Equal(now) {
		t.Fatalf("TerminatedAt = %v, want %v", worker.Lifecycle.Session.TerminatedAt, now)
	}
	if worker.Lifecycle.Session.CompletedAt != nil {
		t.Fatalf("CompletedAt = %v, want nil", worker.Lifecycle.Session.CompletedAt)
	}
}

func TestCLISpawnSameSecondCreatesDistinctWorkers(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 18, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--prompt", "first"}); err != nil {
		t.Fatalf("first spawn error = %v", err)
	}
	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--prompt", "second"}); err != nil {
		t.Fatalf("second spawn error = %v", err)
	}

	workers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	if len(workers) != 2 {
		t.Fatalf("workers = %d, want 2: %#v", len(workers), workers)
	}
	if workers[0].ID == workers[1].ID {
		t.Fatalf("worker IDs both = %q, want distinct IDs", workers[0].ID)
	}
}

func TestCLIDisplaysLifecycleStatusForStaleWorkers(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	now := time.Date(2026, 6, 26, 15, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &errOut,
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")
	staleLifecycle := lifecycle.NewWorkerLifecycle()
	staleLifecycle.Runtime.State = lifecycle.RuntimeDead
	staleLifecycle.Runtime.Reason = lifecycle.ReasonRuntimeLost
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-stale",
		ProjectRoot: "/repo",
		ThreadID:    "thread-stale",
		Engine:      "mock",
		Status:      store.WorkerRunning,
		Lifecycle:   &staleLifecycle,
		Prompt:      "stale worker",
		Report:      "stale report",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	if err := c.run([]string{"status", "--state", state}); err != nil {
		t.Fatalf("status error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "w-stale\tstale\t") {
		t.Fatalf("status output = %q, want stale display status", got)
	}

	out.Reset()
	if err := c.run([]string{"show", "--state", state, "w-stale"}); err != nil {
		t.Fatalf("show error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "status=stale\n") {
		t.Fatalf("show output = %q, want stale display status", got)
	}

	body := workerIssueReportMarkdown("MTG-Thomas/codex-swarm#42", mustGetWorker(t, state, "w-stale"), "", now)
	if !strings.Contains(body, "- Status: `stale`") {
		t.Fatalf("worker report markdown = %q, want stale display status", body)
	}
}

func TestCLISpawnAppserverPrintsLifecycleDisplayStatus(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{ThreadID: "thread-1", TurnID: "turn-1", Status: "inProgress"}, nil
			},
		},
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", ".", "--prompt", "continue"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "status=working") {
		t.Fatalf("spawn output = %q, want lifecycle display status", got)
	}
}

func TestCLISpawnAppserverFailedResultSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 15, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{ThreadID: "thread-1", TurnID: "turn-1", Status: "failed"}, nil
			},
		},
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", ".", "--prompt", "continue"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, mustFindWorkerByPrompt(t, state, "continue").ID, now)
	if got := out.String(); !strings.Contains(got, "status=failed") {
		t.Fatalf("spawn output = %q, want failed display status", got)
	}
}

func TestCLISendAppserverFailureSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	saveAppserverWorker(t, state, now)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			sendTurn: func(context.Context, string, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{}, errors.New("transport failed")
			},
		},
	}

	if err := c.run([]string{"send", "--state", state, "w-app", "continue"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, "w-app", now)
}

func TestCLISendAppserverFailedResultSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 17, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	saveAppserverWorker(t, state, now)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			sendTurn: func(context.Context, string, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{ThreadID: "thread-app", TurnID: "turn-failed", Status: "failed"}, nil
			},
		},
	}

	if err := c.run([]string{"send", "--state", state, "w-app", "continue"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, "w-app", now)
}

func TestCLIResumeAppserverFailureSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 17, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	saveAppserverWorker(t, state, now)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			resume: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{}, errors.New("resume failed")
			},
		},
	}

	if err := c.run([]string{"resume", "--state", state, "w-app"}); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, "w-app", now)
}

func mustGetWorker(t *testing.T, state, id string) store.Worker {
	t.Helper()
	worker, err := store.NewJSONStore(state).GetWorker(id)
	if err != nil {
		t.Fatalf("GetWorker(%q) error = %v", id, err)
	}
	return worker
}

func mustFindWorkerByPrompt(t *testing.T, state, prompt string) store.Worker {
	t.Helper()
	workers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	for _, worker := range workers {
		if worker.Prompt == prompt {
			return worker
		}
	}
	t.Fatalf("worker with prompt %q not found in %#v", prompt, workers)
	return store.Worker{}
}

func saveAppserverWorker(t *testing.T, state string, now time.Time) {
	t.Helper()
	worker := store.Worker{
		ID:          "w-app",
		ProjectRoot: "/repo",
		ThreadID:    "thread-app",
		Engine:      "appserver",
		Status:      store.WorkerIdle,
		Prompt:      "app worker",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}
	if err := store.NewJSONStore(state).SaveWorker(worker); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
}

func assertWorkerTerminatedAt(t *testing.T, state, id string, want time.Time) {
	t.Helper()
	worker := mustGetWorker(t, state, id)
	if worker.Lifecycle == nil {
		t.Fatal("Lifecycle = nil, want failed lifecycle")
	}
	if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayFailed {
		t.Fatalf("DeriveStatus() = %q, want %q", got, lifecycle.DisplayFailed)
	}
	if worker.Lifecycle.Session.TerminatedAt == nil || !worker.Lifecycle.Session.TerminatedAt.Equal(want) {
		t.Fatalf("TerminatedAt = %v, want %v", worker.Lifecycle.Session.TerminatedAt, want)
	}
	if worker.Lifecycle.Session.CompletedAt != nil {
		t.Fatalf("CompletedAt = %v, want nil", worker.Lifecycle.Session.CompletedAt)
	}
}

type fakeAppserverRunner struct {
	runTurn  func(context.Context, string, string) (appserver.RunResult, error)
	sendTurn func(context.Context, string, string, string) (appserver.RunResult, error)
	resume   func(context.Context, string, string) (appserver.RunResult, error)
}

func (f fakeAppserverRunner) RunTurn(ctx context.Context, cwd, prompt string) (appserver.RunResult, error) {
	if f.runTurn == nil {
		return appserver.RunResult{}, errors.New("unexpected RunTurn")
	}
	return f.runTurn(ctx, cwd, prompt)
}

func (f fakeAppserverRunner) SendTurn(ctx context.Context, cwd, threadID, prompt string) (appserver.RunResult, error) {
	if f.sendTurn == nil {
		return appserver.RunResult{}, errors.New("unexpected SendTurn")
	}
	return f.sendTurn(ctx, cwd, threadID, prompt)
}

func (f fakeAppserverRunner) Resume(ctx context.Context, cwd, threadID string) (appserver.RunResult, error) {
	if f.resume == nil {
		return appserver.RunResult{}, errors.New("unexpected Resume")
	}
	return f.resume(ctx, cwd, threadID)
}
