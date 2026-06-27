package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type memoryStore struct {
	workers []store.Worker
	claims  []store.Claim
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

func (m memoryStore) ListClaims() ([]store.Claim, error) {
	return m.claims, nil
}

type fakeIssueProvider struct {
	issue readiness.Issue
	err   error
}

func (p fakeIssueProvider) IssueMetadata(ctx context.Context, issue string) (readiness.Issue, error) {
	if p.err != nil {
		return readiness.Issue{}, p.err
	}
	got := p.issue
	got.Ref = issue
	return got, nil
}

func TestStatusString(t *testing.T) {
	status := Status{Daemon: "running", Version: "0.1.0", StatePath: "state.json", WorkerCount: 2, ClaimCount: 3, ConflictCount: 1}
	got := status.String()
	want := "daemon=running version=0.1.0 workers=2 claims=3 conflicts=1 state=state.json"
	if got != want {
		t.Fatalf("Status.String() = %q, want %q", got, want)
	}
}

func TestServerStatus(t *testing.T) {
	now := time.Now().UTC()
	server := NewServer("state.json", memoryStore{
		workers: []store.Worker{{
			ID:        "w-1",
			Status:    store.WorkerIdle,
			Engine:    "mock",
			ThreadID:  "thread-1",
			CreatedAt: time.Date(2026, 6, 25, 1, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 6, 25, 1, 0, 0, 0, time.UTC),
		}},
		claims: []store.Claim{{
			ID:        "c-1",
			Repo:      "/repo",
			Scope:     "internal",
			Status:    store.ClaimActive,
			ExpiresAt: now.Add(time.Hour),
		}, {
			ID:        "c-2",
			Repo:      "/repo",
			Scope:     "internal/daemon",
			Status:    store.ClaimActive,
			ExpiresAt: now.Add(time.Hour),
		}},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var status Status
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Daemon != "running" || status.Version == "" || status.StatePath != "state.json" {
		t.Fatalf("status identity = %#v", status)
	}
	if status.WorkerCount != 1 || status.ClaimCount != 2 || status.ConflictCount != 1 {
		t.Fatalf("status counts = workers:%d claims:%d conflicts:%d", status.WorkerCount, status.ClaimCount, status.ConflictCount)
	}
}

func TestServerLegacyStatusShape(t *testing.T) {
	server := NewServer("state.json", memoryStore{workers: []store.Worker{{
		ID:       "w-legacy",
		Status:   store.WorkerIdle,
		Engine:   "mock",
		ThreadID: "thread-legacy",
	}}})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/status", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var status LegacyStatus
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode legacy status: %v", err)
	}
	if status.Daemon != "running" || status.StatePath != "state.json" || len(status.Workers) != 1 || status.Workers[0].ID != "w-legacy" {
		t.Fatalf("legacy status = %#v", status)
	}
}

func TestServerWorkers(t *testing.T) {
	staleLifecycle := lifecycle.NewWorkerLifecycle()
	staleLifecycle.Runtime.State = lifecycle.RuntimeDead
	staleLifecycle.Runtime.Reason = lifecycle.ReasonRuntimeLost
	server := NewServer("state.json", memoryStore{workers: []store.Worker{{
		ID:        "w-1",
		Status:    store.WorkerRunning,
		Lifecycle: &staleLifecycle,
		Engine:    "mock",
		Issue:     "MTG-Thomas/codex-swarm#42",
		Worktree:  "C:/repo/.codex-swarm/worktrees/w-1",
		ThreadID:  "thread-1",
		CreatedAt: time.Date(2026, 6, 25, 1, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 25, 1, 0, 0, 0, time.UTC),
	}}})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/workers", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response WorkersResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode workers: %v", err)
	}
	if len(response.Workers) != 1 {
		t.Fatalf("workers = %#v", response.Workers)
	}
	worker := response.Workers[0]
	if worker.ID != "w-1" || worker.Status != "stale" || worker.Issue != "MTG-Thomas/codex-swarm#42" || worker.Worktree != "C:/repo/.codex-swarm/worktrees/w-1" || worker.ThreadID != "thread-1" {
		t.Fatalf("worker response = %#v", worker)
	}
}

