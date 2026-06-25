package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type memoryStore struct {
	workers []store.Worker
}

func (m memoryStore) SaveWorker(worker store.Worker) error {
	return nil
}

func (m memoryStore) GetWorker(id string) (store.Worker, error) {
	return store.Worker{}, store.ErrWorkerNotFound
}

func (m memoryStore) ListWorkers() ([]store.Worker, error) {
	return m.workers, nil
}

func TestStatusString(t *testing.T) {
	status := Status{Daemon: "running", StatePath: "state.json", Workers: []store.Worker{{ID: "w-1"}, {ID: "w-2"}}}
	got := status.String()
	want := "daemon=running workers=2 state=state.json"
	if got != want {
		t.Fatalf("Status.String() = %q, want %q", got, want)
	}
}

func TestServerStatus(t *testing.T) {
	server := NewServer("state.json", memoryStore{workers: []store.Worker{{
		ID:        "w-1",
		Status:    store.WorkerIdle,
		Engine:    "mock",
		ThreadID:  "thread-1",
		CreatedAt: time.Date(2026, 6, 25, 1, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 25, 1, 0, 0, 0, time.UTC),
	}}})

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var status Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Daemon != "running" || len(status.Workers) != 1 || status.Workers[0].ID != "w-1" {
		t.Fatalf("status = %#v", status)
	}
}

func TestClientStatus(t *testing.T) {
	server := httptest.NewServer(NewServer("state.json", memoryStore{workers: []store.Worker{{ID: "w-1"}}}).Handler())
	defer server.Close()

	status, err := (Client{BaseURL: server.URL}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.StatePath != "state.json" || len(status.Workers) != 1 {
		t.Fatalf("status = %#v", status)
	}
}

func TestHealthRejectsPost(t *testing.T) {
	server := NewServer("state.json", memoryStore{})
	req := httptest.NewRequest(http.MethodPost, "/healthz", strings.NewReader(""))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
