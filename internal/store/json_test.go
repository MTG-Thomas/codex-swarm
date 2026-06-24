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
	if got.ID != worker.ID || got.ThreadID != worker.ThreadID || got.Events[0].Type != "spawned" {
		t.Fatalf("GetWorker() = %#v, want %#v", got, worker)
	}
}

func TestJSONStoreNotFound(t *testing.T) {
	s := NewJSONStore(filepath.Join(t.TempDir(), "missing.json"))
	_, err := s.GetWorker("missing")
	if !errors.Is(err, ErrWorkerNotFound) {
		t.Fatalf("GetWorker() error = %v, want ErrWorkerNotFound", err)
	}
}
