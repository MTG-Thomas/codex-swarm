package lifecycle

import "time"

const CurrentVersion = 1

type SessionState string

const (
	SessionPending SessionState = "pending"
	SessionIdle    SessionState = "idle"
	SessionWorking SessionState = "working"
	SessionDone    SessionState = "done"
	SessionFailed  SessionState = "failed"
)

type RuntimeState string

const (
	RuntimeUnknown RuntimeState = "unknown"
	RuntimeLive    RuntimeState = "live"
	RuntimeStopped RuntimeState = "stopped"
	RuntimeDead    RuntimeState = "dead"
)

type Reason string

const (
	ReasonTaskInProgress Reason = "task_in_progress"
	ReasonRuntimeLost    Reason = "runtime_lost"
	ReasonCompleted      Reason = "completed"
	ReasonFailed         Reason = "failed"
	ReasonRestored       Reason = "restored"
	ReasonOrchestrating  Reason = "orchestrating"
)

type DisplayStatus string

const (
	DisplayPending DisplayStatus = "pending"
	DisplayIdle    DisplayStatus = "idle"
	DisplayWorking DisplayStatus = "working"
	DisplayStale   DisplayStatus = "stale"
	DisplayDone    DisplayStatus = "done"
	DisplayFailed  DisplayStatus = "failed"
)

type Lifecycle struct {
	Version int              `json:"version"`
	Session SessionLifecycle `json:"session"`
	Runtime RuntimeLifecycle `json:"runtime"`
}

type SessionLifecycle struct {
	State        SessionState `json:"state"`
	Reason       Reason       `json:"reason,omitempty"`
	CompletedAt  *time.Time   `json:"completed_at,omitempty"`
	TerminatedAt *time.Time   `json:"terminated_at,omitempty"`
}

type RuntimeLifecycle struct {
	State  RuntimeState `json:"state"`
	Reason Reason       `json:"reason,omitempty"`
}

func NewWorkerLifecycle() Lifecycle {
	return Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State:  SessionWorking,
			Reason: ReasonTaskInProgress,
		},
		Runtime: RuntimeLifecycle{
			State: RuntimeLive,
		},
	}
}

func NewOrchestratorLifecycle() Lifecycle {
	return Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State:  SessionWorking,
			Reason: ReasonOrchestrating,
		},
		Runtime: RuntimeLifecycle{
			State: RuntimeLive,
		},
	}
}

func (l Lifecycle) DeriveStatus() DisplayStatus {
	if l.IsTerminal() {
		if l.Session.State == SessionFailed {
			return DisplayFailed
		}
		return DisplayDone
	}
	if l.Runtime.State == RuntimeDead && l.Runtime.Reason == ReasonRuntimeLost {
		return DisplayStale
	}
	switch l.Session.State {
	case SessionIdle:
		return DisplayIdle
	case SessionWorking:
		return DisplayWorking
	case SessionDone:
		return DisplayDone
	case SessionFailed:
		return DisplayFailed
	default:
		return DisplayPending
	}
}

func (l Lifecycle) IsTerminal() bool {
	return l.Session.State == SessionDone || l.Session.State == SessionFailed
}

func (l *Lifecycle) ClearTerminalMarkersForNonTerminal() {
	if l == nil || l.IsTerminal() {
		return
	}
	l.Session.CompletedAt = nil
	l.Session.TerminatedAt = nil
}
