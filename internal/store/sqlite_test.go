package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestJSONStoreMigratesLegacyJSONToSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Now().UTC().Round(0)
	legacy := stateFile{
		Workers: []Worker{{ID: "w-legacy", Status: WorkerRunning, UpdatedAt: now}},
		BifrostChangesets: []BifrostChangeset{{
			ID: "chg-legacy", WorkerID: "w-legacy", Target: "dev", Scope: "features/demo", State: "opened", UpdatedAt: now,
		}},
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	st := NewJSONStore(path)
	worker, err := st.GetWorker("w-legacy")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if worker.ID != "w-legacy" || worker.Lifecycle == nil {
		t.Fatalf("GetWorker() = %#v", worker)
	}
	changeset, err := st.GetBifrostChangeset("chg-legacy")
	if err != nil || changeset.Scope != "features/demo" {
		t.Fatalf("GetBifrostChangeset() = %#v, %v", changeset, err)
	}

	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(migrated, []byte(sqliteHeader)) {
		t.Fatalf("state header = %q, want SQLite", migrated[:min(len(migrated), len(sqliteHeader))])
	}
	backup, err := os.ReadFile(path + ".legacy.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, data) {
		t.Fatal("legacy backup does not exactly match imported JSON")
	}

	// Reopening is idempotent and leaves the original backup untouched.
	if _, err := NewJSONStore(path).ListWorkers(); err != nil {
		t.Fatalf("ListWorkers() after migration error = %v", err)
	}
	backupAgain, err := os.ReadFile(path + ".legacy.json")
	if err != nil || !bytes.Equal(backupAgain, data) {
		t.Fatalf("backup after reopen changed: err=%v", err)
	}
}

func TestJSONStoreConcurrentWritersUseSQLiteWithoutLostUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("w-%02d", i)
			err := NewJSONStore(path).SaveWorker(Worker{ID: id, Status: WorkerRunning, UpdatedAt: time.Now().UTC()})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("SaveWorker() error = %v", err)
		}
	}
	workers, err := NewJSONStore(path).ListWorkers()
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != writers {
		t.Fatalf("worker count = %d, want %d", len(workers), writers)
	}
}

func TestBifrostChangesetCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st := NewJSONStore(path)
	now := time.Now().UTC()
	older := BifrostChangeset{ID: "chg-1", WorkerID: "w-1", Target: "dev", Scope: "apps/demo", State: "opened", Validation: json.RawMessage(`{"ok":true}`), UpdatedAt: now.Add(-time.Minute)}
	newer := BifrostChangeset{ID: "chg-2", WorkerID: "w-2", Target: "dev", Scope: "features/demo", State: "validated", UpdatedAt: now}
	for _, changeset := range []BifrostChangeset{newer, older} {
		if err := st.SaveBifrostChangeset(changeset); err != nil {
			t.Fatal(err)
		}
	}
	older.State = "committed"
	older.CommitSHA = "abc123"
	if err := st.SaveBifrostChangeset(older); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetBifrostChangeset("chg-1")
	if err != nil || got.CommitSHA != "abc123" {
		t.Fatalf("GetBifrostChangeset() = %#v, %v", got, err)
	}
	listed, err := st.ListBifrostChangesets()
	if err != nil || len(listed) != 2 || listed[0].ID != "chg-2" {
		t.Fatalf("ListBifrostChangesets() = %#v, %v", listed, err)
	}
	_, err = st.GetBifrostChangeset("missing")
	if !errors.Is(err, ErrBifrostChangesetNotFound) {
		t.Fatalf("missing error = %v", err)
	}
}