func TestServerClaims(t *testing.T) {
	now := time.Now().UTC()
	server := NewServer("state.json", memoryStore{claims: []store.Claim{{
		ID:        "c-parent",
		WorkerID:  "w-1",
		Repo:      "/repo",
		Scope:     "internal",
		Status:    store.ClaimActive,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now,
	}, {
		ID:        "c-child",
		WorkerID:  "w-2",
		Repo:      "/repo",
		Scope:     "internal/daemon",
		Status:    store.ClaimActive,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now,
	}, {
		ID:        "c-released",
		WorkerID:  "w-3",
		Repo:      "/repo",
		Scope:     "internal/daemon",
		Status:    store.ClaimReleased,
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now,
	}}})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/claims", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response ClaimsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if len(response.Claims) != 3 {
		t.Fatalf("claims = %#v", response.Claims)
	}
	if len(response.Conflicts) != 1 {
		t.Fatalf("conflicts = %#v, want one unique overlapping claim pair", response.Conflicts)
	}
	if response.Conflicts[0].ClaimID == "" || response.Conflicts[0].ConflictID == "" {
		t.Fatalf("conflict identifiers = %#v", response.Conflicts[0])
	}
}

func TestServerReadiness(t *testing.T) {
	repo := t.TempDir()
	writeDaemonRepoHints(t, repo)
	server := NewServerWithIssueProvider("state.json", memoryStore{claims: []store.Claim{{
		ID:     "c-released",
		Issue:  "MTG-Thomas/codex-swarm#27",
		Status: store.ClaimReleased,
	}}}, fakeIssueProvider{issue: readiness.Issue{
		Title: "Expose issue readiness through daemon",
		Body:  "Acceptance criteria",
	}})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readiness?issue=MTG-Thomas/codex-swarm%2327&repo="+repo, nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var report readiness.Report
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if !report.Ready || report.Issue.Ref != "MTG-Thomas/codex-swarm#27" || report.Repo == "" || len(report.Gates) != 1 {
		t.Fatalf("readiness report = %#v", report)
	}
}

func TestClientReadiness(t *testing.T) {
	repo := t.TempDir()
	writeDaemonRepoHints(t, repo)
	server := httptest.NewServer(NewServerWithIssueProvider("state.json", memoryStore{}, fakeIssueProvider{issue: readiness.Issue{
		Title: "Ready issue",
		Body:  "Body",
	}}).Handler())
	defer server.Close()

	report, err := (Client{BaseURL: server.URL}).Readiness(context.Background(), "MTG-Thomas/codex-swarm#27", repo)
	if err != nil {
		t.Fatalf("Readiness() error = %v", err)
	}
	if !report.Ready || report.Issue.Ref != "MTG-Thomas/codex-swarm#27" {
		t.Fatalf("readiness report = %#v", report)
	}
}

func TestClientStatus(t *testing.T) {
	server := httptest.NewServer(NewServer("state.json", memoryStore{workers: []store.Worker{{ID: "w-1"}}}).Handler())
	defer server.Close()

	status, err := (Client{BaseURL: server.URL}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.StatePath != "state.json" || status.WorkerCount != 1 {
		t.Fatalf("status = %#v", status)
	}
}

func TestClientFallsBackToLegacyStatus(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, LegacyStatus{
			Daemon:    "running",
			StatePath: "legacy-state.json",
			Workers:   []store.Worker{{ID: "w-legacy", Status: store.WorkerIdle, ThreadID: "thread-legacy"}},
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := Client{BaseURL: server.URL}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Version != "legacy" || status.StatePath != "legacy-state.json" || status.WorkerCount != 1 {
		t.Fatalf("fallback status = %#v", status)
	}
	workers, err := client.Workers(context.Background())
	if err != nil {
		t.Fatalf("Workers() error = %v", err)
	}
	if len(workers.Workers) != 1 || workers.Workers[0].ID != "w-legacy" {
		t.Fatalf("fallback workers = %#v", workers)
	}
}

func TestClientStatusDoesNotFallbackOnServerError(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	})
	handler.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, LegacyStatus{Daemon: "running", StatePath: "legacy-state.json"})
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	_, err := (Client{BaseURL: server.URL}).Status(context.Background())
	if err == nil {
		t.Fatal("Status() error = nil, want server error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("Status() error = %v, want original /status failure", err)
	}
}

func TestReadOnlyEndpointsRejectPost(t *testing.T) {
	server := NewServer("state.json", memoryStore{})
	for _, path := range []string{"/healthz", "/status", "/workers", "/claims", "/readiness", "/v1/status"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(""))
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status code = %d, want %d", path, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}

func writeDaemonRepoHints(t *testing.T, repo string) {
	t.Helper()
	body := `{
  "quality_gates": [
    {
      "id": "test",
      "command": "go test ./...",
      "scope": "repo"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(repo, "codex-swarm.hints.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write repo hints: %v", err)
	}
}
