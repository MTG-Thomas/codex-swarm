package attention

import (
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestDeriveProjectsActionableOpenLoops(t *testing.T) {
	now := time.Date(2026, 7, 22, 16, 0, 0, 0, time.UTC)
	workers := []store.Worker{
		{ID: "w-active", Issue: "owner/repo#68", ProjectRoot: "/repo", Status: store.WorkerIdle, UpdatedAt: now.Add(-time.Hour)},
		{ID: "w-stale", Issue: "owner/repo#70", ProjectRoot: "/repo", Status: store.WorkerRunning, UpdatedAt: now.Add(-25 * time.Hour)},
		{ID: "w-validator", ValidationOf: "w-active", ValidationStatus: "rejected", ProjectRoot: "/repo", Status: store.WorkerDone, UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "w-pr", Issue: "owner/repo#71", ProjectRoot: "/repo", Status: store.WorkerIdle, UpdatedAt: now.Add(-time.Hour), PullRequests: []store.PullRequestState{{
			URL: "https://github.com/owner/repo/pull/71", State: "OPEN", CheckSummary: "pass=2 fail=1 pending=0", NextAction: "fix-ci", UpdatedAt: now.Add(-30 * time.Minute),
		}}},
	}
	items := Derive(Input{
		Workers: workers,
		Claims:  []store.Claim{{ID: "c-1", WorkerID: "w-active", Issue: "owner/repo#68", Repo: "/repo", Scope: "cmd/cs", Status: store.ClaimBlocked, Blocker: "review", Next: "request review", UpdatedAt: now.Add(-3 * time.Hour)}},
		Messages: []store.DeliveredMessage{{
			Message:  store.Message{ID: "m-1", From: "w-sender", Kind: store.MessageDirect, CreatedAt: now.Add(-4 * time.Hour)},
			Delivery: store.Delivery{ID: "d-1", RecipientID: "w-active", State: store.DeliveryQueued, UpdatedAt: now.Add(-4 * time.Hour)},
		}},
		GateEvidence: []store.GateEvidence{{ID: "g-old", GateID: "test", WorkerID: "w-active", Repo: "/repo", ExitCode: 0, CreatedAt: now.Add(-6 * time.Hour)}, {ID: "g-new", GateID: "test", WorkerID: "w-active", Repo: "/repo", ExitCode: 1, CreatedAt: now.Add(-5 * time.Hour)}},
		Now:          now, StaleAfter: 24 * time.Hour,
	})

	wantKinds := map[string]bool{
		KindBlockedClaim: true, KindFailedGate: true, KindValidatorRejected: true,
		KindQueuedMessage: true, KindPullRequest: true, KindStaleWorker: true,
	}
	if len(items) != len(wantKinds) {
		t.Fatalf("Derive() items = %#v, want one of each actionable kind", items)
	}
	for _, item := range items {
		if !wantKinds[item.Kind] {
			t.Fatalf("unexpected attention item kind %q", item.Kind)
		}
		delete(wantKinds, item.Kind)
		if item.NextAction == "" || item.UpdatedAt.IsZero() {
			t.Fatalf("attention item lacks action/evidence: %#v", item)
		}
	}
}

func TestDeriveSuppressesResolvedHistory(t *testing.T) {
	now := time.Date(2026, 7, 22, 16, 0, 0, 0, time.UTC)
	items := Derive(Input{
		Workers: []store.Worker{
			{ID: "w-done", ProjectRoot: "/repo", Status: store.WorkerDone, UpdatedAt: now.Add(-48 * time.Hour), PullRequests: []store.PullRequestState{{URL: "pr-1", NextAction: "complete", UpdatedAt: now}}},
			{ID: "w-validator", ValidationOf: "w-done", ValidationStatus: "rejected", Status: store.WorkerDone, UpdatedAt: now.Add(-time.Hour)},
		},
		Messages: []store.DeliveredMessage{{Delivery: store.Delivery{ID: "d-delivered", RecipientID: "w-done", State: store.DeliveryDelivered, UpdatedAt: now}}},
		GateEvidence: []store.GateEvidence{
			{ID: "g-fail", GateID: "test", WorkerID: "w-done", Repo: "/repo", ExitCode: 1, CreatedAt: now.Add(-2 * time.Hour)},
			{ID: "g-pass", GateID: "test", WorkerID: "w-done", Repo: "/repo", ExitCode: 0, CreatedAt: now.Add(-time.Hour)},
		},
		Now: now, StaleAfter: 24 * time.Hour,
	})
	if len(items) != 0 {
		t.Fatalf("Derive() = %#v, want resolved history suppressed", items)
	}
}
