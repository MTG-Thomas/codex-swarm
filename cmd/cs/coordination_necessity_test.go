package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCLIAttachCreatesTruthfulTrackerAndCanBindActiveAppserverTurn(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	repo := t.TempDir()
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"attach", "--state", state, "--repo", repo, "--thread", "thread-desktop", "--role", "implementer", "--prompt", "current task"}); err != nil {
		t.Fatalf("attach tracker error = %v", err)
	}
	worker := mustFindWorkerByPrompt(t, state, "current task")
	if worker.Engine != "tracker" || worker.ThreadID != "thread-desktop" || worker.Worktree != "" || worker.Branch != "" {
		t.Fatalf("attached tracker = %#v", worker)
	}
	if !strings.Contains(out.String(), "live_messages=queued") {
		t.Fatalf("attach output = %q", out.String())
	}
	st := store.NewJSONStore(state)
	if _, err := st.UpdateWorker(worker.ID, func(worker *store.Worker) error {
		worker.Worktree = filepath.Join(repo, "managed-worktree")
		worker.Branch = "codex/managed"
		worker.Events = append(worker.Events, store.Event{At: now, Type: "worktree.created", WorkerID: worker.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	out.Reset()
	if err := c.run([]string{"attach", "--state", state, "--repo", repo, "--worker", worker.ID, "--engine", "appserver", "--thread", "thread-app", "--turn", "turn-1"}); err != nil {
		t.Fatalf("attach appserver error = %v", err)
	}
	worker = mustGetWorker(t, state, worker.ID)
	if worker.Engine != "appserver" || worker.TurnID != "turn-1" || displayWorkerStatus(worker) != "working" || worker.Worktree == "" || worker.Branch != "codex/managed" {
		t.Fatalf("attached appserver worker = %#v", worker)
	}
	if !strings.Contains(out.String(), "live_messages=steerable") {
		t.Fatalf("appserver attach output = %q", out.String())
	}
}

func TestCLIStatusDefaultsToRecentAndWorkingWorkers(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	st := store.NewJSONStore(state)
	workers := []store.Worker{
		{ID: "w-old-idle", ProjectRoot: "/repo", Engine: "tracker", Status: store.WorkerIdle, Prompt: "old idle", CreatedAt: now.Add(-72 * time.Hour), UpdatedAt: now.Add(-72 * time.Hour)},
		{ID: "w-recent-done", ProjectRoot: "/repo", Engine: "tracker", Status: store.WorkerDone, Prompt: "recent done", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
		{ID: "w-old-working", ProjectRoot: "/repo", Engine: "appserver", Status: store.WorkerRunning, Prompt: "old working", CreatedAt: now.Add(-72 * time.Hour), UpdatedAt: now.Add(-72 * time.Hour)},
	}
	for i := range workers {
		workers[i].ApplyStatusAt(workers[i].Status, workers[i].UpdatedAt)
	}
	if err := st.SaveWorkers(workers...); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := c.run([]string{"status", "--state", state}); err != nil {
		t.Fatalf("status error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"backend=sqlite", "view shown=2 total=3", "w-recent-done", "w-old-working"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "w-old-idle") {
		t.Fatalf("status included old idle worker:\n%s", got)
	}

	out.Reset()
	if err := c.run([]string{"status", "--all", "--state", state}); err != nil {
		t.Fatalf("status --all error = %v", err)
	}
	if !strings.Contains(out.String(), "w-old-idle") {
		t.Fatalf("status --all omitted old worker:\n%s", out.String())
	}
}

func TestCLIStatusJSONReportsCapabilitiesWithoutEngineSpecificShape(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	st := store.NewJSONStore(state)
	workers := []store.Worker{
		{ID: "w-app", ProjectRoot: "/repo", Engine: "appserver", Status: store.WorkerRunning, ThreadID: "thread", TurnID: "turn", CreatedAt: now, UpdatedAt: now},
		{ID: "w-tracker", ProjectRoot: "/repo", Engine: "tracker", Status: store.WorkerIdle, CreatedAt: now, UpdatedAt: now},
		{ID: "w-future", ProjectRoot: "/repo", Engine: "future-engine", Status: store.WorkerIdle, CreatedAt: now, UpdatedAt: now},
	}
	for i := range workers {
		workers[i].ApplyStatusAt(workers[i].Status, workers[i].UpdatedAt)
	}
	if err := st.SaveWorkers(workers...); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := c.run([]string{"status", "--state", state, "--all", "--json"}); err != nil {
		t.Fatalf("status JSON error = %v", err)
	}
	var view workerStatusView
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Status.LiveMessageWorkers != 1 || view.Status.ResumeWorkers != 1 || view.Status.AutomaticCompletionWorkers != 1 || view.Status.ExternalTrackerWorkers != 1 {
		t.Fatalf("capability status = %#v", view.Status)
	}
	capabilities := map[string][]string{}
	for _, worker := range view.Workers {
		capabilities[worker.ID] = worker.Capabilities
	}
	if strings.Join(capabilities["w-app"], ",") != "live_message,resume,automatic_completion" || strings.Join(capabilities["w-tracker"], ",") != "external_tracker" || len(capabilities["w-future"]) != 0 {
		t.Fatalf("worker capabilities = %#v", capabilities)
	}
	var raw struct {
		Status map[string]json.RawMessage `json:"status"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"appserver_workers", "tracker_workers", "mock_workers"} {
		if _, found := raw.Status[forbidden]; found {
			t.Fatalf("status JSON contains engine-specific field %q: %s", forbidden, out.String())
		}
	}
}

func TestCLIClaimCreateAcceptsRepeatedTypedScopesAtomically(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	repo := t.TempDir()
	st := store.NewJSONStore(state)
	if err := st.SaveWorker(store.Worker{ID: "w-claim", ProjectRoot: repo, Engine: "tracker", Status: store.WorkerIdle, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := c.run([]string{"claim", "create", "--state", state, "--repo", repo, "--worker", "w-claim", "--scope", "path:cmd/cs", "--scope", "live:pve-t340/vm114"}); err != nil {
		t.Fatalf("claim create error = %v", err)
	}
	claimList, err := st.ListClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claimList) != 2 {
		t.Fatalf("claims = %#v", claimList)
	}
	if !strings.Contains(out.String(), "claims=2") || !strings.Contains(out.String(), "scope=path:cmd/cs") || !strings.Contains(out.String(), "scope=live:pve-t340/vm114") {
		t.Fatalf("claim output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "create", "--state", state, "--repo", repo, "--worker", "w-claim", "--scope", "cmd/cs,internal/store"}); err == nil {
		t.Fatal("comma-packed claim error = nil")
	}
	if got, err := st.ListClaims(); err != nil || len(got) != 2 {
		t.Fatalf("claims after rejected create = %#v err=%v", got, err)
	}
}

func TestCLICloseAtomicallyReleasesClaimsRefreshesPRAndNotifiesParent(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	repo := t.TempDir()
	st := store.NewJSONStore(state)
	parent := store.Worker{ID: "w-parent", ProjectRoot: repo, Engine: "tracker", Status: store.WorkerIdle, CreatedAt: now, UpdatedAt: now}
	child := store.Worker{
		ID: "w-child", ParentID: parent.ID, ProjectRoot: repo, Engine: "tracker", Status: store.WorkerIdle,
		PullRequests: []store.PullRequestState{{URL: "https://github.com/MTG-Thomas/codex-swarm/pull/66", UpdatedAt: now}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := st.SaveWorkers(parent, child); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []store.Claim{
		{ID: "c-active", WorkerID: child.ID, Repo: repo, ScopeKind: store.ClaimScopePath, Scope: "cmd/cs", Status: store.ClaimActive, CreatedAt: now, UpdatedAt: now},
		{ID: "c-blocked", WorkerID: child.ID, Repo: repo, ScopeKind: store.ClaimScopeTask, Scope: "release", Status: store.ClaimBlocked, Blocker: "waiting", CreatedAt: now, UpdatedAt: now},
	} {
		if err := st.SaveClaim(claim); err != nil {
			t.Fatal(err)
		}
	}
	installFakeGH(t, fakeGHState{PR: fakeGHPR{
		URL: "https://github.com/MTG-Thomas/codex-swarm/pull/66", State: "MERGED", BaseRefName: "main", HeadRefName: "codex/change",
		Checks: []fakeGHCheck{{Name: "go", Conclusion: "SUCCESS", Status: "COMPLETED"}, {Context: "CodeRabbit", State: "FAILURE"}},
	}})
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	args := []string{"close", "--state", state, "--request-id", "close-1", "--note", "merged and verified", child.ID}
	if err := c.run(args); err != nil {
		t.Fatalf("close error = %v", err)
	}
	if !strings.Contains(out.String(), "released_claims=2") || !strings.Contains(out.String(), "completion forwarded") {
		t.Fatalf("close output = %q", out.String())
	}
	closed := mustGetWorker(t, state, child.ID)
	if displayWorkerStatus(closed) != "done" || closed.Report != "done: merged and verified" || len(closed.PullRequests) != 1 || closed.PullRequests[0].NextAction != "complete" || closed.PullRequests[0].CodeRabbitStatus != "FAILURE" {
		t.Fatalf("closed worker = %#v", closed)
	}
	claimList, err := st.ListClaims()
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range claimList {
		if claim.Status != store.ClaimReleased || claim.Blocker != "" {
			t.Fatalf("claim after close = %#v", claim)
		}
	}
	messages, err := st.ListMessages(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Message.Kind != store.MessageCompletion {
		t.Fatalf("parent messages = %#v", messages)
	}

	out.Reset()
	if err := c.run(args); err != nil {
		t.Fatalf("close replay error = %v", err)
	}
	if !strings.Contains(out.String(), "replayed=true") {
		t.Fatalf("close replay output = %q", out.String())
	}
	messages, err = st.ListMessages(parent.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("parent messages after replay = %#v err=%v", messages, err)
	}
}
