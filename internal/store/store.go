package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
)

var (
	ErrWorkerNotFound           = errors.New("worker not found")
	ErrClaimNotFound            = errors.New("claim not found")
	ErrAgentNotFound            = errors.New("agent not found")
	ErrTraceNotFound            = errors.New("trace lane not found")
	ErrBifrostChangesetNotFound = errors.New("bifrost changeset not found")
	ErrMessageNotFound          = errors.New("message not found")
	ErrMessageReplayMismatch    = errors.New("message request replay mismatch")
	ErrCodexTaskReplayMismatch  = errors.New("codex task snapshot request replay mismatch")
	ErrDecisionNotFound         = errors.New("decision not found")
	ErrDecisionReplayMismatch   = errors.New("decision request replay mismatch")
	ErrDecisionSuperseded       = errors.New("decision already superseded")
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
	HostID           string               `json:"host_id,omitempty"`
	Engine           string               `json:"engine"`
	RuntimeOwner     RuntimeOwner         `json:"runtime_owner,omitempty"`
	Remote           *RemoteExecution     `json:"remote,omitempty"`
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

// RuntimeOwner identifies which process owns a worker's live app-server
// connection. An external owner requires Codex-hosted native steering; cs-owned
// workers consume their durable queue over the connection cs already owns.
type RuntimeOwner string

const (
	RuntimeOwnerCS       RuntimeOwner = "cs"
	RuntimeOwnerExternal RuntimeOwner = "external"
)

// RemoteExecution identifies the SSH transport and isolated Git workspace for
// an app-server worker. It contains no credentials.
type RemoteExecution struct {
	Host        string `json:"host"`
	JumpHost    string `json:"jump_host,omitempty"`
	CodexBinary string `json:"codex_binary,omitempty"`
	RepoURL     string `json:"repo_url"`
	BaseRef     string `json:"base_ref"`
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

// MessageKind is the intentionally small coordination vocabulary.
type MessageKind string

const (
	MessageDirect     MessageKind = "dm"
	MessageSubtree    MessageKind = "subtree"
	MessageConflict   MessageKind = "conflict"
	MessageCompletion MessageKind = "completion"
)

// DeliveryState records how a durable message reached a worker.
type DeliveryState string

const (
	DeliveryQueued    DeliveryState = "queued"
	DeliverySteered   DeliveryState = "steered"
	DeliveryDelivered DeliveryState = "delivered"
)

// Message is one immutable coordination message.
type Message struct {
	ID        string      `json:"id"`
	RequestID string      `json:"request_id"`
	Kind      MessageKind `json:"kind"`
	From      string      `json:"from"`
	Body      string      `json:"body"`
	CreatedAt time.Time   `json:"created_at"`
}

// Delivery is the per-recipient state for a message.
type Delivery struct {
	ID          string          `json:"id"`
	MessageID   string          `json:"message_id"`
	RecipientID string          `json:"recipient_id"`
	State       DeliveryState   `json:"state"`
	LastError   string          `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	History     []DeliveryEvent `json:"history,omitempty"`
}

// DeliveryEvent is one durable delivery-state observation. Repeated updates
// with the same state and error are intentionally suppressed.
type DeliveryEvent struct {
	Sequence   int64         `json:"sequence"`
	DeliveryID string        `json:"delivery_id"`
	State      DeliveryState `json:"state"`
	LastError  string        `json:"last_error,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
}

// DeliveredMessage joins a message with its recipient delivery state.
type DeliveredMessage struct {
	Message  Message  `json:"message"`
	Delivery Delivery `json:"delivery"`
}

// NativeSteeringRequest is a durable delivery that must be injected by the
// Codex host which owns the destination task's active connection.
type NativeSteeringRequest struct {
	DeliveryID  string `json:"delivery_id"`
	MessageID   string `json:"message_id"`
	RecipientID string `json:"recipient_id"`
	StatePath   string `json:"state_path"`
	HostID      string `json:"host_id,omitempty"`
	ThreadID    string `json:"thread_id"`
	TurnID      string `json:"turn_id"`
	Prompt      string `json:"prompt"`
}

// NativeFollowupRequest is a durable delivery that must start a new turn in
// the destination task through the Codex host which owns that task.
type NativeFollowupRequest struct {
	DeliveryID  string `json:"delivery_id"`
	MessageID   string `json:"message_id"`
	RecipientID string `json:"recipient_id"`
	StatePath   string `json:"state_path"`
	HostID      string `json:"host_id,omitempty"`
	ThreadID    string `json:"thread_id"`
	Prompt      string `json:"prompt"`
}

// CoordinationMetrics summarizes whether durable coordination is actually in use.
type CoordinationMetrics struct {
	Backend                    string `json:"backend"`
	WorkerCount                int    `json:"worker_count"`
	ActiveWorkers              int    `json:"active_workers"`
	LiveMessageWorkers         int    `json:"live_message_workers"`
	ResumeWorkers              int    `json:"resume_workers"`
	ManagedWorktreeWorkers     int    `json:"managed_worktree_workers"`
	AutomaticCompletionWorkers int    `json:"automatic_completion_workers"`
	ExternalTrackerWorkers     int    `json:"external_tracker_workers"`
	SteerableWorkers           int    `json:"steerable_workers"`
	ClaimCount                 int    `json:"claim_count"`
	ActiveClaims               int    `json:"active_claims"`
	MessageCount               int    `json:"message_count"`
	QueuedMessages             int    `json:"queued_messages"`
	SteeredMessages            int    `json:"steered_messages"`
	DeliveredMessages          int    `json:"delivered_messages"`
	RecentTouches              int    `json:"recent_touches"`
	ConflictMessages           int    `json:"conflict_messages"`
}

// CoordinationSnapshot is one transactionally consistent read of the record
// kinds used by broad derived projections such as logical operations.
type CoordinationSnapshot struct {
	Workers      []Worker
	Claims       []Claim
	Messages     []DeliveredMessage
	GateEvidence []GateEvidence
	CodexTasks   []CodexTask
}

// FileTouch is a recent worker read or write intent used for warning-only conflicts.
type FileTouch struct {
	ID        string    `json:"id"`
	WorkerID  string    `json:"worker_id"`
	Repo      string    `json:"repo"`
	Path      string    `json:"path"`
	Operation string    `json:"operation"`
	LineStart int       `json:"line_start,omitempty"`
	LineEnd   int       `json:"line_end,omitempty"`
	Intent    string    `json:"intent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TouchConflict describes a high-confidence overlapping peer write.
type TouchConflict struct {
	Touch     FileTouch `json:"touch"`
	PeerTouch FileTouch `json:"peer_touch"`
}

// TraceItem records one nested task frame in a per-agent trace stack.
type TraceItem struct {
	Title     string    `json:"title"`
	Key       string    `json:"key,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// TraceEvent records a durable trace transition for audit and note export.
type TraceEvent struct {
	At      time.Time `json:"at"`
	Type    string    `json:"type"`
	Title   string    `json:"title,omitempty"`
	Message string    `json:"message,omitempty"`
	Key     string    `json:"key,omitempty"`
	Depth   int       `json:"depth"`
}

// TraceLane is a detour-style nested execution stack for one local agent.
type TraceLane struct {
	Agent     string       `json:"agent"`
	Stack     []TraceItem  `json:"stack,omitempty"`
	Events    []TraceEvent `json:"events,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
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

// DecisionEvidence is a bounded reference to evidence used by a decision.
// State records whether the reference resolved in the local coordination
// ledger when the decision was written; external references are not fetched.
type DecisionEvidence struct {
	Ref    string `json:"ref"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

const (
	DecisionEvidenceAvailable = "available"
	DecisionEvidenceMissing   = "missing"
	DecisionEvidenceExternal  = "external"
)

// Decision is one immutable operator decision plus supersession metadata.
// Operation is the stable derived key from internal/operation when known.
type Decision struct {
	ID                 string             `json:"id"`
	RequestID          string             `json:"request_id"`
	Operation          string             `json:"operation,omitempty"`
	Repo               string             `json:"repo,omitempty"`
	Issue              string             `json:"issue,omitempty"`
	Summary            string             `json:"summary"`
	Rationale          string             `json:"rationale"`
	Evidence           []DecisionEvidence `json:"evidence,omitempty"`
	Dissent            string             `json:"dissent,omitempty"`
	AuthorWorker       string             `json:"author_worker"`
	ProvenanceGaps     []string           `json:"provenance_gaps,omitempty"`
	SupersedesID       string             `json:"supersedes_id,omitempty"`
	SupersededByID     string             `json:"superseded_by_id,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	SupersededAt       *time.Time         `json:"superseded_at,omitempty"`
	RequestFingerprint string             `json:"-"`
}

// Current reports whether the decision has not been superseded.
func (d Decision) Current() bool { return d.SupersededByID == "" }

// DecisionListFilter selects decision history without changing it.
type DecisionListFilter struct {
	Operation   string
	Repo        string
	Issue       string
	CurrentOnly bool
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

// ClaimScopeKind distinguishes repository paths from logical tasks and live resources.
type ClaimScopeKind string

const (
	ClaimScopePath ClaimScopeKind = "path"
	ClaimScopeTask ClaimScopeKind = "task"
	ClaimScopeLive ClaimScopeKind = "live"
)

// Claim records warning-only ownership of a repo scope.
type Claim struct {
	ID             string         `json:"id"`
	WorkerID       string         `json:"worker_id,omitempty"`
	Repo           string         `json:"repo"`
	ScopeKind      ClaimScopeKind `json:"scope_kind,omitempty"`
	Scope          string         `json:"scope"`
	Issue          string         `json:"issue,omitempty"`
	Status         ClaimStatus    `json:"status"`
	Note           string         `json:"note,omitempty"`
	ExternalWorker bool           `json:"external_worker,omitempty"`
	WorkerSource   string         `json:"worker_source,omitempty"`
	Blocker        string         `json:"blocker,omitempty"`
	Next           string         `json:"next,omitempty"`
	ExpiresAt      time.Time      `json:"expires_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
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

// BifrostChangeset records one remotely coordinated Bifrost workspace change.
type BifrostChangeset struct {
	ID                string          `json:"id"`
	WorkerID          string          `json:"worker_id"`
	Target            string          `json:"target"`
	Scope             string          `json:"scope"`
	BaseRevision      string          `json:"base_revision,omitempty"`
	RemoteChangesetID string          `json:"remote_changeset_id,omitempty"`
	State             string          `json:"state"`
	Validation        json.RawMessage `json:"validation,omitempty"`
	CommitSHA         string          `json:"commit_sha,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// Store is the minimal worker store interface used by simple consumers.
type Store interface {
	SaveWorker(worker Worker) error
	GetWorker(id string) (Worker, error)
	ListWorkers() ([]Worker, error)
	SaveBifrostChangeset(changeset BifrostChangeset) error
	UpdateBifrostChangeset(id string, mutate func(*BifrostChangeset) error) (BifrostChangeset, error)
	DeleteBifrostChangeset(id string) error
	GetBifrostChangeset(id string) (BifrostChangeset, error)
	ListBifrostChangesets() ([]BifrostChangeset, error)
}
