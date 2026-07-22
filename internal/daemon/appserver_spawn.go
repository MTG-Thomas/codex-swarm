package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/appserver"
	"github.com/MTG-Thomas/codex-swarm/internal/coordination"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const maxAppserverPromptBytes = 1 << 20

// AppserverTurnRunner is the daemon-owned boundary for one first turn.
type AppserverTurnRunner interface {
	RunTurnCoordinated(context.Context, string, string, appserver.TurnObserver, appserver.SteeringPolicy) (appserver.RunResult, error)
}

type appserverWorkerStore interface {
	coordinationStore
	UpdateWorker(string, func(*store.Worker) error) (store.Worker, error)
	ListQueuedMessages(string) ([]store.DeliveredMessage, error)
}

type appserverLaunch struct {
	requestID   string
	fingerprint string
	ready       chan struct{}
	done        chan struct{}
	mu          sync.Mutex
	response    protocol.AppserverSpawnResponse
	err         error
	readyOnce   sync.Once
}

func (l *appserverLaunch) finishStart(response protocol.AppserverSpawnResponse, err error) {
	l.mu.Lock()
	l.response = response
	l.err = err
	l.mu.Unlock()
	l.readyOnce.Do(func() { close(l.ready) })
}

func (l *appserverLaunch) startResult() (protocol.AppserverSpawnResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.response, l.err
}

func (s *Server) appserverStore() (appserverWorkerStore, error) {
	st, ok := s.store.(appserverWorkerStore)
	if !ok {
		return nil, errors.New("store does not support daemon-owned app-server workers")
	}
	return st, nil
}

func (s *Server) handleAppserverSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "app-server spawn requires loopback daemon access")
		return
	}
	if s.spawnDisabled != "" {
		writeRouteError(w, r, http.StatusServiceUnavailable, "appserver_runtime_unavailable", "daemon cannot launch Codex under "+s.spawnDisabled+"; use the caller-owned csd appserver runtime")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAppserverPromptBytes+(64<<10))
	var request protocol.AppserverSpawnRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "parse app-server spawn request: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "app-server spawn request must contain one JSON document")
		return
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	if err := validateAppserverSpawnRequest(request); err != nil {
		status, code := http.StatusBadRequest, "invalid_spawn"
		if len(request.Prompt) > maxAppserverPromptBytes {
			status, code = http.StatusRequestEntityTooLarge, "prompt_too_large"
		}
		writeRouteError(w, r, status, code, err.Error())
		return
	}
	response, err := s.startAppserverWorker(r.Context(), request)
	if err != nil {
		status, code := http.StatusInternalServerError, "appserver_spawn_failed"
		if errors.Is(err, store.ErrWorkerNotFound) {
			status, code = http.StatusNotFound, "worker_not_found"
		} else if errors.Is(err, errAppserverSpawnConflict) {
			status, code = http.StatusConflict, "request_replay_mismatch"
		} else if errors.Is(err, errInvalidAppserverWorker) {
			status, code = http.StatusBadRequest, "invalid_worker"
		}
		writeRouteError(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, response)
}

var (
	errAppserverSpawnConflict = errors.New("app-server spawn request conflicts with durable worker state")
	errInvalidAppserverWorker = errors.New("invalid app-server worker")
)

