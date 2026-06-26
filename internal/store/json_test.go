package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
)

func TestJSONStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewJSONStore(path)
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	worker := Worker{
		ID:          "w-test",
		Issue:       "MTG-Thomas/codex-swarm#42",
		ProjectRoot: "/repo",
		ThreadID:    "thread-test",
		Status:      WorkerIdle,
		Prompt:      "inspect repo",
		CreatedAt:   now,
		UpdatedAt:   now,
		Events:      []Event{{At: now, Type: "spawned", Message: "worker created"}},
	}
	if err := s.SaveWorker(worker); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	got, err := s.GetWorker("w-test")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if got.ID != worker.ID || got.ThreadID != worker.ThreadID || got.Issue != worker.Issue || got.Events[0].Type != "spawned" {
		t.Fatalf("GetWorker() = %#v, want %#v", got, worker)
	}

	schedule := Schedule{
		ID:        "s-test",
		Repo:      "/repo",
		Prompt:    "weekly check",
		Cron:      "0 8 * * 1",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.SaveSchedule(schedule); err != nil {
		t.Fatalf("SaveSchedule() error = %v", err)
	}
	schedules, err := s.ListSchedules()
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	if len(schedules) != 1 || schedules[0].ID != schedule.ID || schedules[0].Cron != schedule.Cron {
		t.Fatalf("ListSchedules() = %#v", schedules)
	}

	claim := Claim{
		ID:        "c-test",
		WorkerID:  "w-test",
		Repo:      "/repo",
		Scope:     "internal/store",
		Issue:     "MTG-Thomas/codex-swarm#42",
		Status:    ClaimActive,
		Note:      "working",
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.SaveClaim(claim); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	gotClaim, err := s.GetClaim("c-test")
	if err != nil {
		t.Fatalf("GetClaim() error = %v", err)
	}
	if gotClaim.ID != claim.ID || gotClaim.Scope != claim.Scope || gotClaim.Issue != claim.Issue {
		t.Fatalf("GetClaim() = %#v, want %#v", gotClaim, claim)
	}

	agent := Agent{
		ID:        "a-test",
		Name:      "test-agent",
		Role:      "tester",
		Current:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.SaveAgent(agent); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	current, err := s.CurrentAgent()
	if err != nil {
		t.Fatalf("CurrentAgent() error = %v", err)
	}
	if current.ID != agent.ID || current.Name != agent.Name {
		t.Fatalf("CurrentAgent() = %#v", current)
	}
}

func TestJSONStoreImportClaimsSkipsNewerLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewJSONStore(path)
	localUpdated := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	remoteUpdated := localUpdated.Add(-time.Hour)
	if err := s.SaveClaim(Claim{
		ID:        "c-1",
		Status:    ClaimActive,
		Note:      "newer local",
		UpdatedAt: localUpdated,
	}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}

	imported, skipped, conflicted, err := s.ImportClaims([]Claim{{
		ID:        "c-1",
		Status:    ClaimReleased,
		Note:      "older remote",
		UpdatedAt: remoteUpdated,
	}}, false)
	if err != nil {
		t.Fatalf("ImportClaims() error = %v", err)
	}
	if imported != 0 || skipped != 1 || conflicted != 1 {
		t.Fatalf("ImportClaims() = imported:%d skipped:%d conflicted:%d, want 0/1/1", imported, skipped, conflicted)
	}
	got, err := s.GetClaim("c-1")
	if err != nil {
		t.Fatalf("GetClaim() error = %v", err)
	}
	if got.Note != "newer local" || got.Status != ClaimActive {
		t.Fatalf("claim overwritten = %#v", got)
	}
}

func TestJSONStoreNotFound(t *testing.T) {
	s := NewJSONStore(filepath.Join(t.TempDir(), "missing.json"))
	_, err := s.GetWorker("missing")
	if !errors.Is(err, ErrWorkerNotFound) {
		t.Fatalf("GetWorker() error = %v, want ErrWorkerNotFound", err)
	}
}

func TestJSONStoreSynthesizesLifecycleForOldWorkerState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	oldState := `{
  "workers": [
    {
      "id": "w-old",
      "project_root": "/repo",
      "worktree": "",
      "branch": "",
      "thread_id": "thread-old",
      "engine": "mock",
      "status": "running",
      "prompt": "old worker",
      "created_at": "2026-06-26T01:00:00Z",
      "updated_at": "2026-06-26T01:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(oldState), 0o600); err != nil {
		t.Fatalf("WriteFile(old state) error = %v", err)
	}

	worker, err := NewJSONStore(path).GetWorker("w-old")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if worker.Lifecycle == nil {
		t.Fatal("Lifecycle = nil, want synthesized lifecycle")
	}
	if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayWorking {
		t.Fatalf("Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayWorking)
	}
	if worker.Status != WorkerRunning {
		t.Fatalf("Status = %q, want legacy status preserved as %q", worker.Status, WorkerRunning)
	}
}

func TestJSONStoreSynthesizesIdleLifecycleForOldWorkerState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	oldState := `{
  "workers": [
    {
      "id": "w-idle",
      "project_root": "/repo",
      "worktree": "",
      "branch": "",
      "thread_id": "thread-idle",
      "engine": "mock",
      "status": "idle",
      "prompt": "old idle worker",
      "created_at": "2026-06-26T01:00:00Z",
      "updated_at": "2026-06-26T01:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(oldState), 0o600); err != nil {
		t.Fatalf("WriteFile(old state) error = %v", err)
	}

	worker, err := NewJSONStore(path).GetWorker("w-idle")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if worker.Lifecycle == nil {
		t.Fatal("Lifecycle = nil, want synthesized lifecycle")
	}
	if worker.Lifecycle.Session.State != lifecycle.SessionIdle {
		t.Fatalf("Lifecycle.Session.State = %q, want %q", worker.Lifecycle.Session.State, lifecycle.SessionIdle)
	}
	if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayIdle {
		t.Fatalf("Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayIdle)
	}
	if worker.Status != WorkerIdle {
		t.Fatalf("Status = %q, want legacy status preserved as %q", worker.Status, WorkerIdle)
	}
}

func TestJSONStoreSavesWorkerLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 26, 1, 30, 0, 0, time.UTC)
	if err := NewJSONStore(path).SaveWorker(Worker{
		ID:          "w-new",
		ProjectRoot: "/repo",
		ThreadID:    "thread-new",
		Engine:      "mock",
		Status:      WorkerRunning,
		Prompt:      "new worker",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal(state) error = %v\n%s", err, string(data))
	}
	if len(state.Workers) != 1 {
		t.Fatalf("workers count = %d, want 1", len(state.Workers))
	}
	if state.Workers[0].Lifecycle == nil {
		t.Fatal("stored Lifecycle = nil, want lifecycle persisted")
	}
	if got := state.Workers[0].Lifecycle.DeriveStatus(); got != lifecycle.DisplayWorking {
		t.Fatalf("stored Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayWorking)
	}
}

func TestJSONStoreSynthesizesLifecycleFromStatusOnSaveWhenMissingLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 26, 1, 45, 0, 0, time.UTC)
	tests := []struct {
		name        string
		status      WorkerStatus
		wantDisplay lifecycle.DisplayStatus
		wantSession lifecycle.SessionState
		wantReason  lifecycle.Reason
	}{
		{
			name:        "done",
			status:      WorkerDone,
			wantDisplay: lifecycle.DisplayDone,
			wantSession: lifecycle.SessionDone,
			wantReason:  lifecycle.ReasonCompleted,
		},
		{
			name:        "failed",
			status:      WorkerFailed,
			wantDisplay: lifecycle.DisplayFailed,
			wantSession: lifecycle.SessionFailed,
			wantReason:  lifecycle.ReasonFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := NewJSONStore(path).SaveWorker(Worker{
				ID:          "w-reconcile",
				ProjectRoot: "/repo",
				ThreadID:    "thread-reconcile",
				Engine:      "mock",
				Status:      tt.status,
				Prompt:      "status changed by existing caller",
				CreatedAt:   now,
				UpdatedAt:   now,
			}); err != nil {
				t.Fatalf("SaveWorker() error = %v", err)
			}

			worker, err := NewJSONStore(path).GetWorker("w-reconcile")
			if err != nil {
				t.Fatalf("GetWorker() error = %v", err)
			}
			if worker.Lifecycle == nil {
				t.Fatal("Lifecycle = nil, want reconciled lifecycle")
			}
			if got := worker.Lifecycle.DeriveStatus(); got != tt.wantDisplay {
				t.Fatalf("Lifecycle.DeriveStatus() = %q, want %q", got, tt.wantDisplay)
			}
			if worker.Lifecycle.Session.State != tt.wantSession {
				t.Fatalf("Lifecycle.Session.State = %q, want %q", worker.Lifecycle.Session.State, tt.wantSession)
			}
			if worker.Lifecycle.Session.Reason != tt.wantReason {
				t.Fatalf("Lifecycle.Session.Reason = %q, want %q", worker.Lifecycle.Session.Reason, tt.wantReason)
			}
		})
	}
}

