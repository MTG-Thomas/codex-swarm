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
