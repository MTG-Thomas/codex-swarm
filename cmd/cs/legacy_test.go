package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	claimrules "github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestLegacyImportCoordinator(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "claims.json")
	state := filepath.Join(dir, "state.json")
	data := `[
  {
    "id": "active-id",
    "owner": "legacy-owner",
    "repo": "C:\\repo",
    "scope": "internal/store",
    "status": "active",
    "note": "legacy active",
    "created_at": "2026-06-25T11:00:00Z",
    "expires_at": "2026-06-25T13:00:00Z"
  },
  {
    "id": "released-id",
    "owner": "legacy-owner",
    "repo": "C:\\repo",
    "scope": "cmd/cs",
    "status": "released",
    "note": "legacy released",
    "created_at": "2026-06-25T10:00:00Z",
    "expires_at": "2026-06-25T13:00:00Z"
  }
]`
	if err := os.WriteFile(source, []byte(data), 0o600); err != nil {
		t.Fatalf("write legacy source: %v", err)
	}

	if err := c.run([]string{"legacy", "import-coordinator", "--state", state, "--source", source}); err != nil {
		t.Fatalf("legacy import error = %v", err)
	}
	if !strings.Contains(out.String(), "imported=1 skipped=1") {
		t.Fatalf("legacy import output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "list", "--state", state}); err != nil {
		t.Fatalf("claim list error = %v", err)
	}
	if !strings.Contains(out.String(), "legacy-active-id") || strings.Contains(out.String(), "legacy-released-id") {
		t.Fatalf("claim list output = %q", out.String())
	}

	claim, err := store.NewJSONStore(state).GetClaim("legacy-active-id")
	if err != nil {
		t.Fatalf("GetClaim(legacy-active-id) error = %v", err)
	}
	if claim.WorkerID != "legacy-owner" {
		t.Fatalf("WorkerID = %q, want legacy-owner", claim.WorkerID)
	}
	if claim.WorkerSource != "legacy-coordinator" {
		t.Fatalf("WorkerSource = %q, want legacy-coordinator", claim.WorkerSource)
	}
	if !claimrules.IsExternalWorker(claim) {
		t.Fatalf("legacy claim = %#v, want external marker", claim)
	}
}