func TestJSONStorePersistsLifecycleTransitionHelper(t *testing.T) {
	now := time.Date(2026, 6, 26, 2, 20, 0, 0, time.UTC)
	tests := []struct {
		name        string
		status      WorkerStatus
		wantDisplay lifecycle.DisplayStatus
		wantSession lifecycle.SessionState
	}{
		{
			name:        "done",
			status:      WorkerDone,
			wantDisplay: lifecycle.DisplayDone,
			wantSession: lifecycle.SessionDone,
		},
		{
			name:        "failed",
			status:      WorkerFailed,
			wantDisplay: lifecycle.DisplayFailed,
			wantSession: lifecycle.SessionFailed,
		},
		{
			name:        "idle",
			status:      WorkerIdle,
			wantDisplay: lifecycle.DisplayIdle,
			wantSession: lifecycle.SessionIdle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := NewJSONStore(path).SaveWorker(Worker{
				ID:          "w-transition",
				ProjectRoot: "/repo",
				ThreadID:    "thread-transition",
				Engine:      "mock",
				Status:      WorkerIdle,
				Prompt:      "existing worker",
				CreatedAt:   now,
				UpdatedAt:   now,
			}); err != nil {
				t.Fatalf("SaveWorker(initial) error = %v", err)
			}

			worker, err := NewJSONStore(path).GetWorker("w-transition")
			if err != nil {
				t.Fatalf("GetWorker() error = %v", err)
			}
			worker.ApplyStatus(tt.status)
			worker.UpdatedAt = now.Add(time.Minute)
			if err := NewJSONStore(path).SaveWorker(worker); err != nil {
				t.Fatalf("SaveWorker(transitioned) error = %v", err)
			}

			got, err := NewJSONStore(path).GetWorker("w-transition")
			if err != nil {
				t.Fatalf("GetWorker(after) error = %v", err)
			}
			if got.Status != tt.status {
				t.Fatalf("Status = %q, want %q", got.Status, tt.status)
			}
			if got.Lifecycle == nil {
				t.Fatal("Lifecycle = nil, want transitioned lifecycle")
			}
			if display := got.Lifecycle.DeriveStatus(); display != tt.wantDisplay {
				t.Fatalf("Lifecycle.DeriveStatus() = %q, want %q", display, tt.wantDisplay)
			}
			if got.Lifecycle.Session.State != tt.wantSession {
				t.Fatalf("Lifecycle.Session.State = %q, want %q", got.Lifecycle.Session.State, tt.wantSession)
			}
		})
	}
}

