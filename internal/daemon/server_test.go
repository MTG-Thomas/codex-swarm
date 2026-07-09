package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	events  []store.Event
}

func (m *memoryStore) SaveWorkers(workers ...store.Worker) error {
	m.workers = append(m.workers, workers...)
	return nil
}

func (m *memoryStore) GetWorker(id string) (store.Worker, error) {
	return store.Worker{}, store.ErrWorkerNotFound
}

func (m *memoryStore) ListWorkers() ([]store.Worker, error) {
	return m.workers, nil
}

func (m *memoryStore) ListClaims() ([]store.Claim, error) {
	return m.claims, nil
}

func (m *memoryStore) ListEvents() ([]store.Event, error) {
	return m.events, nil
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
	server := NewServer("state.json", &memoryStore{
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
	server := NewServer("state.json", &memoryStore{workers: []store.Worker{{
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

func TestProtocolLegacyWorkersPreserveJSONShape(t *testing.T) {
	now := time.Date(2026, 7, 9, 19, 0, 0, 0, time.UTC)
	lc := lifecycle.NewWorkerLifecycle()
	worker := store.Worker{
		ID: "w-legacy", ParentID: "w-parent", Role: "tester", Issue: "owner/repo#58",
		ValidationOf: "w-implementer", ValidationStatus: "passed", ProjectRoot: "/repo",
		Worktree: "/repo/worktree", Branch: "codex/test", ThreadID: "thread-1", TurnID: "turn-1",
		Engine: "appserver", Status: store.WorkerRunning, Lifecycle: &lc, Prompt: "verify",
		LastMessage: "working", Report: "done", CreatedAt: now, UpdatedAt: now,
		PullRequests: []store.PullRequestState{{URL: "https://example.test/pr/58", State: "OPEN", UpdatedAt: now}},
		Events:       []store.Event{{At: now, Type: "message.sent", Message: "hello", WorkerID: "w-legacy"}},
	}

	wantJSON, err := json.Marshal(worker)
	if err != nil {
		t.Fatalf("marshal store worker: %v", err)
	}
	gotJSON, err := json.Marshal(protocolLegacyWorkers([]store.Worker{worker})[0])
	if err != nil {
		t.Fatalf("marshal protocol worker: %v", err)
	}
	var want, got any
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatalf("decode store worker: %v", err)
	}
	if err := json.Unmarshal(gotJSON, &got); err != nil {
		t.Fatalf("decode protocol worker: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol worker JSON = %s, want %s", gotJSON, wantJSON)
	}
}

func TestServerWorkers(t *testing.T) {
	staleLifecycle := lifecycle.NewWorkerLifecycle()
	staleLifecycle.Runtime.State = lifecycle.RuntimeDead
	staleLifecycle.Runtime.Reason = lifecycle.ReasonRuntimeLost
	server := NewServer("state.json", &memoryStore{workers: []store.Worker{{
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
	server := NewServer("state.json", &memoryStore{claims: []store.Claim{{
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
	server := NewServerWithIssueProvider("state.json", &memoryStore{claims: []store.Claim{{
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
	server := httptest.NewServer(NewServerWithIssueProvider("state.json", &memoryStore{}, fakeIssueProvider{issue: readiness.Issue{
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

func TestServerDispatchCreatesAndReplaysWorkers(t *testing.T) {
	repo := t.TempDir()
	writeDaemonRepoHints(t, repo)
	st := &memoryStore{}
	server := NewServerWithIssueProvider("state.json", st, fakeIssueProvider{issue: readiness.Issue{
		Title: "Dispatch issue",
		Body:  "Acceptance criteria",
	}})
	body := `{"request_id":"r-dispatch","issue":"MTG-Thomas/codex-swarm#31","repo":` + quoteJSON(repo) + `,"prompt":"implement issue #31","gates":["test"]}`

	first := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/dispatch", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:45678"
	server.Handler().ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status code = %d, want %d body=%s", first.Code, http.StatusOK, first.Body.String())
	}
	var firstResponse DispatchResponse
	if err := json.NewDecoder(first.Body).Decode(&firstResponse); err != nil {
		t.Fatalf("decode first dispatch: %v", err)
	}
	if firstResponse.Replayed || firstResponse.Implementer == "" || firstResponse.Validator == "" || len(st.workers) != 2 {
		t.Fatalf("first response=%#v workers=%#v", firstResponse, st.workers)
	}

	second := httptest.NewRecorder()
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/dispatch", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:45678"
	server.Handler().ServeHTTP(second, req)
	if second.Code != http.StatusOK {
		t.Fatalf("second status code = %d, want %d body=%s", second.Code, http.StatusOK, second.Body.String())
	}
	var secondResponse DispatchResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResponse); err != nil {
		t.Fatalf("decode second dispatch: %v", err)
	}
	if !secondResponse.Replayed || secondResponse.Implementer != firstResponse.Implementer || secondResponse.Validator != firstResponse.Validator || len(st.workers) != 2 {
		t.Fatalf("second response=%#v first=%#v workers=%#v", secondResponse, firstResponse, st.workers)
	}
}

func TestServerDispatchUsesExplicitGateWithoutRepoHints(t *testing.T) {
	repo := t.TempDir()
	st := &memoryStore{}
	server := NewServerWithIssueProvider("state.json", st, fakeIssueProvider{issue: readiness.Issue{
		Title: "Dispatch issue",
		Body:  "Acceptance criteria",
	}})
	body := `{"request_id":"r-dispatch","issue":"MTG-Thomas/codex-swarm#31","repo":` + quoteJSON(repo) + `,"prompt":"implement issue #31","gates":["manual-test"]}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/dispatch", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:45678"
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response DispatchResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode dispatch: %v", err)
	}
	if response.Implementer == "" || response.Validator == "" || len(st.workers) != 2 {
		t.Fatalf("response=%#v workers=%#v", response, st.workers)
	}
}

func TestServerDispatchRejectsNonLoopbackRemote(t *testing.T) {
	server := NewServerWithIssueProvider("state.json", &memoryStore{}, fakeIssueProvider{issue: readiness.Issue{Title: "Issue", Body: "Body"}})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/dispatch", strings.NewReader(`{}`))
	req.RemoteAddr = "203.0.113.10:45678"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestServerV1DispatchReplayMismatchReturnsTypedError(t *testing.T) {
	repo := t.TempDir()
	writeDaemonRepoHints(t, repo)
	st := &memoryStore{}
	server := NewServerWithIssueProvider("state.json", st, fakeIssueProvider{issue: readiness.Issue{
		Title: "Dispatch issue",
		Body:  "Acceptance criteria",
	}})
	firstBody := `{"request_id":"r-dispatch","issue":"MTG-Thomas/codex-swarm#31","repo":` + quoteJSON(repo) + `,"prompt":"implement issue #31","gates":["test"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/dispatch", strings.NewReader(firstBody))
	req.RemoteAddr = "127.0.0.1:45678"
	first := httptest.NewRecorder()
	server.Handler().ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status code = %d, want %d body=%s", first.Code, http.StatusOK, first.Body.String())
	}

	secondBody := `{"request_id":"r-dispatch","issue":"MTG-Thomas/codex-swarm#31","repo":` + quoteJSON(repo) + `,"prompt":"different","gates":["test"]}`
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/dispatch", strings.NewReader(secondBody))
	req.RemoteAddr = "127.0.0.1:45678"
	second := httptest.NewRecorder()
	server.Handler().ServeHTTP(second, req)
	if second.Code != http.StatusConflict {
		t.Fatalf("second status code = %d, want %d body=%s", second.Code, http.StatusConflict, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"code":"request_replay_mismatch"`) {
		t.Fatalf("typed error body = %s", second.Body.String())
	}
}

func TestServerEventsSnapshotFiltersWorker(t *testing.T) {
	now := time.Date(2026, 7, 9, 18, 30, 0, 0, time.UTC)
	server := NewServer("state.json", &memoryStore{workers: []store.Worker{{
		ID:          "w-1",
		Issue:       "MTG-Thomas/codex-swarm#53",
		ProjectRoot: "/repo",
		Status:      store.WorkerIdle,
		Events: []store.Event{{
			At:      now,
			Type:    "message.received",
			Message: "hello",
		}},
	}, {
		ID:          "w-2",
		ProjectRoot: "/repo",
		Status:      store.WorkerIdle,
		Events: []store.Event{{
			At:      now,
			Type:    "message.received",
			Message: "other",
		}},
	}}})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/events?worker=w-1", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"worker_id":"w-1"`) || strings.Contains(rec.Body.String(), `"worker_id":"w-2"`) {
		t.Fatalf("events response = %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"schema":"codex-swarm:event:v1"`) {
		t.Fatalf("events response missing schema: %s", rec.Body.String())
	}
}

func TestServerEventsSnapshotUsesStoredEvents(t *testing.T) {
	now := time.Date(2026, 7, 9, 18, 30, 0, 0, time.UTC)
	server := NewServer("state.json", &memoryStore{
		workers: []store.Worker{{ID: "w-1", Issue: "MTG-Thomas/codex-swarm#53"}, {ID: "w-2"}},
		events: []store.Event{
			{At: now, WorkerID: "w-1", Type: "message.received", Message: "stored"},
			{At: now, WorkerID: "w-2", Type: "message.received", Message: "other"},
		},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/events?worker=w-1", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"message":"stored"`) || strings.Contains(rec.Body.String(), `"message":"other"`) {
		t.Fatalf("stored events response = %s", rec.Body.String())
	}
}

func TestClientStatus(t *testing.T) {
	server := httptest.NewServer(NewServer("state.json", &memoryStore{workers: []store.Worker{{ID: "w-1"}}}).Handler())
	defer server.Close()

	status, err := (Client{BaseURL: server.URL}).Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.StatePath != "state.json" || status.WorkerCount != 1 {
		t.Fatalf("status = %#v", status)
	}
}

func quoteJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestClientFallsBackToLegacyStatus(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, LegacyStatus{
			Daemon:    "running",
			StatePath: "legacy-state.json",
			Workers:   []LegacyWorker{{ID: "w-legacy", Status: "idle", ThreadID: "thread-legacy"}},
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
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("Status() error = %v, want response body", err)
	}
}

func TestClientErrorIncludesResponseBody(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/readiness", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gh auth missing", http.StatusBadRequest)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	_, err := (Client{BaseURL: server.URL}).Readiness(context.Background(), "MTG-Thomas/codex-swarm#46", "/repo")
	if err == nil {
		t.Fatal("Readiness() error = nil, want server error")
	}
	if !strings.Contains(err.Error(), "400 Bad Request") || !strings.Contains(err.Error(), "gh auth missing") {
		t.Fatalf("Readiness() error = %v, want status and body", err)
	}
}

func TestClientPostErrorIncludesResponseBody(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/dispatch", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "dispatch blocked", http.StatusConflict)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	_, err := (Client{BaseURL: server.URL}).Dispatch(context.Background(), DispatchRequest{
		RequestID: "r-test",
		Issue:     "MTG-Thomas/codex-swarm#46",
		Repo:      "/repo",
		Prompt:    "test dispatch",
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want server error")
	}
	if !strings.Contains(err.Error(), "409 Conflict") || !strings.Contains(err.Error(), "dispatch blocked") {
		t.Fatalf("Dispatch() error = %v, want status and body", err)
	}
}

func TestReadOnlyEndpointsRejectPost(t *testing.T) {
	server := NewServer("state.json", &memoryStore{})
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
