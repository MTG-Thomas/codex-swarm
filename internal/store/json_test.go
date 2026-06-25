package store

import (
	"errors"
	"path/filepath"
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
}

func TestJSONStoreNotFound(t *testing.T) {
	s := NewJSONStore(filepath.Join(t.TempDir(), "missing.json"))
	_, err := s.GetWorker("missing")
	if !errors.Is(err, ErrWorkerNotFound) {
		t.Fatalf("GetWorker() error = %v, want ErrWorkerNotFound", err)
	}
}
