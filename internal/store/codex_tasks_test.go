package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCodexTaskIndexRetainsMoreThanDiscoveryWindowAndPaginates(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 17, 30, 0, 0, time.UTC)
	tasks := make([]CodexTaskObservation, 75)
	for i := range tasks {
		tasks[i] = CodexTaskObservation{
			ThreadID: fmt.Sprintf("thread-%03d", i), Title: fmt.Sprintf("Task %03d", i),
			Project: "Bifrost Workspace", CWD: `C:\repo`, Status: "idle", Unread: i%5 == 0,
			CreatedAt: now.Add(-time.Duration(i) * time.Hour), UpdatedAt: now,
		}
	}
	result, err := st.IngestCodexTasks(CodexTaskIngestRequest{
		RequestID: "snapshot-75", HostID: "local", Source: "codex.list_threads",
		ObservedAt: now, Coverage: CodexTaskCoverageWindow, Tasks: tasks,
	})
	if err != nil {
		t.Fatalf("IngestCodexTasks() error = %v", err)
	}
	if result.Inserted != 75 || result.Observed != 75 {
		t.Fatalf("ingest result = %#v", result)
	}

	var all []CodexTask
	cursor := ""
	for {
		page, err := st.ListCodexTasks(CodexTaskListFilter{HostID: "local", Limit: 17, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListCodexTasks(cursor=%q) error = %v", cursor, err)
		}
		if page.Total != 75 {
			t.Fatalf("page total = %d, want 75", page.Total)
		}
		all = append(all, page.Tasks...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(all) != 75 {
		t.Fatalf("walked tasks = %d, want 75", len(all))
	}
	for i, task := range all {
		if want := fmt.Sprintf("thread-%03d", i); task.ThreadID != want {
			t.Fatalf("task[%d] = %q, want %q", i, task.ThreadID, want)
		}
	}
	stats, err := st.CodexTaskStats(nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 75 || stats.Unread != 15 || stats.ByHost["local"] != 75 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestCodexTaskIngestReplayAndMismatch(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	request := CodexTaskIngestRequest{
		RequestID: "snapshot-1", HostID: "local", Source: "codex.list_threads", ObservedAt: now,
		Tasks: []CodexTaskObservation{{ThreadID: "thread-1", Title: "One", Status: "active", WaitCursor: taskStringPtr("cursor-1")}},
	}
	first, err := st.IngestCodexTasks(request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := st.IngestCodexTasks(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || !replay.Replayed || replay.Inserted != 1 {
		t.Fatalf("first=%#v replay=%#v", first, replay)
	}
	request.Tasks[0].Title = "Different"
	_, err = st.IngestCodexTasks(request)
	if !errors.Is(err, ErrCodexTaskReplayMismatch) {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestCodexTaskWindowAbsenceIsNotDeletionAndCompleteCoverageMarksMissing(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	ingest := func(id string, at time.Time, coverage string, tasks ...CodexTaskObservation) CodexTaskIngestResult {
		t.Helper()
		result, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: id, HostID: "local", Source: "codex.list_threads", ObservedAt: at, Coverage: coverage, Tasks: tasks})
		if err != nil {
			t.Fatalf("ingest %s: %v", id, err)
		}
		return result
	}
	ingest("initial", now, CodexTaskCoverageWindow, CodexTaskObservation{ThreadID: "old", Status: "idle"})
	ingest("window-empty", now.Add(time.Minute), CodexTaskCoverageWindow)
	page, _ := st.ListCodexTasks(CodexTaskListFilter{Limit: 10})
	if len(page.Tasks) != 1 || page.Tasks[0].MissingSince != nil || page.Tasks[0].TombstonedAt != nil {
		t.Fatalf("window absence changed task = %#v", page.Tasks)
	}
	complete := ingest("complete-empty", now.Add(2*time.Minute), CodexTaskCoverageComplete)
	if complete.MissingMarked != 1 {
		t.Fatalf("complete result = %#v", complete)
	}
	page, _ = st.ListCodexTasks(CodexTaskListFilter{Limit: 10})
	if page.Tasks[0].MissingSince == nil {
		t.Fatalf("complete absence did not mark missing: %#v", page.Tasks[0])
	}
	sameTime := ingest("aaa-same-time", now.Add(2*time.Minute), CodexTaskCoverageWindow, CodexTaskObservation{ThreadID: "old", Status: "active"})
	if sameTime.StaleSkipped != 1 {
		t.Fatalf("same-time result = %#v", sameTime)
	}
	late := ingest("late-seen", now.Add(time.Minute), CodexTaskCoverageWindow, CodexTaskObservation{ThreadID: "old", Status: "active"})
	if late.StaleSkipped != 1 {
		t.Fatalf("late result = %#v", late)
	}
	page, _ = st.ListCodexTasks(CodexTaskListFilter{Limit: 10})
	if page.Tasks[0].MissingSince == nil || page.Tasks[0].Status != "idle" {
		t.Fatalf("late observation regressed missing state: %#v", page.Tasks[0])
	}
	ingest("reappeared", now.Add(3*time.Minute), CodexTaskCoverageWindow, CodexTaskObservation{ThreadID: "old", Status: "active"})
	page, _ = st.ListCodexTasks(CodexTaskListFilter{Limit: 10})
	if page.Tasks[0].MissingSince != nil || page.Tasks[0].Status != "active" {
		t.Fatalf("reappeared task = %#v", page.Tasks[0])
	}
}

func TestCodexTaskOlderSnapshotDoesNotRegressLatestObservation(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	_, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "new", HostID: "local", Source: "codex.list_threads", ObservedAt: now, Tasks: []CodexTaskObservation{{ThreadID: "one", Title: "New", Status: "active", Unread: true}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "old", HostID: "local", Source: "codex.list_threads", ObservedAt: now.Add(-time.Hour), Tasks: []CodexTaskObservation{{ThreadID: "one", Title: "Old", Status: "idle"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.StaleSkipped != 1 {
		t.Fatalf("result = %#v", result)
	}
	page, _ := st.ListCodexTasks(CodexTaskListFilter{Limit: 10})
	if page.Tasks[0].Title != "New" || page.Tasks[0].Status != "active" || !page.Tasks[0].Unread {
		t.Fatalf("regressed task = %#v", page.Tasks[0])
	}
}

func TestCodexTaskOrderingPreservesSubsecondTime(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	base := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		id string
		at time.Time
	}{{"older", base}, {"newer", base.Add(time.Nanosecond)}} {
		_, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: item.id, HostID: "local", Source: "test", ObservedAt: item.at, Tasks: []CodexTaskObservation{{ThreadID: item.id}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := st.ListCodexTasks(CodexTaskListFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 2 || page.Tasks[0].ThreadID != "newer" {
		t.Fatalf("order = %#v", page.Tasks)
	}
}

func TestCodexTaskClassificationPersistsAndCursorCanClear(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	coordinator := true
	_, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "classified", HostID: "local", Source: "test", ObservedAt: now, Tasks: []CodexTaskObservation{{
		ThreadID: "one", WaitCursor: taskStringPtr("wait-1"), Coordinator: &coordinator,
		Classification: &CodexTaskClassification{Tier: "p2", LastMeaningfulOutcome: "Tests passed", UnresolvedLoop: "PR not opened", SmallestNextAction: "Open PR"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "clear-cursor", HostID: "local", Source: "test", ObservedAt: now.Add(time.Minute), Tasks: []CodexTaskObservation{{ThreadID: "one", WaitCursor: taskStringPtr("")}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "stale-classification", HostID: "local", Source: "test", ObservedAt: now.Add(2 * time.Minute), Tasks: []CodexTaskObservation{{ThreadID: "one", Classification: &CodexTaskClassification{Tier: "P0", LastMeaningfulOutcome: "cached old state", ClassifiedAt: now.Add(-time.Hour)}}}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.ListCodexTasks(CodexTaskListFilter{Tier: "P2", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 {
		t.Fatalf("tasks = %#v", page.Tasks)
	}
	task := page.Tasks[0]
	if task.WaitCursor != "" || !task.Coordinator || task.Tier != "P2" || task.UnresolvedLoop != "PR not opened" || task.LastClassifiedAt == nil || !task.LastClassifiedAt.Equal(now) {
		t.Fatalf("task = %#v", task)
	}
	result, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "new-classification-old-state", HostID: "local", Source: "test", ObservedAt: now.Add(90 * time.Second), Tasks: []CodexTaskObservation{{ThreadID: "one", Status: "stale-state", Classification: &CodexTaskClassification{Tier: "P1", LastMeaningfulOutcome: "Review returned", ClassifiedAt: now.Add(3 * time.Minute)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.StaleSkipped != 1 || result.Updated != 1 {
		t.Fatalf("independent classification result = %#v", result)
	}
	page, err = st.ListCodexTasks(CodexTaskListFilter{Tier: "P1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].Status == "stale-state" || page.Tasks[0].LastMeaningfulOutcome != "Review returned" {
		t.Fatalf("independent classification task = %#v", page.Tasks)
	}
}

func TestCodexTaskEqualTimestampUsesRequestIDTieBreak(t *testing.T) {
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	for _, order := range [][]string{{"a", "z"}, {"z", "a"}} {
		st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
		for _, requestID := range order {
			_, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: requestID, HostID: "local", Source: "test", ObservedAt: now, Tasks: []CodexTaskObservation{{ThreadID: "one", Status: requestID}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		page, _ := st.ListCodexTasks(CodexTaskListFilter{Limit: 10})
		if page.Tasks[0].Status != "z" {
			t.Fatalf("order %v produced %#v", order, page.Tasks[0])
		}
	}
}

func TestCodexTaskConcurrentIngest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := NewJSONStore(path)
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: fmt.Sprintf("r-%d", i), HostID: "local", Source: "test", ObservedAt: now.Add(time.Duration(i) * time.Second), Tasks: []CodexTaskObservation{{ThreadID: fmt.Sprintf("t-%d", i), Status: "idle"}}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	stats, err := st.CodexTaskStats(nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 12 {
		t.Fatalf("total = %d, want 12", stats.Total)
	}
}

func TestCodexTaskIngestReplayRetentionIsBounded(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	err := st.withStateLock(func() error {
		for i := range 4 {
			if _, err := st.tx.Exec(`INSERT INTO codex_task_ingests(request_id,fingerprint,result,created_at) VALUES(?,?,?,?)`, fmt.Sprintf("r-%d", i), "fingerprint", []byte(`{}`), formatDBTime(time.Now().UTC())); err != nil {
				return err
			}
		}
		return pruneCodexTaskIngestReplays(st.tx, 2)
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.withStateLock(func() error { return st.tx.QueryRow(`SELECT COUNT(*) FROM codex_task_ingests`).Scan(&count) }); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("replay rows = %d, want 2", count)
	}
}

func taskStringPtr(value string) *string { return &value }
