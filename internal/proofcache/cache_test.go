package proofcache

import (
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestLookupReturnsMatchingProof(t *testing.T) {
	now := time.Date(2026, 6, 27, 19, 20, 0, 0, time.UTC)
	proof, err := Lookup([]store.GateEvidence{{
		ID:        "g-1",
		GateID:    "test",
		WorkerID:  "w-proof",
		Repo:      "/repo",
		Command:   "go test ./...",
		ExitCode:  0,
		Output:    "ok ./...",
		Commit:    "abc123",
		CreatedAt: now,
	}}, Query{
		Repo:          "/repo",
		GateID:        "test",
		Command:       "go test ./...",
		Head:          "abc123",
		NotBefore:     now.Add(-time.Minute),
		RequirePassed: true,
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if proof.ID != "g-1" || proof.WorkerID != "w-proof" || proof.Output != "ok ./..." {
		t.Fatalf("proof = %#v, want cached proof with provenance and output", proof)
	}
}

func TestLookupRejectsStaleProof(t *testing.T) {
	now := time.Date(2026, 6, 27, 19, 25, 0, 0, time.UTC)
	_, err := Lookup([]store.GateEvidence{{
		ID:        "g-stale",
		GateID:    "test",
		Repo:      "/repo",
		Command:   "go test ./...",
		ExitCode:  0,
		Commit:    "abc123",
		CreatedAt: now.Add(-time.Hour),
	}}, Query{
		Repo:          "/repo",
		GateID:        "test",
		Command:       "go test ./...",
		Head:          "abc123",
		NotBefore:     now,
		RequirePassed: true,
	})
	if err == nil || !strings.Contains(err.Error(), "stale evidence") {
		t.Fatalf("Lookup() error = %v, want stale evidence", err)
	}
}

func TestLookupRejectsCommandMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 19, 30, 0, 0, time.UTC)
	_, err := Lookup([]store.GateEvidence{{
		ID:        "g-command",
		GateID:    "test",
		Repo:      "/repo",
		Command:   "go test ./cmd/cs",
		ExitCode:  0,
		Commit:    "abc123",
		CreatedAt: now,
	}}, Query{
		Repo:          "/repo",
		GateID:        "test",
		Command:       "go test ./...",
		Head:          "abc123",
		NotBefore:     now.Add(-time.Minute),
		RequirePassed: true,
	})
	if err == nil || !strings.Contains(err.Error(), "command mismatch") {
		t.Fatalf("Lookup() error = %v, want command mismatch", err)
	}
}

func TestLookupRejectsHeadMismatch(t *testing.T) {
	now := time.Date(2026, 6, 27, 19, 35, 0, 0, time.UTC)
	_, err := Lookup([]store.GateEvidence{{
		ID:        "g-head",
		GateID:    "test",
		Repo:      "/repo",
		Command:   "go test ./...",
		ExitCode:  0,
		Commit:    "old",
		CreatedAt: now,
	}}, Query{
		Repo:          "/repo",
		GateID:        "test",
		Command:       "go test ./...",
		Head:          "new",
		NotBefore:     now.Add(-time.Minute),
		RequirePassed: true,
	})
	if err == nil || !strings.Contains(err.Error(), "HEAD mismatch") {
		t.Fatalf("Lookup() error = %v, want HEAD mismatch", err)
	}
}
