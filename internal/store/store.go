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

// WorkerStatus is the legacy display status retained for compatibility.
type WorkerStatus string

const (
	WorkerPending WorkerStatus = "pending"
	WorkerRunning WorkerStatus = "running"
	WorkerIdle    WorkerStatus = "idle"
	WorkerDone    WorkerStatus = "done"
	WorkerFailed  WorkerStatus = "failed"
)

// Worker is a durable local record for one agent or subagent.
type Worker struct {
	ID               string               `json:"id"`
	ParentID         string               `json:"parent_id,omitempty"`
	Role             string               `json:"role,omitempty"`
	Issue            string               `json:"issue,omitempty"`
	ValidationOf     string               `json:"validation_of,omitempty"`
	ValidationStatus string               `json:"validation_status,omitempty"`
	ProjectRoot      string               `json:"project_root"`
	Worktree         string               `json:"worktree"`
	Branch           string               `json:"branch"`
	ThreadID         string               `json:"thread_id"`
	TurnID           string               `json:"turn_id,omitempty"`
	Engine           string               `json:"engine"`
	Status           WorkerStatus         `json:"status"`
	Lifecycle        *lifecycle.Lifecycle `json:"lifecycle,omitempty"`
	Prompt           string               `json:"prompt"`
	LastMessage      string               `json:"last_message,omitempty"`
	Report           string               `json:"report,omitempty"`
	PullRequests     []PullRequestState   `json:"pull_requests,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
	Events           []Event              `json:"events,omitempty"`
}

// ApplyStatus updates the worker lifecycle using the current time semantics.
func (w *Worker) ApplyStatus(status WorkerStatus) {
	w.Status = status
	lc := lifecycleFromWorkerStatusWithPrevious(status, w.Lifecycle)
	w.Lifecycle = &lc
}

// ApplyStatusAt updates status and terminal timestamps at a known time.
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
	return lifecycleFromWorkerStatusWithPrevious(status, nil)
}

func lifecycleFromWorkerStatusWithPrevious(status WorkerStatus, previous *lifecycle.Lifecycle) lifecycle.Lifecycle {
	preserveOrchestrator := previous != nil && previous.Session.Reason == lifecycle.ReasonOrchestrating
	switch status {
	case WorkerRunning:
		if preserveOrchestrator {
			return lifecycle.NewOrchestratorLifecycle()
		}
		return lifecycle.NewWorkerLifecycle()
	case WorkerDone:
		reason := lifecycle.ReasonCompleted
		if preserveOrchestrator {
			reason = lifecycle.ReasonOrchestrating
		}
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State:  lifecycle.SessionDone,
				Reason: reason,
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeStopped,
			},
		}
	case WorkerFailed:
		reason := lifecycle.ReasonFailed
		if preserveOrchestrator {
			reason = lifecycle.ReasonOrchestrating
		}
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State:  lifecycle.SessionFailed,
				Reason: reason,
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeStopped,
			},
		}
	case WorkerIdle:
		reason := lifecycle.Reason("")
		if preserveOrchestrator {
			reason = lifecycle.ReasonOrchestrating
		}
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State:  lifecycle.SessionIdle,
				Reason: reason,
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeStopped,
			},
		}
	default:
		return lifecycle.Lifecycle{
			Version: lifecycle.CurrentVersion,
			Session: lifecycle.SessionLifecycle{
				State:  statusToSessionState(status),
				Reason: previousSessionReason(previous),
			},
			Runtime: lifecycle.RuntimeLifecycle{
				State: lifecycle.RuntimeUnknown,
			},
		}
	}
}

func previousSessionReason(previous *lifecycle.Lifecycle) lifecycle.Reason {
	if previous == nil {
		return ""
	}
	return previous.Session.Reason
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

// Event records one durable swarm timeline entry.
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

// GateEvidence records proof that a named repo quality gate was evaluated.
type GateEvidence struct {
	ID        string    `json:"id"`
	GateID    string    `json:"gate_id"`
	WorkerID  string    `json:"worker_id,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	Scope     string    `json:"scope,omitempty"`
	Command   string    `json:"command"`
	ExitCode  int       `json:"exit_code"`
	Output    string    `json:"output,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// PullRequestState records explicit PR stewardship state attached to a worker.
type PullRequestState struct {
	URL              string    `json:"url"`
	State            string    `json:"state,omitempty"`
	BaseBranch       string    `json:"base_branch,omitempty"`
	HeadBranch       string    `json:"head_branch,omitempty"`
	ReviewDecision   string    `json:"review_decision,omitempty"`
	CheckSummary     string    `json:"check_summary,omitempty"`
	CodeRabbitStatus string    `json:"coderabbit_status,omitempty"`
	NextAction       string    `json:"next_action,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CompletedMutation stores an idempotency replay record.
type CompletedMutation struct {
	RequestID   string    `json:"request_id"`
	Command     string    `json:"command"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Output      string    `json:"output"`
	CreatedAt   time.Time `json:"created_at"`
}

// WorkerMutationResult is the durable output of a multi-worker mutation.
type WorkerMutationResult struct {
	Fingerprint string
	Output      string
	Events      []Event
}

// Schedule describes a recurring prompt against a repository.
type Schedule struct {
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Prompt    string    `json:"prompt"`
	Cron      string    `json:"cron"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ClaimStatus is the state of a warning-only ownership claim.
type ClaimStatus string

const (
	ClaimActive   ClaimStatus = "active"
	ClaimReleased ClaimStatus = "released"
	ClaimBlocked  ClaimStatus = "blocked"
)

// Claim records warning-only ownership of a repo scope.
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

// Agent is a named local agent identity.
type Agent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty"`
	Current   bool      `json:"current,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store is the minimal worker store interface used by simple consumers.
type Store interface {
	SaveWorker(worker Worker) error
	GetWorker(id string) (Worker, error)
	ListWorkers() ([]Worker, error)
}
