package claims

import (
	"runtime"
	"strings"
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

func TestConflictsUsesPlatformRepoPathSemantics(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	existing := store.Claim{
		ID:        "c-existing",
		Repo:      "C:/Repo",
		Scope:     "cmd/cs",
		Status:    store.ClaimActive,
		ExpiresAt: now.Add(time.Hour),
	}
	candidate := store.Claim{
		ID:        "c-candidate",
		Repo:      "c:/repo",
		Scope:     "cmd/cs/issue.go",
		Status:    store.ClaimActive,
		ExpiresAt: now.Add(time.Hour),
	}

	got := Conflicts(existing, candidate, now)
	if runtime.GOOS == "windows" {
		if !got {
			t.Fatal("Conflicts() = false, want true for Windows repo path casing")
		}
		return
	}
	if got {
		t.Fatal("Conflicts() = true, want false for case-sensitive platform")
	}
}

func TestConflictsUsesIssueScopeAcrossRepoPaths(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	existing := store.Claim{
		ID:        "c-remote",
		Repo:      "/remote/checkout",
		Scope:     "cmd/cs",
		Issue:     "MTG-Thomas/codex-swarm#42",
		Status:    store.ClaimActive,
		ExpiresAt: now.Add(time.Hour),
	}
	candidate := store.Claim{
		ID:        "c-local",
		Repo:      "/local/checkout",
		Scope:     "cmd/cs/issue.go",
		Issue:     "MTG-Thomas/codex-swarm#42",
		Status:    store.ClaimActive,
		ExpiresAt: now.Add(time.Hour),
	}

	if !Conflicts(existing, candidate, now) {
		t.Fatal("Conflicts() = false, want issue/scope conflict across checkout paths")
	}
	candidate.Issue = "MTG-Thomas/codex-swarm#43"
	if Conflicts(existing, candidate, now) {
		t.Fatal("Conflicts() = true, want different issues to fall back to repo matching")
	}
}

func TestValidateWorkerID(t *testing.T) {
	workers := []store.Worker{{ID: "w-known"}}

	if err := ValidateWorkerID("w-known", workers); err != nil {
		t.Fatalf("ValidateWorkerID(known) error = %v", err)
	}

	err := ValidateWorkerID("missing", workers)
	if err == nil {
		t.Fatal("ValidateWorkerID(missing) error = nil, want not found")
	}
	if !strings.Contains(err.Error(), `worker "missing" not found`) {
		t.Fatalf("ValidateWorkerID(missing) error = %v", err)
	}

	err = ValidateWorkerID("", workers)
	if err == nil {
		t.Fatal("ValidateWorkerID(empty) error = nil, want required worker")
	}
	if !strings.Contains(err.Error(), "claim create requires --worker") {
		t.Fatalf("ValidateWorkerID(empty) error = %v", err)
	}
}

func TestValidateWorkerForRepo(t *testing.T) {
	workers := []store.Worker{{
		ID:          "w-known",
		ProjectRoot: "C:/repo",
	}}

	if err := ValidateWorkerForRepo("w-known", "C:/repo", workers); err != nil {
		t.Fatalf("ValidateWorkerForRepo(matching repo) error = %v", err)
	}

	err := ValidateWorkerForRepo("w-known", "C:/other", workers)
	if err == nil {
		t.Fatal("ValidateWorkerForRepo(wrong repo) error = nil, want repo mismatch")
	}
	if !strings.Contains(err.Error(), `worker "w-known" is for repo`) {
		t.Fatalf("ValidateWorkerForRepo(wrong repo) error = %v", err)
	}
}

func TestWorkerMatchesRepoUsesPlatformPathSemantics(t *testing.T) {
	worker := store.Worker{
		ID:          "w-known",
		ProjectRoot: "C:/Repo",
	}
	got := WorkerMatchesRepo(worker, "c:/repo")
	if runtime.GOOS == "windows" {
		if !got {
			t.Fatal("WorkerMatchesRepo() = false, want true for Windows path casing")
		}
		return
	}
	if got {
		t.Fatal("WorkerMatchesRepo() = true, want false for case-sensitive platform")
	}
}

func TestMarkExternalWorkerKeepsHistoricalWorkerID(t *testing.T) {
	claim := MarkExternalWorker(store.Claim{
		ID:       "legacy-active",
		WorkerID: "legacy-owner",
		Note:     "legacy active",
	})

	if claim.WorkerID != "legacy-owner" {
		t.Fatalf("WorkerID = %q, want historical worker id", claim.WorkerID)
	}
	if !IsExternalWorker(claim) {
		t.Fatalf("IsExternalWorker(%#v) = false, want true", claim)
	}
	if !claim.ExternalWorker {
		t.Fatalf("ExternalWorker = false, want true")
	}
	if !strings.Contains(claim.Note, "legacy active") {
		t.Fatalf("Note = %q, want legacy note preserved", claim.Note)
	}
	if strings.Contains(claim.Note, "[external]") {
		t.Fatalf("Note = %q, want structured external marker outside note", claim.Note)
	}
}

func TestIsExternalWorkerReadsLegacyNoteMarker(t *testing.T) {
	claim := store.Claim{
		ID:       "legacy-marker",
		WorkerID: "legacy-owner",
		Note:     "[external] legacy active",
	}

	if !IsExternalWorker(claim) {
		t.Fatalf("IsExternalWorker(%#v) = false, want legacy marker readable", claim)
	}
}

func TestTypedScopesDoNotCrossKinds(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	pathClaim := store.Claim{ID: "c-path", Repo: "/repo", ScopeKind: store.ClaimScopePath, Scope: "cmd/cs", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)}
	taskClaim := store.Claim{ID: "c-task", Repo: "/repo", ScopeKind: store.ClaimScopeTask, Scope: "cmd/cs", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)}
	if Conflicts(pathClaim, taskClaim, now) {
		t.Fatal("path and task claims conflicted")
	}
	taskChild := taskClaim
	taskChild.ID = "c-task-child"
	taskChild.Scope = "cmd/cs/child"
	if Conflicts(taskClaim, taskChild, now) {
		t.Fatal("task claims used path-prefix overlap")
	}
}

func TestNormalizeScopeSupportsTypedLiveHierarchyAndRejectsCommaPacking(t *testing.T) {
	kind, value, err := NormalizeScope(store.ClaimScopePath, "live:pve-t340/vm114")
	if err != nil {
		t.Fatal(err)
	}
	if kind != store.ClaimScopeLive || value != "pve-t340/vm114" {
		t.Fatalf("NormalizeScope() = %s:%s", kind, value)
	}
	if _, _, err := NormalizeScope(store.ClaimScopePath, "cmd/cs,internal/store"); err == nil {
		t.Fatal("NormalizeScope(comma-packed) error = nil")
	}
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	parent := store.Claim{ID: "c-parent", Repo: "/repo", ScopeKind: store.ClaimScopeLive, Scope: "pve-t340", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)}
	child := store.Claim{ID: "c-child", Repo: "/repo", ScopeKind: store.ClaimScopeLive, Scope: "pve-t340/vm114", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)}
	if !Conflicts(parent, child, now) {
		t.Fatal("hierarchical live scopes did not conflict")
	}
}
