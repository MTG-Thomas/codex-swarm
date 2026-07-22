package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/appserver"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestDaemonAppserverSpawnReturnsAfterDurableIdentityForFourLongTurns(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	parent := store.Worker{ID: "parent", Engine: "tracker", Status: store.WorkerIdle, ProjectRoot: t.TempDir(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := st.SaveWorker(parent); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	runner := &blockingAppserverRunner{release: release, started: make(chan string, 4), returned: make(chan string, 4)}
	serverState := NewServer(statePath, st)
	serverState.SetAppserverTurnRunner(runner)
	defer serverState.Close()
	server := httptest.NewServer(serverState.Handler())
	defer server.Close()
	client := Client{BaseURL: server.URL}

	for i := range 4 {
		worker := testAppserverWorker(t, st, parent.ID, i)
		prompt := fmt.Sprintf("task-%d", i)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		response, err := client.SpawnAppserver(ctx, protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: prompt})
		cancel()
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		if response.WorkerID != worker.ID || response.HostID != "local" || response.ThreadID != "thread-"+prompt || response.TurnID != "turn-"+prompt || response.Worktree != worker.Worktree || response.Status != string(store.WorkerRunning) || response.RuntimeOwner != string(store.RuntimeOwnerCS) {
			t.Fatalf("spawn %d response = %#v", i, response)
		}
		persisted, err := st.GetWorker(worker.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.ThreadID != response.ThreadID || persisted.TurnID != response.TurnID || persisted.Status != store.WorkerRunning {
			t.Fatalf("spawn %d durable worker = %#v", i, persisted)
		}
	}
	if runner.callCount() != 4 {
		t.Fatalf("runner calls = %d, want 4", runner.callCount())
	}

	replayWorker, err := st.GetWorker("worker-0")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.SpawnAppserver(context.Background(), protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(replayWorker.ID), WorkerID: replayWorker.ID, Prompt: "task-0"})
	if err != nil {
		t.Fatalf("replay spawn: %v", err)
	}
	if !replay.Replayed || replay.ThreadID != "thread-task-0" || runner.callCount() != 4 {
		t.Fatalf("replay = %#v calls=%d", replay, runner.callCount())
	}
	_, err = client.SpawnAppserver(context.Background(), protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(replayWorker.ID), WorkerID: replayWorker.ID, Prompt: "different"})
	if err == nil || !strings.Contains(err.Error(), "409 Conflict") {
		t.Fatalf("mismatched replay error = %v, want conflict", err)
	}
	if runner.callCount() != 4 {
		t.Fatalf("runner calls after mismatch = %d, want 4", runner.callCount())
	}

	close(release)
	for range 4 {
		select {
		case <-runner.returned:
		case <-time.After(time.Second):
			t.Fatal("first turn did not return after release")
		}
	}
	for i := range 4 {
		workerID := fmt.Sprintf("worker-%d", i)
		waitForWorkerStatus(t, st, workerID, store.WorkerDone)
		worker, _ := st.GetWorker(workerID)
		if worker.ThreadID != fmt.Sprintf("thread-task-%d", i) || worker.TurnID != fmt.Sprintf("turn-task-%d", i) {
			t.Fatalf("worker %s lost task identity: %#v", workerID, worker)
		}
	}
	completionMessages, err := st.ListMessages(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	completionCount := 0
	for _, delivered := range completionMessages {
		if delivered.Message.Kind == store.MessageCompletion {
			completionCount++
		}
	}
	if completionCount != 4 {
		t.Fatalf("asynchronous parent completions = %d, want 4", completionCount)
	}
}

