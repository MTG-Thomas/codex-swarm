package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	nativeTaskPrepareCommand = "dispatch.prepare"
	nativeTaskBindCommand    = "dispatch.bind"
)

// NativeTaskReservationRequest creates one externally owned Codex task
// reservation before the host creates the actual thread.
type NativeTaskReservationRequest struct {
	RequestID   string
	Fingerprint string
	Worker      Worker
	At          time.Time
}

// NativeTaskReservationResult is the durable reservation readback.
type NativeTaskReservationResult struct {
	Worker   Worker
	Replayed bool
}

// NativeTaskBindingRequest binds host-owned task identity to a reservation.
type NativeTaskBindingRequest struct {
	RequestID   string
	Fingerprint string
	WorkerID    string
	HostID      string
	ThreadID    string
	TurnID      string
	CWD         string
	Branch      string
	At          time.Time
}

// NativeTaskBindingResult is the durable binding readback.
type NativeTaskBindingResult struct {
	Worker   Worker
	Replayed bool
}

// ReserveNativeTask atomically creates an idempotent pending worker record.
func (s *JSONStore) ReserveNativeTask(request NativeTaskReservationRequest) (NativeTaskReservationResult, error) {
	if strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.Fingerprint) == "" {
		return NativeTaskReservationResult{}, errors.New("native task reservation request id and fingerprint are required")
	}
	worker := request.Worker
	if strings.TrimSpace(worker.ID) == "" || strings.TrimSpace(worker.ProjectRoot) == "" || strings.TrimSpace(worker.Prompt) == "" {
		return NativeTaskReservationResult{}, errors.New("native task reservation worker id, repo, and prompt are required")
	}
	if worker.Engine != "appserver" || worker.RuntimeOwner != RuntimeOwnerExternal {
		return NativeTaskReservationResult{}, errors.New("native task reservation requires an externally owned appserver worker")
	}
	if worker.TaskEnvironment != NativeTaskEnvironmentLocal && worker.TaskEnvironment != NativeTaskEnvironmentWorktree {
		return NativeTaskReservationResult{}, fmt.Errorf("unsupported native task environment %q", worker.TaskEnvironment)
	}
	request.At = request.At.UTC()
	var result NativeTaskReservationResult
	err := s.withStateLock(func() error {
		completed, err := completedMutationsForUpdate(s)
		if err != nil {
			return err
		}
		for _, mutation := range completed {
			if mutation.RequestID != request.RequestID || mutation.Command != nativeTaskPrepareCommand {
				continue
			}
			if mutation.Fingerprint != request.Fingerprint {
				return fmt.Errorf("request %q for %s does not match original mutation fingerprint", request.RequestID, nativeTaskPrepareCommand)
			}
			reserved, found, err := getRecord[Worker](s.tx, "worker", mutation.Output)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%w: %s", ErrWorkerNotFound, mutation.Output)
			}
			normalizeWorkerLifecycleForRead(&reserved)
			result = NativeTaskReservationResult{Worker: reserved, Replayed: true}
			return nil
		}

		if _, found, err := getRecord[Worker](s.tx, "worker", worker.ID); err != nil {
			return err
		} else if found {
			return fmt.Errorf("worker %q already exists", worker.ID)
		}
		if worker.ParentID != "" {
			parent, found, err := getRecord[Worker](s.tx, "worker", worker.ParentID)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("parent %w: %s", ErrWorkerNotFound, worker.ParentID)
			}
			normalizeWorkerLifecycleForRead(&parent)
			if parent.Status == WorkerDone || parent.Status == WorkerFailed {
				return fmt.Errorf("parent worker %s is terminal with status %s", parent.ID, parent.Status)
			}
		}
		normalizeWorkerLifecycleForSave(&worker)
		if err := s.upsert("worker", worker.ID, worker); err != nil {
			return err
		}
		completed = appendBoundedCompletedMutations(completed, CompletedMutation{
			RequestID: request.RequestID, Command: nativeTaskPrepareCommand, Fingerprint: request.Fingerprint,
			Output: worker.ID, CreatedAt: request.At,
		}, CompletedMutationCacheCap)
		if err := s.upsert("completed_mutations", "singleton", completed); err != nil {
			return err
		}
		result.Worker = worker
		return nil
	})
	return result, err
}

