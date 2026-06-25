package claims

import (
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestFindConflicts(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	candidate := store.Claim{ID: "c-new", Repo: "C:/repo", Scope: "internal/store", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)}
	all := []store.Claim{
		{ID: "c-parent", Repo: "C:/repo", Scope: "internal", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)},
		{ID: "c-child", Repo: "C:/repo", Scope: "internal/store/json.go", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)},
		{ID: "c-other", Repo: "C:/repo", Scope: "cmd/cs", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)},
		{ID: "c-expired", Repo: "C:/repo", Scope: "internal/store", Status: store.ClaimActive, ExpiresAt: now.Add(-time.Minute)},
		{ID: "c-released", Repo: "C:/repo", Scope: "internal/store", Status: store.ClaimReleased, ExpiresAt: now.Add(time.Hour)},
	}

	conflicts := FindConflicts(all, candidate, now)
	if len(conflicts) != 2 {
		t.Fatalf("conflicts = %#v, want two parent/child conflicts", conflicts)
	}
	if conflicts[0].ID != "c-parent" || conflicts[1].ID != "c-child" {
		t.Fatalf("conflicts = %#v", conflicts)
	}
}
