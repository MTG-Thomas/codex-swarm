package store

import (
	"errors"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
)

var (
	ErrWorkerNotFound = errors.New("worker not found")
	ErrClaimNotFound  = errors.New("claim not found")
	ErrAgentNotFound  = errors.New("agent not found")
)

const (
	SwarmEventCap             = 500
	CompletedMutationCacheCap = 500
)

type WorkerStatus string

const (
	WorkerPending WorkerStatus = "pending"
	WorkerRunning WorkerStatus = "running"
	WorkerIdle    WorkerStatus = "idle"
	WorkerDone    WorkerStatus = "done"
	WorkerFailed  WorkerStatus = "failed"
)

type Worker struct {
	ID          string               `json:"id"`
	ParentID    string               `json:"parent_id,omitempty"`
	Role        string               `json:"role,omitempty"`
	Issue       string               `json:"issue,omitempty"`
	ProjectRoot string               `json:"project_root"`
	Worktree    string               `json:"worktree"`
	Branch      string               `json:"branch"`
	ThreadID    string               `json:"thread_id"`
	TurnID      string               `json:"turn_id,omitempty"`
	Engine      string               `json:"engine"`
	Status      WorkerStatus         `json:"status"`
	Lifecycle   *lifecycle.Lifecycle `json:"lifecycle,omitempty"`
	Prompt      string               `json:"prompt"`
	LastMessage string               `json:"last_message,omitempty"`
	Report      string               `json:"report,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Events      []Event              `json:"events,omitempty"`
}

func (w *Worker) ApplyStatus(status WorkerStatus) {
	w.Status = status
	lc := lifecycleFromWorkerStatus(status)
	w.Lifecycle = &lc
}

func (w *Worker) ApplyStatusAt(status WorkerStatus, at time.Time) {
	w.ApplyStatus(status)
	if w.Lifecycle == nil {
		return
	}
	switch status {
	case WorkerDone:
		w.Lifecycle.Session.CompletedAt = &at
		w.Lifecycle.Session.TerminatedAt = nil
	case WorkerFailed:
		w.Lifecycle.Session.CompletedAt = nil
		w.Lifecycle.Session.TerminatedAt = &at
	default:
		w.Lifecycle.ClearTerminalMarkersForNonTerminal()
	}
}

func lifecycleFromWorkerStatus(status WorkerStatus) lifecycle.Lifecycle {
	switch status {
	case WorkerRunning:
		return lifecycle.NewWorkerLifecycle()
	case WorkerDone:
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State:  lifecycle.SessionDone,
				Reason: lifecycle.ReasonCompleted,
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeStopped,
			},
		}
	case WorkerFailed:
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State:  lifecycle.SessionFailed,
				Reason: lifecycle.ReasonFailed,
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeStopped,
			},
		}
	case WorkerIdle:
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State: lifecycle.SessionIdle,
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeStopped,
			},
		}
	default:
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State: statusToSessionState(status),
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeUnknown,
			},
		}
	}
}

func statusToSessionState(status WorkerStatus) lifecycle.SessionState {
	switch status {
	case WorkerPending:
		return lifecycle.SessionPending
	case WorkerIdle:
		return lifecycle.SessionIdle
	default:
		return lifecycle.SessionPending
	}
}

func workerStatusFromLifecycle(lc lifecycle.Lifecycle) WorkerStatus {
	switch lc.DeriveStatus() {
	case lifecycle.DisplayDone:
		return WorkerDone
	case lifecycle.DisplayFailed:
		return WorkerFailed
	case lifecycle.DisplayIdle:
		return WorkerIdle
	case lifecycle.DisplayStale:
		// There is no legacy stale status. Running is the least misleading
		// fallback because stale represents interrupted work, not completion.
		return WorkerRunning
	case lifecycle.DisplayWorking:
		return WorkerRunning
	default:
		return WorkerPending
	}
}

type Event struct {
	At        time.Time `json:"at"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Issue     string    `json:"issue,omitempty"`
	WorkerID  string    `json:"worker,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
}

type CompletedMutation struct {
	RequestID   string    `json:"request_id"`
	Command     string    `json:"command"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Output      string    `json:"output"`
	CreatedAt   time.Time `json:"created_at"`
}

type WorkerMutationResult struct {
	Fingerprint string
	Output      string
	Events      []Event
}

type Schedule struct {
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Prompt    string    `json:"prompt"`
	Cron      string    `json:"cron"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ClaimStatus string

const (
	ClaimActive   ClaimStatus = "active"
	ClaimReleased ClaimStatus = "released"
	ClaimBlocked  ClaimStatus = "blocked"
)

type Claim struct {
	ID             string      `json:"id"`
	WorkerID       string      `json:"worker_id,omitempty"`
	Repo           string      `json:"repo"`
	Scope          string      `json:"scope"`
	Issue          string      `json:"issue,omitempty"`
	Status         ClaimStatus `json:"status"`
	Note           string      `json:"note,omitempty"`
	ExternalWorker bool        `json:"external_worker,omitempty"`
	WorkerSource   string      `json:"worker_source,omitempty"`
	Blocker        string      `json:"blocker,omitempty"`
	Next           string      `json:"next,omitempty"`
	ExpiresAt      time.Time   `json:"expires_at"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type Agent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	Current   bool      `json:"current,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store interface {
	SaveWorker(worker Worker) error
	GetWorker(id string) (Worker, error)
	ListWorkers() ([]Worker, error)
}
