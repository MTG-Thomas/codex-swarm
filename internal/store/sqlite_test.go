package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestJSONStoreMigratesLegacyJSONToSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Now().UTC().Round(0)
	legacy := stateFile{
		Workers:            []Worker{{ID: "w-legacy", Issue: "MTG-Thomas/codex-swarm#59", ProjectRoot: `C:\src\codex-swarm`, Worktree: `C:\src\codex-swarm-wt-sqlite`, ThreadID: "thread-legacy", Status: WorkerRunning, UpdatedAt: now}},
		Schedules:          []Schedule{{ID: "sched-legacy", Repo: "/repo", Prompt: "steward", Cron: "0 * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now}},
		Claims:             []Claim{{ID: "claim-legacy", WorkerID: "w-legacy", Repo: "/repo", Scope: "internal/store", Issue: "MTG-Thomas/codex-swarm#59", Status: ClaimActive, Note: "migration", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}},
		Agents:             []Agent{{ID: "agent-legacy", Name: "SQLite steward", Role: "implementer", Current: true, CreatedAt: now, UpdatedAt: now}},
		Events:             []Event{{At: now, Type: "migration", Message: "legacy event", WorkerID: "w-legacy", RequestID: "request-legacy"}},
		TraceLanes:         []TraceLane{{Agent: "agent-legacy", Stack: []TraceItem{{Title: "migration", Key: "legacy", StartedAt: now}}, Events: []TraceEvent{{At: now, Type: "push", Title: "migration", Depth: 1}}, CreatedAt: now, UpdatedAt: now}},
		GateEvidence:       []GateEvidence{{ID: "gate-legacy", GateID: "go-test", WorkerID: "w-legacy", Repo: "/repo", Scope: "internal/store", Command: "go test ./internal/store", ExitCode: 0, Output: "ok", Commit: "abc123", CreatedAt: now}},
		CompletedMutations: []CompletedMutation{{RequestID: "request-legacy", Command: "worker update", Fingerprint: "fingerprint-legacy", Output: "updated", CreatedAt: now}},
		BifrostChangesets: []BifrostChangeset{{
			ID: "chg-legacy", WorkerID: "w-legacy", Target: "dev", Scope: "features/demo", State: "opened", Validation: json.RawMessage(`{"ok":true}`), CreatedAt: now, UpdatedAt: now,
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
	if worker.ID != "w-legacy" || worker.Issue != "MTG-Thomas/codex-swarm#59" || worker.ProjectRoot != `C:\src\codex-swarm` || worker.Worktree != `C:\src\codex-swarm-wt-sqlite` || worker.ThreadID != "thread-legacy" || worker.Lifecycle == nil {
		t.Fatalf("GetWorker() = %#v", worker)
	}
	changeset, err := st.GetBifrostChangeset("chg-legacy")
	if err != nil || changeset.Scope != "features/demo" {
		t.Fatalf("GetBifrostChangeset() = %#v, %v", changeset, err)
	}
	var migratedState stateFile
	if err := st.withStateLock(func() error {
		var err error
		migratedState, err = st.read()
		return err
	}); err != nil {
		t.Fatalf("read migrated state: %v", err)
	}
	for name, values := range map[string][2]any{
		"schedules":           {legacy.Schedules, migratedState.Schedules},
		"claims":              {legacy.Claims, migratedState.Claims},
		"agents":              {legacy.Agents, migratedState.Agents},
		"events":              {legacy.Events, migratedState.Events},
		"trace lanes":         {legacy.TraceLanes, migratedState.TraceLanes},
		"gate evidence":       {legacy.GateEvidence, migratedState.GateEvidence},
		"completed mutations": {legacy.CompletedMutations, migratedState.CompletedMutations},
		"changesets":          {legacy.BifrostChangesets, migratedState.BifrostChangesets},
	} {
		if !reflect.DeepEqual(values[0], values[1]) {
			t.Errorf("migrated %s = %#v, want %#v", name, values[1], values[0])
		}
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
	if _, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "post-migration", HostID: "local", Source: "test", ObservedAt: now, Tasks: []CodexTaskObservation{{ThreadID: "thread-index"}}}); err != nil {
		t.Fatalf("IngestCodexTasks() after legacy migration error = %v", err)
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

func TestSaveWorkerMutatesOnlyTargetRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st := NewJSONStore(path)
	if err := st.SaveWorker(Worker{ID: "w-target", Status: WorkerRunning}); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)`, "schedule", "malformed", []byte(`{`), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	worker := Worker{ID: "w-target", Status: WorkerDone, UpdatedAt: time.Now().UTC()}
	if err := st.SaveWorker(worker); err != nil {
		t.Fatalf("SaveWorker() read or rewrote unrelated record: %v", err)
	}
	db, err = openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close SQLite: %v", err)
		}
	}()
	var payload []byte
	if err := db.QueryRowContext(context.Background(), `SELECT payload FROM records WHERE kind = ? AND id = ?`, "worker", worker.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var got Worker
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != WorkerDone {
		t.Fatalf("worker status = %q, want %q", got.Status, WorkerDone)
	}
	var malformed []byte
	if err := db.QueryRowContext(context.Background(), `SELECT payload FROM records WHERE kind = ? AND id = ?`, "schedule", "malformed").Scan(&malformed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(malformed, []byte(`{`)) {
		t.Fatalf("malformed schedule payload = %q, want byte-exact %q", malformed, []byte(`{`))
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
	older, err := st.UpdateBifrostChangeset(older.ID, func(changeset *BifrostChangeset) error {
		changeset.State = "committed"
		changeset.CommitSHA = "abc123"
		changeset.ID = "must-not-change"
		return nil
	})
	if err != nil || older.ID != "chg-1" {
		t.Fatalf("UpdateBifrostChangeset() = %#v, %v", older, err)
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
	if err := st.DeleteBifrostChangeset("chg-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetBifrostChangeset("chg-1"); !errors.Is(err, ErrBifrostChangesetNotFound) {
		t.Fatalf("deleted changeset error = %v", err)
	}
	if err := st.DeleteBifrostChangeset("chg-1"); !errors.Is(err, ErrBifrostChangesetNotFound) {
		t.Fatalf("delete missing changeset error = %v", err)
	}
}

func TestWriteStateDeletesLastRecordOfKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := NewJSONStore(path)
	now := time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC)
	if err := s.SaveClaim(Claim{ID: "claim-1", WorkerID: "worker-1", Repo: "/repo", Scope: "features", Status: ClaimActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	if err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		state.Claims = nil
		return s.write(state)
	}); err != nil {
		t.Fatalf("clear final claim error = %v", err)
	}
	claims, err := s.ListClaims()
	if err != nil {
		t.Fatalf("ListClaims() error = %v", err)
	}
	if len(claims) != 0 {
		t.Fatalf("ListClaims() = %#v, want empty", claims)
	}
}
