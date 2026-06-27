package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestClaimCreateRequiresWorker(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	err := c.run([]string{"claim", "create", "--state", state, "--repo", ".", "--scope", "cmd/cs"})
	if err == nil {
		t.Fatal("claim create error = nil, want required worker failure")
	}
	if !strings.Contains(err.Error(), "claim create requires --worker") {
		t.Fatalf("claim create error = %v", err)
	}
}

func TestClaimCreateRejectsUnknownWorker(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	err := c.run([]string{"claim", "create", "--state", state, "--repo", ".", "--scope", "cmd/cs", "--worker", "missing"})
	if err == nil {
		t.Fatal("claim create error = nil, want unknown worker failure")
	}
	if !strings.Contains(err.Error(), `worker "missing" not found`) {
		t.Fatalf("claim create error = %v", err)
	}

	claims, listErr := store.NewJSONStore(state).ListClaims()
	if listErr != nil {
		t.Fatalf("ListClaims() error = %v", listErr)
	}
	if len(claims) != 0 {
		t.Fatalf("ListClaims() = %#v, want no claim saved", claims)
	}
}

func TestClaimCreateRejectsWrongRepoWorker(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	repo := filepath.Join(dir, "repo")
	otherRepo := filepath.Join(dir, "other")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-other",
		ProjectRoot: otherRepo,
		ThreadID:    "thread-other",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "other repo worker",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	err := c.run([]string{"claim", "create", "--state", state, "--repo", repo, "--scope", "cmd/cs", "--worker", "w-other"})
	if err == nil {
		t.Fatal("claim create error = nil, want wrong-repo worker failure")
	}
	if !strings.Contains(err.Error(), `worker "w-other" is for repo`) {
		t.Fatalf("claim create error = %v", err)
	}
	claims, listErr := store.NewJSONStore(state).ListClaims()
	if listErr != nil {
		t.Fatalf("ListClaims() error = %v", listErr)
	}
	if len(claims) != 0 {
		t.Fatalf("ListClaims() = %#v, want no claim saved", claims)
	}
}

func TestClaimCreateAcceptsKnownWorker(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-known",
		ProjectRoot: ".",
		ThreadID:    "thread-known",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "known worker",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	err := c.run([]string{"claim", "create", "--state", state, "--repo", ".", "--scope", "cmd/cs", "--worker", "w-known"})
	if err != nil {
		t.Fatalf("claim create error = %v", err)
	}
	if !strings.Contains(out.String(), "worker=w-known") {
		t.Fatalf("claim create output = %q", out.String())
	}
	claim, err := store.NewJSONStore(state).GetClaim("c-20260625-120000")
	if err != nil {
		t.Fatalf("GetClaim() error = %v", err)
	}
	if claim.WorkerID != "w-known" {
		t.Fatalf("WorkerID = %q, want w-known", claim.WorkerID)
	}
}

func TestClaimReleaseAndBlockPreserveProvenance(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	st := store.NewJSONStore(state)
	if err := st.SaveClaim(store.Claim{
		ID:             "c-external",
		WorkerID:       "remote-worker",
		Repo:           "C:/repo",
		Scope:          "cmd/cs",
		Status:         store.ClaimActive,
		Note:           "remote claim",
		ExternalWorker: true,
		WorkerSource:   "issue:remote-machine",
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now.Add(time.Minute) }}

	if err := c.run([]string{"claim", "release", "--state", state, "--note", "released", "c-external"}); err != nil {
		t.Fatalf("claim release error = %v", err)
	}
	released, err := st.GetClaim("c-external")
	if err != nil {
		t.Fatalf("GetClaim(released) error = %v", err)
	}
	if !released.ExternalWorker || released.WorkerSource != "issue:remote-machine" {
		t.Fatalf("released claim provenance = external:%t source:%q, want preserved", released.ExternalWorker, released.WorkerSource)
	}

	if err := c.run([]string{"claim", "block", "--state", state, "--reason", "blocked", "--next", "retry", "c-external"}); err != nil {
		t.Fatalf("claim block error = %v", err)
	}
	blocked, err := st.GetClaim("c-external")
	if err != nil {
		t.Fatalf("GetClaim(blocked) error = %v", err)
	}
	if !blocked.ExternalWorker || blocked.WorkerSource != "issue:remote-machine" {
		t.Fatalf("blocked claim provenance = external:%t source:%q, want preserved", blocked.ExternalWorker, blocked.WorkerSource)
	}
}