func TestDaemonAppserverSpawnRequestTimeoutDoesNotFailOrDuplicateTask(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	worker := testAppserverWorker(t, st, "", 0)
	allowStart := make(chan struct{})
	release := make(chan struct{})
	runner := &blockingAppserverRunner{allowStart: allowStart, release: release, called: make(chan string, 1), started: make(chan string, 1), returned: make(chan string, 1)}
	serverState := NewServer(statePath, st)
	serverState.SetAppserverTurnRunner(runner)
	defer serverState.Close()
	server := httptest.NewServer(serverState.Handler())
	defer server.Close()
	client := Client{BaseURL: server.URL}
	request := protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "slow-start"}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := client.SpawnAppserver(ctx, request)
		errCh <- err
	}()
	select {
	case <-runner.called:
	case <-time.After(time.Second):
		t.Fatal("daemon did not accept launch before request cancellation")
	}
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("spawn error = %v, want context canceled", err)
	}
	pending, err := st.GetWorker(worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status == store.WorkerFailed {
		t.Fatalf("request cancellation marked worker failed: %#v", pending)
	}

	close(allowStart)
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("daemon did not continue launch after request cancellation")
	}
	waitForWorkerStatus(t, st, worker.ID, store.WorkerRunning)
	replay, err := client.SpawnAppserver(context.Background(), request)
	if err != nil {
		t.Fatalf("replay after timeout: %v", err)
	}
	if !replay.Replayed || replay.ThreadID != "thread-slow-start" || replay.TurnID != "turn-slow-start" || runner.callCount() != 1 {
		t.Fatalf("replay = %#v calls=%d", replay, runner.callCount())
	}
	close(release)
	waitForWorkerStatus(t, st, worker.ID, store.WorkerDone)
}

func TestDaemonShutdownLeavesDurableAppserverTaskResumable(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	worker := testAppserverWorker(t, st, "", 0)
	runner := &blockingAppserverRunner{release: make(chan struct{}), started: make(chan string, 1), returned: make(chan string, 1)}
	serverState := NewServer(statePath, st)
	serverState.SetAppserverTurnRunner(runner)
	server := httptest.NewServer(serverState.Handler())
	client := Client{BaseURL: server.URL}
	response, err := client.SpawnAppserver(context.Background(), protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "shutdown"})
	if err != nil {
		t.Fatal(err)
	}
	serverState.Close()
	waitForWorkerStatus(t, st, worker.ID, store.WorkerIdle)
	server.Close()
	resumable, _ := st.GetWorker(worker.ID)
	if resumable.ThreadID != response.ThreadID || resumable.TurnID != response.TurnID || !workerHasEvent(resumable, "appserver.turn.detached") {
		t.Fatalf("resumable worker = %#v", resumable)
	}
}

func TestPrivilegedDaemonRefusesAppserverSpawn(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	worker := testAppserverWorker(t, st, "", 0)
	runner := &blockingAppserverRunner{release: make(chan struct{})}
	serverState := NewServer(statePath, st)
	serverState.SetAppserverTurnRunner(runner)
	serverState.spawnDisabled = "privileged_service_identity"
	defer serverState.Close()
	server := httptest.NewServer(serverState.Handler())
	defer server.Close()
	_, err := (Client{BaseURL: server.URL}).SpawnAppserver(context.Background(), protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "refuse"})
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") || !AppserverSpawnNeedsCallerRuntime(err) {
		t.Fatalf("privileged spawn error = %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("privileged daemon launched %d turns", runner.callCount())
	}
}

func TestSeparateDaemonProcessesCannotDuplicateAppserverTask(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	worker := testAppserverWorker(t, st, "", 0)
	release := make(chan struct{})
	runner := &blockingAppserverRunner{release: release, started: make(chan string, 1), returned: make(chan string, 1)}
	firstState := NewServer(statePath, st)
	firstState.SetAppserverTurnRunner(runner)
	defer firstState.Close()
	secondState := NewServer(statePath, st)
	secondState.SetAppserverTurnRunner(runner)
	defer secondState.Close()
	first := httptest.NewServer(firstState.Handler())
	defer first.Close()
	second := httptest.NewServer(secondState.Handler())
	defer second.Close()
	request := protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "one-task"}
	type outcome struct {
		response protocol.AppserverSpawnResponse
		err      error
	}
	outcomes := make(chan outcome, 2)
	for _, baseURL := range []string{first.URL, second.URL} {
		go func() {
			response, err := (Client{BaseURL: baseURL}).SpawnAppserver(context.Background(), request)
			outcomes <- outcome{response: response, err: err}
		}()
	}
	valid := 0
	for range 2 {
		result := <-outcomes
		if result.err == nil {
			if result.response.ThreadID != "thread-one-task" || result.response.TurnID != "turn-one-task" {
				t.Fatalf("unexpected identity: %#v", result.response)
			}
			valid++
			continue
		}
		if !strings.Contains(result.err.Error(), "409 Conflict") {
			t.Fatalf("competing launch error = %v", result.err)
		}
	}
	if valid == 0 || runner.callCount() != 1 {
		t.Fatalf("valid responses=%d runner calls=%d, want at least one response and one task", valid, runner.callCount())
	}
	close(release)
	waitForWorkerStatus(t, st, worker.ID, store.WorkerDone)
}

