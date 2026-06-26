package lifecycle

import (
	"testing"
	"time"
)

func TestDeriveStatusWorkingTaskInProgress(t *testing.T) {
	lc := Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State:  SessionWorking,
			Reason: ReasonTaskInProgress,
		},
		Runtime: RuntimeLifecycle{
			State: RuntimeLive,
		},
	}

	if got := lc.DeriveStatus(); got != DisplayWorking {
		t.Fatalf("DeriveStatus() = %q, want %q", got, DisplayWorking)
	}
}

func TestDeriveStatusDeadRuntimeLostStale(t *testing.T) {
	lc := Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State:  SessionWorking,
			Reason: ReasonTaskInProgress,
		},
		Runtime: RuntimeLifecycle{
			State:  RuntimeDead,
			Reason: ReasonRuntimeLost,
		},
	}

	if got := lc.DeriveStatus(); got != DisplayStale {
		t.Fatalf("DeriveStatus() = %q, want %q", got, DisplayStale)
	}
}

func TestDeriveStatusTerminalOverridesRuntimeLost(t *testing.T) {
	tests := []struct {
		name string
		lc   Lifecycle
		want DisplayStatus
	}{
		{
			name: "completed",
			lc: Lifecycle{
				Version: CurrentVersion,
				Session: SessionLifecycle{
					State:  SessionDone,
					Reason: ReasonCompleted,
				},
				Runtime: RuntimeLifecycle{
					State:  RuntimeDead,
					Reason: ReasonRuntimeLost,
				},
			},
			want: DisplayDone,
		},
		{
			name: "failed",
			lc: Lifecycle{
				Version: CurrentVersion,
				Session: SessionLifecycle{
					State:  SessionFailed,
					Reason: ReasonFailed,
				},
				Runtime: RuntimeLifecycle{
					State:  RuntimeDead,
					Reason: ReasonRuntimeLost,
				},
			},
			want: DisplayFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lc.DeriveStatus(); got != tt.want {
				t.Fatalf("DeriveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveStatusDoneWithoutReasonOverridesRuntimeLost(t *testing.T) {
	lc := Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State: SessionDone,
		},
		Runtime: RuntimeLifecycle{
			State:  RuntimeDead,
			Reason: ReasonRuntimeLost,
		},
	}

	if !lc.IsTerminal() {
		t.Fatal("IsTerminal() = false, want true")
	}
	if got := lc.DeriveStatus(); got != DisplayDone {
		t.Fatalf("DeriveStatus() = %q, want %q", got, DisplayDone)
	}
}

func TestDeriveStatusIdle(t *testing.T) {
	lc := Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State: SessionIdle,
		},
		Runtime: RuntimeLifecycle{
			State: RuntimeStopped,
		},
	}

	if got := lc.DeriveStatus(); got != DisplayIdle {
		t.Fatalf("DeriveStatus() = %q, want %q", got, DisplayIdle)
	}
}

func TestIsTerminalDoneCompleted(t *testing.T) {
	lc := Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State:  SessionDone,
			Reason: ReasonCompleted,
		},
		Runtime: RuntimeLifecycle{
			State: RuntimeStopped,
		},
	}

	if !lc.IsTerminal() {
		t.Fatal("IsTerminal() = false, want true")
	}
	if got := lc.DeriveStatus(); got != DisplayDone {
		t.Fatalf("DeriveStatus() = %q, want %q", got, DisplayDone)
	}
}

func TestClearTerminalMarkersForNonTerminal(t *testing.T) {
	completedAt := time.Date(2026, 6, 26, 1, 0, 0, 0, time.UTC)
	terminatedAt := time.Date(2026, 6, 26, 1, 5, 0, 0, time.UTC)
	lc := Lifecycle{
		Version: CurrentVersion,
		Session: SessionLifecycle{
			State:        SessionDone,
			Reason:       ReasonCompleted,
			CompletedAt:  &completedAt,
			TerminatedAt: &terminatedAt,
		},
		Runtime: RuntimeLifecycle{
			State: RuntimeStopped,
		},
	}

	lc.Session.State = SessionWorking
	lc.Session.Reason = ReasonRestored
	lc.Runtime.State = RuntimeLive
	lc.ClearTerminalMarkersForNonTerminal()

	if lc.Session.CompletedAt != nil {
		t.Fatalf("CompletedAt = %v, want nil", lc.Session.CompletedAt)
	}
	if lc.Session.TerminatedAt != nil {
		t.Fatalf("TerminatedAt = %v, want nil", lc.Session.TerminatedAt)
	}
}

func TestConstructorsDefaultLifecycleVersion(t *testing.T) {
	worker := NewWorkerLifecycle()
	orchestrator := NewOrchestratorLifecycle()

	if worker.Version != CurrentVersion || orchestrator.Version != CurrentVersion {
		t.Fatalf("versions = worker:%d orchestrator:%d, want %d", worker.Version, orchestrator.Version, CurrentVersion)
	}
	if worker.Session.State != SessionWorking || worker.Session.Reason != ReasonTaskInProgress {
		t.Fatalf("worker session = %#v, want working task_in_progress", worker.Session)
	}
	if orchestrator.Session.State != SessionWorking || orchestrator.Session.Reason != ReasonOrchestrating {
		t.Fatalf("orchestrator session = %#v, want working orchestrating", orchestrator.Session)
	}
}