func (s *Server) startAppserverWorker(ctx context.Context, request protocol.AppserverSpawnRequest) (protocol.AppserverSpawnResponse, error) {
	st, err := s.appserverStore()
	if err != nil {
		return protocol.AppserverSpawnResponse{}, err
	}
	worker, err := st.GetWorker(request.WorkerID)
	if err != nil {
		return protocol.AppserverSpawnResponse{}, err
	}
	if worker.Engine != "appserver" || worker.RuntimeOwner != store.RuntimeOwnerCS {
		return protocol.AppserverSpawnResponse{}, fmt.Errorf("%w: worker=%s engine=%s runtime_owner=%s", errInvalidAppserverWorker, worker.ID, worker.Engine, worker.RuntimeOwner)
	}
	fingerprint := appserverSpawnFingerprint(worker, request.Prompt)

	s.launchMu.Lock()
	if existing := s.launches[worker.ID]; existing != nil {
		if existing.requestID != request.RequestID || existing.fingerprint != fingerprint {
			s.launchMu.Unlock()
			return protocol.AppserverSpawnResponse{}, fmt.Errorf("%w: worker=%s already has a different launch", errAppserverSpawnConflict, worker.ID)
		}
		s.launchMu.Unlock()
		select {
		case <-existing.ready:
		case <-ctx.Done():
			return protocol.AppserverSpawnResponse{}, ctx.Err()
		}
		response, err := existing.startResult()
		response.Replayed = err == nil
		return response, err
	}
	if replayed, replayErr := replayAppserverSpawn(worker, request.RequestID, fingerprint); replayed || replayErr != nil {
		s.launchMu.Unlock()
		if replayErr != nil {
			return protocol.AppserverSpawnResponse{}, replayErr
		}
		return appserverSpawnResponse(worker, request.RequestID, true), nil
	}
	launch := &appserverLaunch{requestID: request.RequestID, fingerprint: fingerprint, ready: make(chan struct{}), done: make(chan struct{})}
	s.launches[worker.ID] = launch
	s.launchMu.Unlock()

	requestedAt := time.Now().UTC()
	worker, err = st.UpdateWorker(worker.ID, func(current *store.Worker) error {
		if current.ThreadID != "" || current.TurnID != "" {
			return fmt.Errorf("%w: worker=%s already has thread=%s turn=%s", errAppserverSpawnConflict, current.ID, current.ThreadID, current.TurnID)
		}
		for _, event := range current.Events {
			if event.Type == "appserver.spawn.requested" {
				return fmt.Errorf("%w: worker=%s already has launch request=%s", errAppserverSpawnConflict, current.ID, event.RequestID)
			}
		}
		current.ApplyStatusAt(store.WorkerPending, requestedAt)
		current.UpdatedAt = requestedAt
		current.LastMessage = "daemon accepted app-server launch; waiting for durable thread and turn identity"
		current.Events = append(current.Events, store.Event{At: requestedAt, Type: "appserver.spawn.requested", Message: fingerprint, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
		return nil
	})
	if err != nil {
		s.removeLaunch(worker.ID, launch)
		launch.finishStart(protocol.AppserverSpawnResponse{}, err)
		return protocol.AppserverSpawnResponse{}, err
	}

	s.runtimeWG.Add(1)
	go func() {
		defer s.runtimeWG.Done()
		s.runAppserverWorker(st, worker, request, launch)
	}()
	select {
	case <-launch.ready:
	case <-ctx.Done():
		return protocol.AppserverSpawnResponse{}, ctx.Err()
	}
	return launch.startResult()
}

func (s *Server) runAppserverWorker(st appserverWorkerStore, worker store.Worker, request protocol.AppserverSpawnRequest, launch *appserverLaunch) {
	defer func() {
		close(launch.done)
		s.removeLaunch(worker.ID, launch)
	}()
	runner := s.appserverRunner(worker)
	startedPersisted := false
	result, runErr := runner.RunTurnCoordinated(s.runtimeCtx, workerExecutionRoot(worker), request.Prompt, func(started appserver.RunResult) error {
		at := time.Now().UTC()
		updated, err := st.UpdateWorker(worker.ID, func(current *store.Worker) error {
			current.ThreadID = started.ThreadID
			current.TurnID = started.TurnID
			current.ApplyStatusAt(store.WorkerRunning, at)
			current.UpdatedAt = at
			current.LastMessage = fmt.Sprintf("daemon owns active app-server turn: host=%s thread=%s turn=%s worktree=%s", emptyDash(current.HostID), started.ThreadID, started.TurnID, emptyDash(current.Worktree))
			current.Events = append(current.Events, store.Event{At: at, Type: "appserver.turn.started", Message: current.LastMessage, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
			return nil
		})
		if err != nil {
			return err
		}
		startedPersisted = true
		launch.finishStart(appserverSpawnResponse(updated, request.RequestID, false), nil)
		return nil
	}, daemonSteeringPolicy(st, worker.ID))

	if !startedPersisted {
		if runErr == nil {
			runErr = errors.New("runner returned before persisting thread and turn identity")
		}
		if result.ThreadID != "" && result.TurnID != "" {
			at := time.Now().UTC()
			updated, persistErr := st.UpdateWorker(worker.ID, func(current *store.Worker) error {
				current.ThreadID = result.ThreadID
				current.TurnID = result.TurnID
				current.ApplyStatusAt(store.WorkerIdle, at)
				current.UpdatedAt = at
				current.LastMessage = fmt.Sprintf("app-server identity recovered after persistence error; task remains resumable: host=%s thread=%s turn=%s: %v", emptyDash(current.HostID), result.ThreadID, result.TurnID, runErr)
				current.Events = append(current.Events, store.Event{At: at, Type: "appserver.turn.detached", Message: current.LastMessage, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
				return nil
			})
			if persistErr == nil {
				launch.finishStart(appserverSpawnResponse(updated, request.RequestID, false), nil)
				return
			}
			runErr = errors.Join(runErr, fmt.Errorf("retry durable task identity: %w", persistErr))
		}
		failure := fmt.Errorf("start app-server worker=%s host=%s worktree=%s: %w", worker.ID, emptyDash(worker.HostID), emptyDash(worker.Worktree), runErr)
		at := time.Now().UTC()
		_, _ = st.UpdateWorker(worker.ID, func(current *store.Worker) error {
			current.ThreadID = result.ThreadID
			current.TurnID = result.TurnID
			current.ApplyStatusAt(store.WorkerFailed, at)
			current.UpdatedAt = at
			current.LastMessage = failure.Error()
			current.Events = append(current.Events, store.Event{At: at, Type: "appserver.spawn.failed", Message: current.LastMessage, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
			return nil
		})
		launch.finishStart(protocol.AppserverSpawnResponse{}, failure)
		return
	}

	at := time.Now().UTC()
	updated, updateErr := st.UpdateWorker(worker.ID, func(current *store.Worker) error {
		current.ThreadID = result.ThreadID
		current.TurnID = result.TurnID
		current.UpdatedAt = at
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				current.ApplyStatusAt(store.WorkerIdle, at)
				current.LastMessage = fmt.Sprintf("daemon runtime detached; task remains resumable: host=%s thread=%s turn=%s", emptyDash(current.HostID), current.ThreadID, current.TurnID)
				current.Events = append(current.Events, store.Event{At: at, Type: "appserver.turn.detached", Message: current.LastMessage, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
				return nil
			}
			current.ApplyStatusAt(store.WorkerFailed, at)
			current.LastMessage = fmt.Sprintf("app-server turn failed: host=%s thread=%s turn=%s: %v", emptyDash(current.HostID), current.ThreadID, current.TurnID, runErr)
			current.Events = append(current.Events, store.Event{At: at, Type: "appserver.turn.failed", Message: current.LastMessage, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
			return nil
		}
		status := workerStatusFromAppserverTurn(result.Status)
		current.ApplyStatusAt(status, at)
		current.LastMessage = fmt.Sprintf("app-server turn completed: host=%s thread=%s turn=%s status=%s", emptyDash(current.HostID), result.ThreadID, result.TurnID, result.Status)
		current.Events = append(current.Events, store.Event{At: at, Type: "appserver.turn.completed", Message: current.LastMessage, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
		for _, warning := range result.Warnings {
			current.Events = append(current.Events, store.Event{At: at, Type: "appserver.warning", Message: warning, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
		}
		if strings.TrimSpace(result.FinalMessage) != "" {
			current.Report = strings.TrimSpace(result.FinalMessage)
			current.Events = append(current.Events, store.Event{At: at, Type: "appserver.agent.message", Message: current.Report, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
		}
		return nil
	})
	if updateErr != nil {
		return
	}
	if len(result.FileChanges) > 0 {
		if err := recordDaemonFileChanges(st, updated, result.FileChanges, at); err != nil {
			_, _ = st.UpdateWorker(updated.ID, func(current *store.Worker) error {
				current.UpdatedAt = time.Now().UTC()
				current.Events = append(current.Events, store.Event{At: time.Now().UTC(), Type: "appserver.file_changes.failed", Message: err.Error(), WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
				return nil
			})
		}
	}
	if updated.Status == store.WorkerDone || updated.Status == store.WorkerFailed {
		report := updated.Report
		if report == "" {
			report = updated.LastMessage
		}
		if _, _, err := (coordination.Service{Store: st}).ForwardCompletion(context.Background(), "appserver-completion-"+updated.ID+"-"+updated.TurnID, updated.ID, report); err != nil {
			_, _ = st.UpdateWorker(updated.ID, func(current *store.Worker) error {
				current.UpdatedAt = time.Now().UTC()
				current.Events = append(current.Events, store.Event{At: time.Now().UTC(), Type: "appserver.completion.forward.failed", Message: err.Error(), WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
				return nil
			})
		}
	}
}

// RunAppserverRuntime owns one worker turn in a caller-session csd process.
// It writes the durable start response once, then remains alive until the turn
// completes or the process context is canceled.
func RunAppserverRuntime(ctx context.Context, statePath string, request protocol.AppserverSpawnRequest, started io.Writer) error {
	return runAppserverRuntime(ctx, statePath, request, started, nil)
}

func runAppserverRuntime(ctx context.Context, statePath string, request protocol.AppserverSpawnRequest, started io.Writer, runner AppserverTurnRunner) error {
	if runner == nil {
		if reason := privilegedRuntimeReason(); reason != "" {
			return fmt.Errorf("refuse app-server runtime under %s", reason)
		}
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	if err := validateAppserverSpawnRequest(request); err != nil {
		return err
	}
	server := NewServer(statePath, store.NewJSONStore(statePath))
	if runner != nil {
		server.SetAppserverTurnRunner(runner)
	}
	defer server.Close()
	response, err := server.startAppserverWorker(ctx, request)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(started).Encode(response); err != nil {
		// The parent CLI may reach its deadline and close stdout after the
		// runtime has durably persisted task identity. Losing that response
		// channel must not cancel the detached first turn.
		if st, storeErr := server.appserverStore(); storeErr == nil {
			at := time.Now().UTC()
			_, _ = st.UpdateWorker(response.WorkerID, func(current *store.Worker) error {
				current.UpdatedAt = at
				message := fmt.Sprintf("parent response channel unavailable; app-server turn continues asynchronously: %v", err)
				current.Events = append(current.Events, store.Event{At: at, Type: "appserver.start_response.write.failed", Message: message, WorkerID: current.ID, Issue: current.Issue, RequestID: request.RequestID})
				return nil
			})
		}
	}
	server.launchMu.Lock()
	launch := server.launches[request.WorkerID]
	server.launchMu.Unlock()
	if launch == nil {
		return nil
	}
	select {
	case <-launch.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validateAppserverSpawnRequest(request protocol.AppserverSpawnRequest) error {
	if request.WorkerID == "" || request.RequestID == "" || strings.TrimSpace(request.Prompt) == "" {
		return errors.New("request_id, worker_id, and prompt are required")
	}
	if request.RequestID != appserverSpawnRequestID(request.WorkerID) {
		return errors.New("request_id must be appserver-spawn-<worker_id>")
	}
	if len(request.Prompt) > maxAppserverPromptBytes {
		return errors.New("app-server prompt exceeds 1 MiB")
	}
	return nil
}

func (s *Server) appserverRunner(worker store.Worker) AppserverTurnRunner {
	if s.spawnRunner != nil {
		return s.spawnRunner
	}
	if worker.Remote == nil {
		return appserver.Runner{}
	}
	return appserver.Runner{Process: appserver.SSHProcess{Target: worker.Remote.Host, Jump: worker.Remote.JumpHost, CodexBinary: worker.Remote.CodexBinary}, Sandbox: "danger-full-access"}
}

func (s *Server) removeLaunch(workerID string, launch *appserverLaunch) {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	if s.launches[workerID] == launch {
		delete(s.launches, workerID)
	}
}

func replayAppserverSpawn(worker store.Worker, requestID, fingerprint string) (bool, error) {
	for i := len(worker.Events) - 1; i >= 0; i-- {
		event := worker.Events[i]
		if event.Type != "appserver.spawn.requested" || event.RequestID != requestID {
			continue
		}
		if event.Message != fingerprint {
			return false, fmt.Errorf("%w: worker=%s request_id=%s fingerprint changed", errAppserverSpawnConflict, worker.ID, requestID)
		}
		if worker.ThreadID == "" || worker.TurnID == "" {
			return false, fmt.Errorf("%w: worker=%s request_id=%s has uncertain launch without durable task identity", errAppserverSpawnConflict, worker.ID, requestID)
		}
		return true, nil
	}
	if worker.ThreadID != "" || worker.TurnID != "" {
		return false, fmt.Errorf("%w: worker=%s already has thread=%s turn=%s", errAppserverSpawnConflict, worker.ID, worker.ThreadID, worker.TurnID)
	}
	return false, nil
}

func appserverSpawnFingerprint(worker store.Worker, prompt string) string {
	data, _ := json.Marshal(struct {
		WorkerID string                 `json:"worker_id"`
		HostID   string                 `json:"host_id"`
		Root     string                 `json:"root"`
		Worktree string                 `json:"worktree"`
		Remote   *store.RemoteExecution `json:"remote,omitempty"`
		Prompt   string                 `json:"prompt"`
	}{worker.ID, worker.HostID, worker.ProjectRoot, worker.Worktree, worker.Remote, prompt})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func appserverSpawnRequestID(workerID string) string {
	return "appserver-spawn-" + workerID
}

func appserverSpawnResponse(worker store.Worker, requestID string, replayed bool) protocol.AppserverSpawnResponse {
	return protocol.AppserverSpawnResponse{RequestID: requestID, WorkerID: worker.ID, HostID: worker.HostID, ThreadID: worker.ThreadID, TurnID: worker.TurnID, Worktree: worker.Worktree, Status: string(worker.Status), RuntimeOwner: string(worker.RuntimeOwner), Replayed: replayed}
}

func workerExecutionRoot(worker store.Worker) string {
	if worker.Worktree != "" {
		return worker.Worktree
	}
	return worker.ProjectRoot
}

func workerStatusFromAppserverTurn(status string) store.WorkerStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return store.WorkerDone
	case "failed", "cancelled", "canceled":
		return store.WorkerFailed
	default:
		return store.WorkerRunning
	}
}

func daemonSteeringPolicy(st appserverWorkerStore, workerID string) appserver.SteeringPolicy {
	attempted := map[string]struct{}{}
	return appserver.SteeringPolicy{
		PollInterval: 500 * time.Millisecond,
		Source: func(context.Context) ([]appserver.SteerDelivery, error) {
			queued, err := st.ListQueuedMessages(workerID)
			if err != nil {
				return nil, err
			}
			deliveries := make([]appserver.SteerDelivery, 0, len(queued))
			for _, item := range queued {
				if _, ok := attempted[item.Delivery.ID]; ok {
					continue
				}
				text := fmt.Sprintf("SWARM_%s from=%s message_id=%s\n%s", strings.ToUpper(string(item.Message.Kind)), item.Message.From, item.Message.ID, item.Message.Body)
				deliveries = append(deliveries, appserver.SteerDelivery{ID: item.Delivery.ID, Text: text})
			}
			return deliveries, nil
		},
		Acknowledge: func(id string, deliveryErr error) {
			attempted[id] = struct{}{}
			if deliveryErr != nil {
				_ = st.UpdateDelivery(id, store.DeliveryQueued, deliveryErr.Error(), time.Now().UTC())
				return
			}
			_ = st.UpdateDelivery(id, store.DeliverySteered, "", time.Now().UTC())
		},
	}
}

func recordDaemonFileChanges(st appserverWorkerStore, worker store.Worker, changes []appserver.FileChange, at time.Time) error {
	service := coordination.Service{Store: st, Now: func() time.Time { return at }}
	for i, change := range changes {
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(workerExecutionRoot(worker), path)
		}
		_, err := service.Touch(context.Background(), coordination.TouchRequest{RequestID: fmt.Sprintf("appserver-%s-%s-%03d", worker.ID, worker.TurnID, i+1), WorkerID: worker.ID, Repo: worker.ProjectRoot, Path: filepath.Clean(path), Operation: "write", Intent: "Codex app-server file change"})
		if err != nil {
			return err
		}
	}
	return nil
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