func TestObserverPersistenceFailureSalvagesDurableTaskIdentity(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	base := store.NewJSONStore(statePath)
	st := &flakyUpdateStore{JSONStore: base, failAt: 2}
	worker := testAppserverWorker(t, base, "", 0)
	runner := &blockingAppserverRunner{release: make(chan struct{})}
	serverState := NewServer(statePath, st)
	serverState.SetAppserverTurnRunner(runner)
	defer serverState.Close()
	server := httptest.NewServer(serverState.Handler())
	defer server.Close()
	response, err := (Client{BaseURL: server.URL}).SpawnAppserver(context.Background(), protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "salvage"})
	if err != nil {
		t.Fatalf("spawn with transient observer persistence error: %v", err)
	}
	if response.ThreadID != "thread-salvage" || response.TurnID != "turn-salvage" || response.Status != string(store.WorkerIdle) {
		t.Fatalf("salvaged response = %#v", response)
	}
	salvaged, _ := base.GetWorker(worker.ID)
	if salvaged.Status != store.WorkerIdle || !workerHasEvent(salvaged, "appserver.turn.detached") {
		t.Fatalf("salvaged worker = %#v", salvaged)
	}
}

func TestTurnStartFailurePreservesThreadIdentity(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	worker := testAppserverWorker(t, st, "", 0)
	serverState := NewServer(statePath, st)
	serverState.SetAppserverTurnRunner(appserverRunnerFunc(func(context.Context, string, string, appserver.TurnObserver, appserver.SteeringPolicy) (appserver.RunResult, error) {
		return appserver.RunResult{ThreadID: "thread-only", Status: "threadStarted"}, errors.New("turn/start unavailable")
	}))
	defer serverState.Close()
	server := httptest.NewServer(serverState.Handler())
	defer server.Close()
	_, err := (Client{BaseURL: server.URL}).SpawnAppserver(context.Background(), protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "thread-only"})
	if err == nil {
		t.Fatal("spawn error = nil, want turn/start failure")
	}
	failed, _ := st.GetWorker(worker.ID)
	if failed.Status != store.WorkerFailed || failed.ThreadID != "thread-only" || failed.TurnID != "" {
		t.Fatalf("failed worker = %#v", failed)
	}
}

func TestCallerOwnedRuntimeCancellationPersistsResumableState(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	worker := testAppserverWorker(t, st, "", 0)
	runner := &blockingAppserverRunner{release: make(chan struct{}), started: make(chan string, 1), returned: make(chan string, 1)}
	reader, writer := io.Pipe()
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runAppserverRuntime(ctx, statePath, protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "cancel-runtime"}, writer, runner)
		_ = writer.Close()
	}()
	var response protocol.AppserverSpawnResponse
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		t.Fatalf("decode runtime start: %v", err)
	}
	if response.ThreadID != "thread-cancel-runtime" || response.TurnID != "turn-cancel-runtime" {
		t.Fatalf("runtime response = %#v", response)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("runtime cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller-owned runtime did not stop on cancellation")
	}
	resumable, _ := st.GetWorker(worker.ID)
	if resumable.Status != store.WorkerIdle || resumable.ThreadID != response.ThreadID || resumable.TurnID != response.TurnID || !workerHasEvent(resumable, "appserver.turn.detached") {
		t.Fatalf("resumable worker = %#v", resumable)
	}
}

