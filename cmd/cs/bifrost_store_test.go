package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	bf "github.com/MTG-Thomas/codex-swarm/internal/bifrost"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestBifrostStoreRoundTrip(t *testing.T) {
	db := store.NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	adapter := bifrostStore{store: db}
	now := time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC)
	want := bf.Record{
		ID: "local-1", WorkerID: "worker-1", Target: "https://dev.example.test",
		Scope: "features/vendor", BaseRevision: "base-1", RemoteChangesetID: "remote-1",
		State: "validated", Validation: json.RawMessage(`{"valid":true}`),
		CommitSHA: "abc123", CreatedAt: now, UpdatedAt: now,
	}

	if err := adapter.SaveBifrostChangeset(want); err != nil {
		t.Fatalf("SaveBifrostChangeset() error = %v", err)
	}
	got, err := adapter.GetBifrostChangeset(want.ID)
	if err != nil {
		t.Fatalf("GetBifrostChangeset() error = %v", err)
	}
	if got.ID != want.ID || got.RemoteChangesetID != want.RemoteChangesetID || got.Target != want.Target || string(got.Validation) != string(want.Validation) {
		t.Fatalf("GetBifrostChangeset() = %#v, want key fields from %#v", got, want)
	}
	listed, err := adapter.ListBifrostChangesets()
	if err != nil {
		t.Fatalf("ListBifrostChangesets() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != want.ID {
		t.Fatalf("ListBifrostChangesets() = %#v, want one record", listed)
	}
}
