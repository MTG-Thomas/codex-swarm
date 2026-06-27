package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestBuildDerivesCompactWorkerSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 27, 18, 0, 0, 0, time.UTC)
	worker := store.Worker{
		ID:          "w-1",
		Role:        "implementer",
		Issue:       "MTG-Thomas/codex-swarm#17",
		ProjectRoot: "/repo",
		Worktree:    "/repo/.codex-swarm/worktrees/w-1",
		Branch:      "cs/w-1",
		ThreadID:    "thread-1",
		Engine:      "appserver",
		Status:      store.WorkerIdle,
		Lifecycle:   lifecyclePtr(lifecycle.NewWorkerLifecycle()),
		Prompt:      "implement snapshots",
		LastMessage: "latest worker update",
		Report:      "ready for validation",
		UpdatedAt:   now,
		Events: []store.Event{
			{At: now.Add(-3 * time.Minute), Type: "spawned", Message: "worker created"},
			{At: now.Add(-2 * time.Minute), Type: "message.received", From: "w-review", Message: "from=w-review please check gates"},
			{At: now.Add(-time.Minute), Type: "quality.gate", Message: "gate=test exit=0 command=go test ./..."},
		},
	}

	got := Build(Input{
		Worker: worker,
		Claims: []store.Claim{{
			ID:       "c-1",
			WorkerID: "w-1",
			Issue:    "MTG-Thomas/codex-swarm#17",
			Scope:    "cmd/cs",
			Status:   store.ClaimActive,
		}},
		GateEvidence: []store.GateEvidence{{
			ID:        "g-1",
			GateID:    "test",
			WorkerID:  "w-1",
			Repo:      "/repo",
			Command:   "go test ./...",
			ExitCode:  0,
			Commit:    "abc123",
			CreatedAt: now.Add(-time.Minute),
		}},
	})

	if got.Worker.ID != "w-1" || got.Worker.Status != "working" || got.Worker.Issue != "MTG-Thomas/codex-swarm#17" {
		t.Fatalf("worker snapshot = %#v", got.Worker)
	}
	if len(got.Claims) != 1 || got.Claims[0].ID != "c-1" || got.Claims[0].Status != "active" {
		t.Fatalf("claims = %#v", got.Claims)
	}
	if len(got.Gates) != 1 || got.Gates[0].ID != "test" || got.Gates[0].ExitCode != 0 || got.Gates[0].Commit != "abc123" {
		t.Fatalf("gates = %#v", got.Gates)
	}
	if len(got.RecentEvents) != 3 || got.RecentEvents[2].Type != "quality.gate" {
		t.Fatalf("recent events = %#v", got.RecentEvents)
	}
	text := got.Text()
	for _, want := range []string{
		"STATE_SNAPSHOT worker=w-1 status=working role=implementer issue=MTG-Thomas/codex-swarm#17",
		"repo=/repo",
		"claim=c-1 status=active scope=cmd/cs",
		"gate=test exit=0 commit=abc123 command=go test ./...",
		"report=ready for validation",
		"last=latest worker update",
		"event=quality.gate gate=test exit=0 command=go test ./...",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot text missing %q:\n%s", want, text)
		}
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal snapshot: %v", err)
	}
	if !strings.Contains(string(data), `"schema":"codex-swarm.worker-snapshot.v1"`) {
		t.Fatalf("snapshot JSON missing schema: %s", data)
	}
}

func TestBuildFiltersUnrelatedClaimsAndGates(t *testing.T) {
	worker := store.Worker{
		ID:          "w-1",
		Issue:       "MTG-Thomas/codex-swarm#17",
		ProjectRoot: "/repo",
		Status:      store.WorkerDone,
	}
	got := Build(Input{
		Worker: worker,
		Claims: []store.Claim{
			{ID: "c-worker", WorkerID: "w-1", Issue: "MTG-Thomas/codex-swarm#99", Status: store.ClaimActive},
			{ID: "c-issue", WorkerID: "w-other", Issue: "MTG-Thomas/codex-swarm#17", Status: store.ClaimActive},
			{ID: "c-other", WorkerID: "w-other", Issue: "MTG-Thomas/codex-swarm#99", Status: store.ClaimActive},
		},
		GateEvidence: []store.GateEvidence{
			{ID: "g-worker", GateID: "test", WorkerID: "w-1", Repo: "/other"},
			{ID: "g-repo", GateID: "vet", WorkerID: "w-other", Repo: "/repo"},
			{ID: "g-other", GateID: "build", WorkerID: "w-other", Repo: "/other"},
		},
	})
	if len(got.Claims) != 2 || got.Claims[0].ID != "c-issue" || got.Claims[1].ID != "c-worker" {
		t.Fatalf("claims = %#v", got.Claims)
	}
	if len(got.Gates) != 2 || got.Gates[0].ID != "test" || got.Gates[1].ID != "vet" {
		t.Fatalf("gates = %#v", got.Gates)
	}
}

func lifecyclePtr(lc lifecycle.Lifecycle) *lifecycle.Lifecycle {
	return &lc
}