func TestCallerOwnedRuntimeParentDisconnectStillCompletesTurn(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	st := store.NewJSONStore(statePath)
	worker := testAppserverWorker(t, st, "", 0)
	release := make(chan struct{})
	runner := &blockingAppserverRunner{release: release, started: make(chan string, 1), returned: make(chan string, 1)}
	done := make(chan error, 1)
	go func() {
		done <- runAppserverRuntime(context.Background(), statePath, protocol.AppserverSpawnRequest{RequestID: appserverSpawnRequestID(worker.ID), WorkerID: worker.ID, Prompt: "parent-disconnected"}, failingResponseWriter{}, runner)
	}()

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("caller-owned runtime did not persist durable task identity")
	}
	select {
	case err := <-done:
		t.Fatalf("runtime returned after parent response failure: %v", err)
	default:
	}

	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runtime completion error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller-owned runtime did not finish after turn completion")
	}
	completed, err := st.GetWorker(worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.WorkerDone || completed.ThreadID != "thread-parent-disconnected" || completed.TurnID != "turn-parent-disconnected" || !workerHasEvent(completed, "appserver.start_response.write.failed") || !workerHasEvent(completed, "appserver.turn.completed") {
		t.Fatalf("completed worker = %#v", completed)
	}
}

type failingResponseWriter struct{}

func (failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("response pipe closed")
}

type blockingAppserverRunner struct {
	allowStart <-chan struct{}
	release    <-chan struct{}
	started    chan string
	returned   chan string
	called     chan string
	mu         sync.Mutex
	calls      int
}

type appserverRunnerFunc func(context.Context, string, string, appserver.TurnObserver, appserver.SteeringPolicy) (appserver.RunResult, error)

func (f appserverRunnerFunc) RunTurnCoordinated(ctx context.Context, cwd, prompt string, observer appserver.TurnObserver, steering appserver.SteeringPolicy) (appserver.RunResult, error) {
	return f(ctx, cwd, prompt, observer, steering)
}

type flakyUpdateStore struct {
	*store.JSONStore
	mu     sync.Mutex
	calls  int
	failAt int
}

func (s *flakyUpdateStore) UpdateWorker(id string, mutate func(*store.Worker) error) (store.Worker, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == s.failAt {
		return store.Worker{}, errors.New("transient store failure")
	}
	return s.JSONStore.UpdateWorker(id, mutate)
}

func (r *blockingAppserverRunner) RunTurnCoordinated(ctx context.Context, _ string, prompt string, observer appserver.TurnObserver, _ appserver.SteeringPolicy) (appserver.RunResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.called != nil {
		r.called <- prompt
	}
	if r.allowStart != nil {
		select {
		case <-r.allowStart:
		case <-ctx.Done():
			return appserver.RunResult{}, ctx.Err()
		}
	}
	started := appserver.RunResult{ThreadID: "thread-" + prompt, TurnID: "turn-" + prompt, Status: "inProgress"}
	if err := observer(started); err != nil {
		return started, err
	}
	if r.started != nil {
		r.started <- prompt
	}
	select {
	case <-r.release:
		if r.returned != nil {
			r.returned <- prompt
		}
		return appserver.RunResult{ThreadID: started.ThreadID, TurnID: started.TurnID, Status: "completed", FinalMessage: "completed " + prompt}, nil
	case <-ctx.Done():
		if r.returned != nil {
			r.returned <- prompt
		}
		return started, ctx.Err()
	}
}

func (r *blockingAppserverRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func testAppserverWorker(t *testing.T, st *store.JSONStore, parentID string, index int) store.Worker {
	t.Helper()
	at := time.Now().UTC()
	worker := store.Worker{ID: fmt.Sprintf("worker-%d", index), ParentID: parentID, Engine: "appserver", RuntimeOwner: store.RuntimeOwnerCS, HostID: "local", Status: store.WorkerPending, ProjectRoot: t.TempDir(), Worktree: t.TempDir(), CreatedAt: at, UpdatedAt: at}
	worker.ApplyStatusAt(store.WorkerPending, at)
	if err := st.SaveWorker(worker); err != nil {
		t.Fatal(err)
	}
	return worker
}

func waitForWorkerStatus(t *testing.T, st *store.JSONStore, workerID string, want store.WorkerStatus) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		worker, err := st.GetWorker(workerID)
		if err != nil {
			t.Fatal(err)
		}
		if worker.Status == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("worker %s status = %s, want %s", workerID, worker.Status, want)
		case <-ticker.C:
		}
	}
}

func workerHasEvent(worker store.Worker, eventType string) bool {
	for _, event := range worker.Events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
