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

func TestJSONStoreNotFound(t *testing.T) {
	s := NewJSONStore(filepath.Join(t.TempDir(), "missing.json"))
	_, err := s.GetWorker("missing")
	if !errors.Is(err, ErrWorkerNotFound) {
		t.Fatalf("GetWorker() error = %v, want ErrWorkerNotFound", err)
	}
}

func TestJSONStoreConcurrentMutationsPreserveNonConflictingUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC)

	const pairs = 32
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