func TestJSONStorePreservesExplicitStaleLifecycleOnSave(t *testing.T) {
	now := time.Date(2026, 6, 26, 2, 5, 0, 0, time.UTC)
	for _, status := range []WorkerStatus{WorkerRunning, WorkerIdle, WorkerDone, WorkerFailed} {
		t.Run(string(status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			staleLifecycle := lifecycle.NewWorkerLifecycle()
			staleLifecycle.Runtime.State = lifecycle.RuntimeDead
			staleLifecycle.Runtime.Reason = lifecycle.ReasonRuntimeLost

			if err := NewJSONStore(path).SaveWorker(Worker{
				ID:          "w-stale",
				ProjectRoot: "/repo",
				ThreadID:    "thread-stale",
				Engine:      "mock",
				Status:      status,
				Lifecycle:   &staleLifecycle,
				Prompt:      "stale worker",
				CreatedAt:   now,
				UpdatedAt:   now,
			}); err != nil {
				t.Fatalf("SaveWorker() error = %v", err)
			}

			worker, err := NewJSONStore(path).GetWorker("w-stale")
			if err != nil {
				t.Fatalf("GetWorker() error = %v", err)
			}
			if worker.Lifecycle == nil {
				t.Fatal("Lifecycle = nil, want explicit lifecycle preserved")
			}
			if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayStale {
				t.Fatalf("Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayStale)
			}
			if worker.Lifecycle.Runtime.State != lifecycle.RuntimeDead || worker.Lifecycle.Runtime.Reason != lifecycle.ReasonRuntimeLost {
				t.Fatalf("Runtime = %#v, want dead runtime_lost", worker.Lifecycle.Runtime)
			}
			if worker.Status != WorkerRunning {
				t.Fatalf("Status = %q, want stale legacy fallback %q", worker.Status, WorkerRunning)
			}
		})
	}
}

func TestJSONStorePreservesExplicitTerminalLifecycleOnSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 26, 2, 10, 0, 0, time.UTC)
	completedAt := now.Add(-time.Minute)
	terminalLifecycle := lifecycle.Lifecycle{
		Version: lifecycle.CurrentVersion,
		Session: lifecycle.SessionLifecycle{
			State:       lifecycle.SessionDone,
			CompletedAt: &completedAt,
		},
		Runtime: lifecycle.RuntimeLifecycle{
			State:  lifecycle.RuntimeDead,
			Reason: lifecycle.ReasonRuntimeLost,
		},
	}

	if err := NewJSONStore(path).SaveWorker(Worker{
		ID:          "w-terminal",
		ProjectRoot: "/repo",
		ThreadID:    "thread-terminal",
		Engine:      "mock",
		Status:      WorkerRunning,
		Lifecycle:   &terminalLifecycle,
		Prompt:      "terminal worker",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	worker, err := NewJSONStore(path).GetWorker("w-terminal")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if worker.Lifecycle == nil {
		t.Fatal("Lifecycle = nil, want explicit lifecycle preserved")
	}
	if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayDone {
		t.Fatalf("Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayDone)
	}
	if worker.Lifecycle.Session.CompletedAt == nil || !worker.Lifecycle.Session.CompletedAt.Equal(completedAt) {
		t.Fatalf("CompletedAt = %v, want %v", worker.Lifecycle.Session.CompletedAt, completedAt)
	}
	if worker.Status != WorkerDone {
		t.Fatalf("Status = %q, want derived legacy status %q", worker.Status, WorkerDone)
	}
}

func TestJSONStoreDerivesStatusFromExistingLifecycleOnRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := `{
  "workers": [
    {
      "id": "w-done",
      "project_root": "/repo",
      "worktree": "",
      "branch": "",
      "thread_id": "thread-done",
      "engine": "mock",
      "status": "running",
      "lifecycle": {
        "version": 1,
        "session": {"state": "done"},
        "runtime": {"state": "dead", "reason": "runtime_lost"}
      },
      "prompt": "canonical terminal worker",
      "created_at": "2026-06-26T02:00:00Z",
      "updated_at": "2026-06-26T02:00:00Z"
    },
    {
      "id": "w-stale",
      "project_root": "/repo",
      "worktree": "",
      "branch": "",
      "thread_id": "thread-stale",
      "engine": "mock",
      "status": "idle",
      "lifecycle": {
        "version": 1,
        "session": {"state": "working", "reason": "task_in_progress"},
        "runtime": {"state": "dead", "reason": "runtime_lost"}
      },
      "prompt": "canonical stale worker",
      "created_at": "2026-06-26T02:00:00Z",
      "updated_at": "2026-06-26T02:01:00Z"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatalf("WriteFile(state) error = %v", err)
	}

	done, err := NewJSONStore(path).GetWorker("w-done")
	if err != nil {
		t.Fatalf("GetWorker(done) error = %v", err)
	}
	if done.Status != WorkerDone {
		t.Fatalf("done Status = %q, want %q", done.Status, WorkerDone)
	}
	if got := done.Lifecycle.DeriveStatus(); got != lifecycle.DisplayDone {
		t.Fatalf("done Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayDone)
	}

	stale, err := NewJSONStore(path).GetWorker("w-stale")
	if err != nil {
		t.Fatalf("GetWorker(stale) error = %v", err)
	}
	if stale.Status != WorkerRunning {
		t.Fatalf("stale Status = %q, want least-misleading fallback %q", stale.Status, WorkerRunning)
	}
	if got := stale.Lifecycle.DeriveStatus(); got != lifecycle.DisplayStale {
		t.Fatalf("stale Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayStale)
	}
}

func TestJSONStoreConcurrentMutationsPreserveNonConflictingUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC)

	const pairs = 12
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, pairs*2)

	for i := 0; i < pairs; i++ {
		i := i
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			worker := Worker{
				ID:          fmt.Sprintf("w-%02d", i),
				ProjectRoot: "/repo",
				ThreadID:    fmt.Sprintf("thread-%02d", i),
				Status:      WorkerIdle,
				Prompt:      "concurrent worker update",
				CreatedAt:   now.Add(time.Duration(i) * time.Second),
				UpdatedAt:   now.Add(time.Duration(i) * time.Second),
			}
			if err := NewJSONStore(path).SaveWorker(worker); err != nil {
				errs <- fmt.Errorf("SaveWorker(%s): %w", worker.ID, err)
			}
		}()

		go func() {
			defer wg.Done()
			<-start
			claim := Claim{
				ID:        fmt.Sprintf("c-%02d", i),
				WorkerID:  fmt.Sprintf("w-%02d", i),
				Repo:      "/repo",
				Scope:     fmt.Sprintf("scope-%02d", i),
				Status:    ClaimActive,
				Note:      "concurrent claim update",
				ExpiresAt: now.Add(time.Hour),
				CreatedAt: now.Add(time.Duration(i) * time.Second),
				UpdatedAt: now.Add(time.Duration(i) * time.Second),
			}
			if err := NewJSONStore(path).SaveClaim(claim); err != nil {
				errs <- fmt.Errorf("SaveClaim(%s): %w", claim.ID, err)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("final state is not parseable JSON: %v\n%s", err, string(data))
	}
	if len(state.Workers) != pairs {
		t.Fatalf("final workers count = %d, want %d; state = %s", len(state.Workers), pairs, string(data))
	}
	if len(state.Claims) != pairs {
		t.Fatalf("final claims count = %d, want %d; state = %s", len(state.Claims), pairs, string(data))
	}

	workers := map[string]bool{}
	for _, worker := range state.Workers {
		workers[worker.ID] = true
	}
	claims := map[string]bool{}
	for _, claim := range state.Claims {
		claims[claim.ID] = true
	}
	for i := 0; i < pairs; i++ {
		if id := fmt.Sprintf("w-%02d", i); !workers[id] {
			t.Fatalf("missing worker %s in final state", id)
		}
		if id := fmt.Sprintf("c-%02d", i); !claims[id] {
			t.Fatalf("missing claim %s in final state", id)
		}
	}
}

func TestJSONStoreUpdateWorkerSerializesReadModifyWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 26, 14, 0, 0, 0, time.UTC)
	s := NewJSONStore(path)
	if err := s.SaveWorker(Worker{
		ID:          "w-update",
		ProjectRoot: "/repo",
		ThreadID:    "thread-update",
		Status:      WorkerIdle,
		Prompt:      "atomic update",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	const updates = 16
	start := make(chan struct{})
	errs := make(chan error, updates)
	var wg sync.WaitGroup
	for i := 0; i < updates; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := NewJSONStore(path).UpdateWorker("w-update", func(worker *Worker) error {
				worker.Events = append(worker.Events, Event{
					At:      now.Add(time.Duration(i) * time.Second),
					Type:    fmt.Sprintf("event-%02d", i),
					Message: "atomic worker mutation",
				})
				worker.UpdatedAt = now.Add(time.Duration(i) * time.Second)
				return nil
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	worker, err := s.GetWorker("w-update")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if len(worker.Events) != updates {
		t.Fatalf("events count = %d, want %d", len(worker.Events), updates)
	}
}

func TestJSONStoreLoadIgnoresStaleTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	s := NewJSONStore(path)

	if err := s.SaveWorker(Worker{
		ID:          "w-live",
		ProjectRoot: "/repo",
		ThreadID:    "thread-live",
		Status:      WorkerIdle,
		Prompt:      "live state",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
	if err := os.WriteFile(path+".tmp.99999", []byte(`{"workers":[`), 0o600); err != nil {
		t.Fatalf("WriteFile(stale temp) error = %v", err)
	}

	workers, err := NewJSONStore(path).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	if len(workers) != 1 || workers[0].ID != "w-live" {
		t.Fatalf("ListWorkers() = %#v, want only live worker", workers)
	}
}

func TestJSONStoreRecoversStaleLockFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	old := time.Now().Add(-24 * time.Hour).UTC()
	lock := fmt.Sprintf("pid=-1\nacquired_at=%s\n", old.Format(time.RFC3339Nano))
	if err := os.WriteFile(path+".lock", []byte(lock), 0o600); err != nil {
		t.Fatalf("WriteFile(stale lock) error = %v", err)
	}

	now := time.Date(2026, 6, 26, 11, 0, 0, 0, time.UTC)
	if err := NewJSONStore(path).SaveWorker(Worker{
		ID:          "w-after-stale-lock",
		ProjectRoot: "/repo",
		ThreadID:    "thread-after-stale-lock",
		Status:      WorkerIdle,
		Prompt:      "recover stale lock",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	workers, err := NewJSONStore(path).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	if len(workers) != 1 || workers[0].ID != "w-after-stale-lock" {
		t.Fatalf("ListWorkers() = %#v, want recovered worker", workers)
	}
}

func TestJSONStoreMultiContenderMalformedStaleLockRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, []byte("partial lock metadata"), 0o600); err != nil {
		t.Fatalf("WriteFile(malformed lock) error = %v", err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("Chtimes(malformed lock) error = %v", err)
	}

	now := time.Date(2026, 6, 26, 13, 0, 0, 0, time.UTC)
	const workers = 16
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			worker := Worker{
				ID:          fmt.Sprintf("w-malformed-lock-%02d", i),
				ProjectRoot: "/repo",
				ThreadID:    fmt.Sprintf("thread-malformed-lock-%02d", i),
				Status:      WorkerIdle,
				Prompt:      "recover malformed stale lock",
				CreatedAt:   now.Add(time.Duration(i) * time.Second),
				UpdatedAt:   now.Add(time.Duration(i) * time.Second),
			}
			if err := NewJSONStore(path).SaveWorker(worker); err != nil {
				errs <- fmt.Errorf("SaveWorker(%s): %w", worker.ID, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("final state is not parseable JSON: %v\n%s", err, string(data))
	}
	if len(state.Workers) != workers {
		t.Fatalf("final workers count = %d, want %d; state = %s", len(state.Workers), workers, string(data))
	}
	seen := map[string]bool{}
	for _, worker := range state.Workers {
		seen[worker.ID] = true
	}
	for i := 0; i < workers; i++ {
		id := fmt.Sprintf("w-malformed-lock-%02d", i)
		if !seen[id] {
			t.Fatalf("missing worker %s in final state", id)
		}
	}
}

func TestJSONStoreReplacesExistingTargetWithParseableState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	s := NewJSONStore(path)

	if err := s.SaveWorker(Worker{
		ID:          "w-first",
		ProjectRoot: "/repo",
		ThreadID:    "thread-first",
		Status:      WorkerIdle,
		Prompt:      "first",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker(first) error = %v", err)
	}
	if err := s.SaveClaim(Claim{
		ID:        "c-second",
		WorkerID:  "w-first",
		Repo:      "/repo",
		Scope:     "scope-second",
		Status:    ClaimActive,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveClaim(second) error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("final state is not parseable JSON: %v\n%s", err, string(data))
	}
	if len(state.Workers) != 1 || state.Workers[0].ID != "w-first" {
		t.Fatalf("workers = %#v, want first worker preserved", state.Workers)
	}
	if len(state.Claims) != 1 || state.Claims[0].ID != "c-second" {
		t.Fatalf("claims = %#v, want second claim installed", state.Claims)
	}
}
