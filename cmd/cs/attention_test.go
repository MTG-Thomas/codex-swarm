package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/attention"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestAttentionJSONProjectsAndFiltersOpenLoops(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 7, 22, 16, 0, 0, 0, time.UTC)
	c := cli{out: &out, err: &out, now: func() time.Time { return now }}
	state := filepath.Join(t.TempDir(), "state.db")
	repo := t.TempDir()
	st := store.NewJSONStore(state)
	for _, worker := range []store.Worker{
		{ID: "w-active", Issue: "owner/repo#68", ProjectRoot: repo, Status: store.WorkerIdle, UpdatedAt: now.Add(-time.Hour), PullRequests: []store.PullRequestState{{URL: "https://example.test/pull/1", State: "OPEN", NextAction: "merge-ready", UpdatedAt: now.Add(-time.Hour)}}},
		{ID: "w-stale", Issue: "owner/repo#70", ProjectRoot: repo, Status: store.WorkerRunning, UpdatedAt: now.Add(-25 * time.Hour)},
		{ID: "w-validator", ValidationOf: "w-active", ValidationStatus: "rejected", ProjectRoot: repo, Status: store.WorkerDone, UpdatedAt: now.Add(-2 * time.Hour)},
	} {
		if err := st.SaveWorker(worker); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveClaim(store.Claim{ID: "c-1", WorkerID: "w-active", Issue: "owner/repo#68", Repo: repo, Scope: "cmd/cs", Status: store.ClaimBlocked, Blocker: "review", UpdatedAt: now.Add(-3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.CreateMessage(store.Message{ID: "m-1", RequestID: "request-1", Kind: store.MessageDirect, From: "w-sender", Body: "continue", CreatedAt: now.Add(-4 * time.Hour)}, []string{"w-active"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveGateEvidence(store.GateEvidence{ID: "g-1", GateID: "test", WorkerID: "w-active", Repo: repo, ExitCode: 1, CreatedAt: now.Add(-5 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if err := c.run([]string{"attention", "--state", state, "--repo", repo, "--json"}); err != nil {
		t.Fatal(err)
	}
	var view attentionView
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	if view.Total != 6 || view.Shown != 6 {
		t.Fatalf("attention totals = shown:%d total:%d items:%#v", view.Shown, view.Total, view.Items)
	}
	if view.Counts[attention.KindQueuedMessage] != 1 || view.Counts[attention.KindBlockedClaim] != 1 || view.Counts[attention.KindStaleWorker] != 1 ||
		view.Counts[attention.KindValidatorRejected] != 1 || view.Counts[attention.KindFailedGate] != 1 || view.Counts[attention.KindPullRequest] != 1 {
		t.Fatalf("attention counts = %#v", view.Counts)
	}

	out.Reset()
	if err := c.run([]string{"attention", "--state", state, "--worker", "w-active", "--kind", "queued_message,failed_gate", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Total != 2 || view.Items[0].Kind != attention.KindFailedGate || view.Items[1].Kind != attention.KindQueuedMessage {
		t.Fatalf("filtered attention = %#v", view)
	}
	if err := c.run([]string{"attention", "--state", state, "--kind", "invented"}); err == nil {
		t.Fatal("attention accepted unknown kind")
	}
}
