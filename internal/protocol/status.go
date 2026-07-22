package protocol

import (
	"fmt"
	"time"
)

// Status is the compact operator-facing state returned by the daemon.
type Status struct {
	Daemon                     string `json:"daemon"`
	Version                    string `json:"version"`
	StatePath                  string `json:"state_path"`
	Backend                    string `json:"backend,omitempty"`
	WorkerCount                int    `json:"worker_count"`
	ActiveWorkerCount          int    `json:"active_worker_count,omitempty"`
	LiveMessageWorkers         int    `json:"live_message_workers,omitempty"`
	ResumeWorkers              int    `json:"resume_workers,omitempty"`
	ManagedWorktreeWorkers     int    `json:"managed_worktree_workers,omitempty"`
	AutomaticCompletionWorkers int    `json:"automatic_completion_workers,omitempty"`
	ExternalTrackerWorkers     int    `json:"external_tracker_workers,omitempty"`
	SteerableWorkers           int    `json:"steerable_workers,omitempty"`
	ClaimCount                 int    `json:"claim_count"`
	ActiveClaimCount           int    `json:"active_claim_count,omitempty"`
	ConflictCount              int    `json:"conflict_count"`
	MessageCount               int    `json:"message_count,omitempty"`
	QueuedMessages             int    `json:"queued_messages,omitempty"`
	SteeredMessages            int    `json:"steered_messages,omitempty"`
	DeliveredMessages          int    `json:"delivered_messages,omitempty"`
	RecentTouches              int    `json:"recent_touches,omitempty"`
	ConflictMessages           int    `json:"conflict_messages,omitempty"`
}

// String renders a compact human-readable daemon status line.
func (s Status) String() string {
	base := fmt.Sprintf("daemon=%s version=%s workers=%d claims=%d conflicts=%d state=%s", s.Daemon, s.Version, s.WorkerCount, s.ClaimCount, s.ConflictCount, s.StatePath)
	if s.Backend == "" {
		return base
	}
	return fmt.Sprintf("%s backend=%s active=%d live_message=%d resume=%d managed_worktree=%d automatic_completion=%d external_tracker=%d steerable=%d active_claims=%d messages=%d queued=%d steered=%d delivered=%d touches_30m=%d conflict_messages=%d",
		base, s.Backend, s.ActiveWorkerCount, s.LiveMessageWorkers, s.ResumeWorkers, s.ManagedWorktreeWorkers, s.AutomaticCompletionWorkers, s.ExternalTrackerWorkers, s.SteerableWorkers, s.ActiveClaimCount,
		s.MessageCount, s.QueuedMessages, s.SteeredMessages, s.DeliveredMessages, s.RecentTouches, s.ConflictMessages)
}

// WorkerStatus is the compact daemon representation of one worker.
type WorkerStatus struct {
	ID               string    `json:"id"`
	Status           string    `json:"status"`
	Role             string    `json:"role,omitempty"`
	Issue            string    `json:"issue,omitempty"`
	ValidationOf     string    `json:"validation_of,omitempty"`
	ValidationStatus string    `json:"validation_status,omitempty"`
	Worktree         string    `json:"worktree,omitempty"`
	Repo             string    `json:"repo,omitempty"`
	Engine           string    `json:"engine,omitempty"`
	Capabilities     []string  `json:"capabilities,omitempty"`
	HostID           string    `json:"host_id,omitempty"`
	ThreadID         string    `json:"thread_id,omitempty"`
	TurnID           string    `json:"turn_id,omitempty"`
	RuntimeOwner     string    `json:"runtime_owner,omitempty"`
	Prompt           string    `json:"prompt,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

// WorkersResponse is returned by the daemon workers endpoint.
type WorkersResponse struct {
	Workers []WorkerStatus `json:"workers"`
}

// ClaimsResponse is returned by the daemon claims endpoint.
type ClaimsResponse struct {
	Claims    []Claim         `json:"claims"`
	Conflicts []ClaimConflict `json:"conflicts"`
}

// Claim is the daemon protocol representation of a warning-only ownership claim.
type Claim struct {
	ID             string    `json:"id"`
	WorkerID       string    `json:"worker_id,omitempty"`
	Repo           string    `json:"repo"`
	ScopeKind      string    `json:"scope_kind,omitempty"`
	Scope          string    `json:"scope"`
	Issue          string    `json:"issue,omitempty"`
	Status         string    `json:"status"`
	Note           string    `json:"note,omitempty"`
	ExternalWorker bool      `json:"external_worker,omitempty"`
	WorkerSource   string    `json:"worker_source,omitempty"`
	Blocker        string    `json:"blocker,omitempty"`
	Next           string    `json:"next,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ClaimConflict describes one pair of overlapping open claims.
type ClaimConflict struct {
	ClaimID    string `json:"claim_id"`
	ConflictID string `json:"conflict_id"`
	Repo       string `json:"repo"`
	Scope      string `json:"scope"`
}

// LegacyStatus is the compatibility response for older daemon clients.
type LegacyStatus struct {
	Daemon    string         `json:"daemon"`
	StatePath string         `json:"state_path"`
	Workers   []LegacyWorker `json:"workers"`
}

// LegacyWorker preserves the complete v1 worker response independently of store persistence types.
type LegacyWorker struct {
	ID               string              `json:"id"`
	ParentID         string              `json:"parent_id,omitempty"`
	Role             string              `json:"role,omitempty"`
	Issue            string              `json:"issue,omitempty"`
	ValidationOf     string              `json:"validation_of,omitempty"`
	ValidationStatus string              `json:"validation_status,omitempty"`
	ProjectRoot      string              `json:"project_root"`
	Worktree         string              `json:"worktree"`
	Branch           string              `json:"branch"`
	ThreadID         string              `json:"thread_id"`
	TurnID           string              `json:"turn_id,omitempty"`
	Engine           string              `json:"engine"`
	Status           string              `json:"status"`
	Lifecycle        *LegacyLifecycle    `json:"lifecycle,omitempty"`
	Prompt           string              `json:"prompt"`
	LastMessage      string              `json:"last_message,omitempty"`
	Report           string              `json:"report,omitempty"`
	PullRequests     []LegacyPullRequest `json:"pull_requests,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
	Events           []LegacyEvent       `json:"events,omitempty"`
}

// LegacyLifecycle is the lifecycle shape embedded in a legacy worker response.
type LegacyLifecycle struct {
	Version int                    `json:"version"`
	Session LegacySessionLifecycle `json:"session"`
	Runtime LegacyRuntimeLifecycle `json:"runtime"`
}

// LegacySessionLifecycle is the durable session portion of a legacy lifecycle.
type LegacySessionLifecycle struct {
	State        string     `json:"state"`
	Reason       string     `json:"reason,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	TerminatedAt *time.Time `json:"terminated_at,omitempty"`
}

// LegacyRuntimeLifecycle is the runtime portion of a legacy lifecycle.
type LegacyRuntimeLifecycle struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// LegacyPullRequest is the PR stewardship shape embedded in a legacy worker response.
type LegacyPullRequest struct {
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

// LegacyEvent is the timeline event shape embedded in a legacy worker response.
type LegacyEvent struct {
	At        time.Time `json:"at"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Issue     string    `json:"issue,omitempty"`
	WorkerID  string    `json:"worker,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
}
