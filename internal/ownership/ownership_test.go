package ownership

import (
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCheckWorkerReportsWarnings(t *testing.T) {
	now := time.Date(2026, 7, 9, 19, 0, 0, 0, time.UTC)
	worker := store.Worker{
		ID:          "w-1",
		Issue:       "MTG-Thomas/codex-swarm#57",
		ProjectRoot: "/repo/a",
		Engine:      "mock",
	}
	report := CheckWorker(Input{
		Worker: worker,
		Repo:   "/repo/b",
		Issue:  "MTG-Thomas/codex-swarm#58",
		Claims: []store.Claim{{
			ID:        "c-other",
			WorkerID:  "w-2",
			Repo:      "/repo/b",
			Scope:     "internal",
			Status:    store.ClaimActive,
			ExpiresAt: now.Add(time.Hour),
		}},
		Now: now,
	})
	for _, code := range []string{"repo_mismatch", "issue_mismatch", "worktree_missing", "active_claim_warning"} {
		if !hasCheck(report, code, SeverityWarning) {
			t.Fatalf("report missing warning %s: %#v", code, report.Checks)
		}
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
}

func TestCheckWorkerReportsMatches(t *testing.T) {
	now := time.Date(2026, 7, 9, 19, 0, 0, 0, time.UTC)
	worker := store.Worker{
		ID:          "w-1",
		Issue:       "MTG-Thomas/codex-swarm#57",
		ProjectRoot: "/repo/a",
		Worktree:    "/repo/a/.codex-swarm/worktrees/w-1",
		Engine:      "mock",
	}
	report := CheckWorker(Input{
		Worker:   worker,
		Repo:     "/repo/a",
		Issue:    "MTG-Thomas/codex-swarm#57",
		Worktree: "/repo/a/.codex-swarm/worktrees/w-1",
		Now:      now,
	})
	for _, code := range []string{"repo_match", "issue_match", "worktree_match"} {
		if !hasCheck(report, code, SeverityOK) {
			t.Fatalf("report missing ok check %s: %#v", code, report.Checks)
		}
	}
	if !report.OK {
		t.Fatalf("report.OK = false, want true: %#v", report.Checks)
	}
}

func hasCheck(report Report, code string, severity Severity) bool {
	for _, check := range report.Checks {
		if check.Code == code && check.Severity == severity {
			return true
		}
	}
	return false
}
