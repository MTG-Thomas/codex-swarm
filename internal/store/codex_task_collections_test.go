package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestCodexTaskCollectionStagesReplaysAndFinalizesCompleteSnapshot(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	firstAt := time.Date(2026, 7, 22, 18, 0, 0, 123, time.UTC)
	waitCursor := "  wait-one  "
	coordinator := true
	first := CodexTaskCollectionPageRequest{
		HostID: " local ", ObservationID: " observation-1 ", Page: 1,
		ObservedAt: firstAt, NextCursor: "  page-two  ",
		Tasks: []CodexTaskHostObservation{{
			ThreadID: " thread-one ", Title: " One ", Status: "active", Unread: true,
			WaitCursor: &waitCursor, Coordinator: &coordinator,
		}},
	}
	page, err := st.AddCodexTaskCollectionPage(first)
	if err != nil {
		t.Fatal(err)
	}
	if page.HostID != "local" || page.ObservationID != "observation-1" || page.Page != 1 || page.Tasks != 1 || page.Replayed || !page.ObservedAt.Equal(firstAt) {
		t.Fatalf("first page = %#v", page)
	}
	progress, err := st.GetCodexTaskCollectionStatus(" local ", " observation-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Pages != 1 || progress.Tasks != 1 || progress.NextPage != 2 || progress.NextCursor != "  page-two  " || progress.FinalizedAt != nil {
		t.Fatalf("collection progress = %#v", progress)
	}

	// ObservedAt is caller wall time, not part of page replay identity. The
	// durable manifest returns the first page's ordering timestamp.
	first.ObservedAt = firstAt.Add(time.Hour)
	replay, err := st.AddCodexTaskCollectionPage(first)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || !replay.ObservedAt.Equal(firstAt) {
		t.Fatalf("page replay = %#v", replay)
	}

	second, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{
		HostID: "local", ObservationID: "observation-1", Page: 2,
		Cursor: "  page-two  ", ObservedAt: firstAt.Add(2 * time.Hour),
		Tasks: []CodexTaskHostObservation{{ThreadID: "thread-two", Status: "idle", Tombstoned: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.ObservedAt.Equal(firstAt) {
		t.Fatalf("second page observed_at = %v, want %v", second.ObservedAt, firstAt)
	}

	finished, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{
		HostID: " local ", ObservationID: " observation-1 ", Coverage: " COMPLETE ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.ObservationID != "observation-1" || finished.Pages != 2 || finished.Replayed || finished.Ingest.Observed != 2 || finished.Ingest.Inserted != 2 || finished.Ingest.Source != CodexTaskCollectionSource || !finished.Ingest.ObservedAt.Equal(firstAt) {
		t.Fatalf("finish = %#v", finished)
	}
	if finished.Ingest.RequestID != codexTaskCollectionIngestID("local", "observation-1") {
		t.Fatalf("ingest request id = %q", finished.Ingest.RequestID)
	}

	listed, err := st.ListCodexTasks(CodexTaskListFilter{HostID: "local", IncludeTombstoned: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tasks) != 2 || listed.Tasks[0].ThreadID != "thread-one" || listed.Tasks[0].WaitCursor != "  wait-one  " || !listed.Tasks[0].Coordinator || listed.Tasks[1].ThreadID != "thread-two" || listed.Tasks[1].TombstonedAt == nil {
		t.Fatalf("listed tasks = %#v", listed.Tasks)
	}

	var staged, compact int
	if err := st.withStateLock(func() error {
		return st.tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(LENGTH(tasks)),0) FROM codex_task_collection_pages WHERE host_id=? AND observation_id=?`, "local", "observation-1").Scan(&staged, &compact)
	}); err != nil {
		t.Fatal(err)
	}
	if staged != 2 || compact != 4 {
		t.Fatalf("compacted pages count=%d payload bytes=%d, want 2 pages of []", staged, compact)
	}

	finishReplay, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{HostID: "local", ObservationID: "observation-1", Coverage: CodexTaskCoverageComplete})
	if err != nil {
		t.Fatal(err)
	}
	if !finishReplay.Replayed || finishReplay.Ingest.Replayed {
		t.Fatalf("finish replay = %#v", finishReplay)
	}
	progress, err = st.GetCodexTaskCollectionStatus("local", "observation-1")
	if err != nil {
		t.Fatal(err)
	}
	if progress.FinalizedAt == nil || progress.Coverage != CodexTaskCoverageComplete || progress.NextPage != 0 {
		t.Fatalf("final collection progress = %#v", progress)
	}
	_, err = st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{HostID: "local", ObservationID: "observation-1", Coverage: CodexTaskCoverageWindow})
	if !errors.Is(err, ErrCodexTaskCollectionReplayMismatch) {
		t.Fatalf("finish mismatch error = %v", err)
	}
	pageReplay, err := st.AddCodexTaskCollectionPage(first)
	if err != nil || !pageReplay.Replayed {
		t.Fatalf("page replay after finish = %#v, %v", pageReplay, err)
	}
	_, err = st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "local", ObservationID: "observation-1", Page: 3, ObservedAt: firstAt})
	if !errors.Is(err, ErrCodexTaskCollectionFinalized) {
		t.Fatalf("new page after finish error = %v", err)
	}
}

func TestCodexTaskCollectionPrunesStaleOpenStateAndCapsActiveCollections(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Now().UTC()
	old := formatDBTime(now.Add(-staleCodexTaskCollectionAge - time.Hour))
	if err := st.withStateLock(func() error {
		if _, err := st.tx.Exec(`INSERT INTO codex_task_collections(host_id,observation_id,observed_at,page_count,task_count,created_at) VALUES(?,?,?,?,?,?)`, "host", "stale", old, 1, 0, old); err != nil {
			return err
		}
		_, err := st.tx.Exec(`INSERT INTO codex_task_collection_pages(host_id,observation_id,page_number,cursor,next_cursor,fingerprint,tasks,task_count,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, "host", "stale", 1, "", "", "fingerprint", []byte(`[]`), 0, old)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "fresh", Page: 1, ObservedAt: now.Add(-365 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := st.withStateLock(func() error {
		var collections, pages int
		if err := st.tx.QueryRow(`SELECT COUNT(*) FROM codex_task_collections WHERE observation_id='stale'`).Scan(&collections); err != nil {
			return err
		}
		if err := st.tx.QueryRow(`SELECT COUNT(*) FROM codex_task_collection_pages WHERE observation_id='stale'`).Scan(&pages); err != nil {
			return err
		}
		if collections != 0 || pages != 0 {
			return fmt.Errorf("stale collection remains collections=%d pages=%d", collections, pages)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	created := formatDBTime(now)
	if err := st.withStateLock(func() error {
		for i := 1; i < maxOpenCodexTaskCollections; i++ {
			if _, err := st.tx.Exec(`INSERT INTO codex_task_collections(host_id,observation_id,observed_at,page_count,task_count,created_at) VALUES(?,?,?,?,?,?)`, "host", fmt.Sprintf("open-%04d", i), created, 0, 0, created); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "overflow", Page: 1, ObservedAt: now})
	if err == nil || !strings.Contains(err.Error(), "open codex task collections") {
		t.Fatalf("open collection limit error = %v", err)
	}
}

func TestCodexTaskCollectionRejectsPageReplayMismatchAndBrokenChains(t *testing.T) {
	newStore := func(t *testing.T) *JSONStore {
		t.Helper()
		return NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	}
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)

	t.Run("page one required", func(t *testing.T) {
		st := newStore(t)
		_, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 2, Cursor: "next", ObservedAt: now})
		if !errors.Is(err, ErrCodexTaskCollectionNotFound) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("replay mismatch", func(t *testing.T) {
		st := newStore(t)
		request := CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 1, ObservedAt: now, Tasks: []CodexTaskHostObservation{{ThreadID: "one"}}}
		if _, err := st.AddCodexTaskCollectionPage(request); err != nil {
			t.Fatal(err)
		}
		request.Tasks[0].Title = "changed"
		_, err := st.AddCodexTaskCollectionPage(request)
		if !errors.Is(err, ErrCodexTaskCollectionReplayMismatch) {
			t.Fatalf("error = %v", err)
		}
	})

	tests := []struct {
		name      string
		firstNext string
		second    CodexTaskCollectionPageRequest
		want      string
	}{
		{name: "page one cursor", second: CodexTaskCollectionPageRequest{Page: 1, Cursor: "unexpected"}, want: "page 1 cursor"},
		{name: "skip page", firstNext: "two", second: CodexTaskCollectionPageRequest{Page: 3, Cursor: "two"}, want: "not contiguous"},
		{name: "after terminal", second: CodexTaskCollectionPageRequest{Page: 2}, want: "terminal page"},
		{name: "cursor mismatch", firstNext: "two", second: CodexTaskCollectionPageRequest{Page: 2, Cursor: "wrong"}, want: "does not match"},
		{name: "cursor cycle", firstNext: "two", second: CodexTaskCollectionPageRequest{Page: 2, Cursor: "two", NextCursor: "two"}, want: "repeats"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := newStore(t)
			if test.second.Page != 1 {
				if _, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 1, NextCursor: test.firstNext, ObservedAt: now}); err != nil {
					t.Fatal(err)
				}
			}
			test.second.HostID = "host"
			test.second.ObservationID = "obs"
			test.second.ObservedAt = now
			_, err := st.AddCodexTaskCollectionPage(test.second)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestCodexTaskCollectionRejectsDuplicateAndOversizedAggregates(t *testing.T) {
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	t.Run("duplicate across pages", func(t *testing.T) {
		st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
		if _, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 1, NextCursor: "two", ObservedAt: now, Tasks: []CodexTaskHostObservation{{ThreadID: "same"}}}); err != nil {
			t.Fatal(err)
		}
		_, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 2, Cursor: "two", ObservedAt: now, Tasks: []CodexTaskHostObservation{{ThreadID: "same"}}})
		if err == nil || !strings.Contains(err.Error(), "duplicate thread_id") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("aggregate limit", func(t *testing.T) {
		st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
		first := make([]CodexTaskHostObservation, 600)
		second := make([]CodexTaskHostObservation, 401)
		for i := range first {
			first[i].ThreadID = fmt.Sprintf("a-%03d", i)
		}
		for i := range second {
			second[i].ThreadID = fmt.Sprintf("b-%03d", i)
		}
		if _, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 1, NextCursor: "two", ObservedAt: now, Tasks: first}); err != nil {
			t.Fatal(err)
		}
		_, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 2, Cursor: "two", ObservedAt: now, Tasks: second})
		if err == nil || !strings.Contains(err.Error(), "exceeds 1000") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCodexTaskCollectionCompleteRequiresTerminalCursorButWindowCanFinalize(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	if _, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: "host", ObservationID: "obs", Page: 1, NextCursor: "more", ObservedAt: now, Tasks: []CodexTaskHostObservation{{ThreadID: "one"}}}); err != nil {
		t.Fatal(err)
	}
	_, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{HostID: "host", ObservationID: "obs", Coverage: CodexTaskCoverageComplete})
	if err == nil || !strings.Contains(err.Error(), "terminal empty next_cursor") {
		t.Fatalf("complete finish error = %v", err)
	}
	finished, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{HostID: "host", ObservationID: "obs", Coverage: CodexTaskCoverageWindow})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Ingest.Coverage != CodexTaskCoverageWindow || finished.Ingest.Observed != 1 {
		t.Fatalf("finish = %#v", finished)
	}
}

func TestCodexTaskCollectionFinalizationIsAtomicWithIngest(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	hostID, observationID := "host", "obs"
	if _, err := st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{HostID: hostID, ObservationID: observationID, Page: 1, ObservedAt: now, Tasks: []CodexTaskHostObservation{{ThreadID: "one"}}}); err != nil {
		t.Fatal(err)
	}
	// Occupy the deterministic ingest identity with different content to force
	// a replay mismatch inside finalization.
	if _, err := st.IngestCodexTasks(CodexTaskIngestRequest{
		RequestID: codexTaskCollectionIngestID(hostID, observationID), HostID: hostID,
		Source: CodexTaskCollectionSource, ObservedAt: now, Coverage: CodexTaskCoverageWindow,
		Tasks: []CodexTaskObservation{{ThreadID: "different"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{HostID: hostID, ObservationID: observationID, Coverage: CodexTaskCoverageComplete})
	if !errors.Is(err, ErrCodexTaskReplayMismatch) {
		t.Fatalf("finish error = %v", err)
	}
	var pages int
	var finalized sql.NullString
	if err := st.withStateLock(func() error {
		return st.tx.QueryRow(`SELECT (SELECT COUNT(*) FROM codex_task_collection_pages WHERE host_id=? AND observation_id=?),finalized_at FROM codex_task_collections WHERE host_id=? AND observation_id=?`, hostID, observationID, hostID, observationID).Scan(&pages, &finalized)
	}); err != nil {
		t.Fatal(err)
	}
	if pages != 1 || finalized.Valid {
		t.Fatalf("atomic rollback pages=%d finalized=%#v", pages, finalized)
	}
}

func TestCodexTaskCollectionSchemaIsAddedToExistingSQLiteStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE records (kind TEXT NOT NULL,id TEXT NOT NULL,payload BLOB NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(kind,id))`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st := NewJSONStore(path)
	_, err = st.AddCodexTaskCollectionPage(CodexTaskCollectionPageRequest{
		HostID: "host", ObservationID: "migration", Page: 1,
		ObservedAt: time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AddCodexTaskCollectionPage() on existing SQLite store: %v", err)
	}
	if _, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{HostID: "host", ObservationID: "migration", Coverage: CodexTaskCoverageComplete}); err != nil {
		t.Fatalf("FinishCodexTaskCollection() on existing SQLite store: %v", err)
	}
}

func TestCodexTaskCollectionReplayRetentionIsBounded(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	err := st.withStateLock(func() error {
		for i := range 4 {
			at := formatDBTime(time.Date(2026, 7, 22, 18, 0, i, 0, time.UTC))
			if _, err := st.tx.Exec(`INSERT INTO codex_task_collections(host_id,observation_id,observed_at,page_count,task_count,finish_fingerprint,finish_result,finalized_at,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, "host", fmt.Sprintf("obs-%d", i), at, 1, 0, "fingerprint", []byte(`{}`), at, at); err != nil {
				return err
			}
		}
		return pruneCodexTaskCollectionReplays(st.tx, 2)
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.withStateLock(func() error {
		return st.tx.QueryRow(`SELECT COUNT(*) FROM codex_task_collections WHERE finalized_at IS NOT NULL`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("finalized replay rows = %d, want 2", count)
	}
}

func TestCodexTaskCollectionValidatesInputs(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	tests := []CodexTaskCollectionPageRequest{
		{},
		{HostID: "host", ObservationID: "obs", ObservedAt: now},
		{HostID: "host", ObservationID: "obs", Page: 1},
		{HostID: "host", ObservationID: "obs", Page: 1, ObservedAt: now, Cursor: strings.Repeat("x", maxCodexTaskCollectionCursorLength+1)},
		{HostID: "host", ObservationID: "obs", Page: maxCodexTaskCollectionPages + 1, ObservedAt: now},
		{HostID: "host", ObservationID: "obs", Page: 1, ObservedAt: now, Tasks: []CodexTaskHostObservation{{ThreadID: "same"}, {ThreadID: "same"}}},
	}
	for i, request := range tests {
		if _, err := st.AddCodexTaskCollectionPage(request); err == nil {
			t.Fatalf("case %d unexpectedly succeeded", i)
		}
	}
	if _, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{}); err == nil {
		t.Fatal("empty finish unexpectedly succeeded")
	}
	if _, err := st.FinishCodexTaskCollection(CodexTaskCollectionFinishRequest{HostID: "host", ObservationID: "missing", Coverage: CodexTaskCoverageComplete}); !errors.Is(err, ErrCodexTaskCollectionNotFound) {
		t.Fatalf("missing finish error = %v", err)
	}
}