// BindNativeTask atomically records the host, thread, turn, and host-created
// checkout for a prior reservation.
func (s *JSONStore) BindNativeTask(request NativeTaskBindingRequest) (NativeTaskBindingResult, error) {
	if strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.Fingerprint) == "" {
		return NativeTaskBindingResult{}, errors.New("native task binding request id and fingerprint are required")
	}
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	request.HostID = strings.TrimSpace(request.HostID)
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	request.TurnID = strings.TrimSpace(request.TurnID)
	request.CWD = strings.TrimSpace(request.CWD)
	request.Branch = strings.TrimSpace(request.Branch)
	if request.WorkerID == "" || request.HostID == "" || request.ThreadID == "" {
		return NativeTaskBindingResult{}, errors.New("native task binding worker, host, and thread are required")
	}
	request.At = request.At.UTC()
	var result NativeTaskBindingResult
	err := s.withStateLock(func() error {
		completed, err := completedMutationsForUpdate(s)
		if err != nil {
			return err
		}
		for _, mutation := range completed {
			if mutation.RequestID != request.RequestID || mutation.Command != nativeTaskBindCommand {
				continue
			}
			if mutation.Fingerprint != request.Fingerprint {
				return fmt.Errorf("request %q for %s does not match original mutation fingerprint", request.RequestID, nativeTaskBindCommand)
			}
			bound, found, err := getRecord[Worker](s.tx, "worker", mutation.Output)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%w: %s", ErrWorkerNotFound, mutation.Output)
			}
			normalizeWorkerLifecycleForRead(&bound)
			result = NativeTaskBindingResult{Worker: bound, Replayed: true}
			return nil
		}

		worker, found, err := getRecord[Worker](s.tx, "worker", request.WorkerID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: %s", ErrWorkerNotFound, request.WorkerID)
		}
		normalizeWorkerLifecycleForRead(&worker)
		if worker.Engine != "appserver" || worker.RuntimeOwner != RuntimeOwnerExternal || worker.TaskEnvironment == "" {
			return fmt.Errorf("worker %s is not a native task reservation", worker.ID)
		}
		if worker.Status == WorkerDone || worker.Status == WorkerFailed {
			return fmt.Errorf("worker %s is terminal with status %s", worker.ID, worker.Status)
		}
		if worker.ThreadID != "" && (worker.HostID != request.HostID || worker.ThreadID != request.ThreadID || worker.TurnID != request.TurnID || worker.Worktree != request.CWD || worker.Branch != request.Branch) {
			return fmt.Errorf("worker %s is already bound to host=%s thread=%s turn=%s worktree=%s branch=%s", worker.ID, worker.HostID, worker.ThreadID, worker.TurnID, worker.Worktree, worker.Branch)
		}
		if worker.TaskEnvironment == NativeTaskEnvironmentWorktree && request.CWD == "" {
			return fmt.Errorf("worker %s requested a worktree; binding requires cwd", worker.ID)
		}
		if worker.TaskEnvironment == NativeTaskEnvironmentLocal && request.Branch != "" {
			return fmt.Errorf("worker %s requested the local checkout; binding cannot set branch", worker.ID)
		}

		worker.HostID = request.HostID
		worker.ThreadID = request.ThreadID
		worker.TurnID = request.TurnID
		if worker.TaskEnvironment == NativeTaskEnvironmentWorktree {
			worker.Worktree = request.CWD
			worker.Branch = request.Branch
		}
		if worker.TurnID != "" {
			worker.ApplyStatusAt(WorkerRunning, request.At)
		} else {
			worker.ApplyStatusAt(WorkerIdle, request.At)
		}
		worker.LastMessage = fmt.Sprintf("native task bound: host=%s thread=%s turn=%s cwd=%s", worker.HostID, worker.ThreadID, valueOrDash(worker.TurnID), valueOrDash(request.CWD))
		worker.Events = append(worker.Events, Event{At: request.At, Type: "native.task.bound", Message: worker.LastMessage, WorkerID: worker.ID, Issue: worker.Issue, RequestID: request.RequestID})
		worker.UpdatedAt = request.At
		if err := s.upsert("worker", worker.ID, worker); err != nil {
			return err
		}
		completed = appendBoundedCompletedMutations(completed, CompletedMutation{
			RequestID: request.RequestID, Command: nativeTaskBindCommand, Fingerprint: request.Fingerprint,
			Output: worker.ID, CreatedAt: request.At,
		}, CompletedMutationCacheCap)
		if err := s.upsert("completed_mutations", "singleton", completed); err != nil {
			return err
		}
		result.Worker = worker
		return nil
	})
	return result, err
}

func completedMutationsForUpdate(s *JSONStore) ([]CompletedMutation, error) {
	completed, found, err := getRecord[[]CompletedMutation](s.tx, "completed_mutations", "singleton")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return completed, nil
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
