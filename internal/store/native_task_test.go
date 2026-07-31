package store

import (
	"strings"
	"testing"
	"time"
)

func TestReserveNativeTaskIsIdempotentAndValidatesParent(t *testing.T) {
	state := NewJSONStore(t.TempDir() + "/state.db")
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	repo := t.TempDir()
	parent := Worker{ID: "parent", ProjectRoot: repo, Engine: "tracker", ThreadID: "parent-thread", Status: WorkerIdle, CreatedAt: now, UpdatedAt: now}
	if err := state.SaveWorker(parent); err != nil {
		t.Fatal(err)
	}
	worker := reservedNativeTaskWorker("worker-1", "parent", repo, now)
	request := NativeTaskReservationRequest{RequestID: "prepare-1", Fingerprint: "fingerprint-1", Worker: worker, At: now}
	first, err := state.ReserveNativeTask(request)
	if err != nil {
		t.Fatalf("ReserveNativeTask() error = %v", err)
	}
	if first.Replayed || first.Worker.ID != worker.ID || first.Worker.Status != WorkerPending {
		t.Fatalf("reservation = %#v", first)
	}

	replayWorker := reservedNativeTaskWorker("worker-2", "parent", repo, now.Add(time.Second))
	replayed, err := state.ReserveNativeTask(NativeTaskReservationRequest{RequestID: request.RequestID, Fingerprint: request.Fingerprint, Worker: replayWorker, At: now.Add(time.Second)})
	if err != nil {
		t.Fatalf("ReserveNativeTask(replay) error = %v", err)
	}
	if !replayed.Replayed || replayed.Worker.ID != worker.ID {
		t.Fatalf("replayed reservation = %#v", replayed)
	}
	if _, err := state.ReserveNativeTask(NativeTaskReservationRequest{RequestID: request.RequestID, Fingerprint: "different", Worker: replayWorker, At: now}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched replay error = %v", err)
	}

	orphan := reservedNativeTaskWorker("orphan", "missing-parent", repo, now)
	if _, err := state.ReserveNativeTask(NativeTaskReservationRequest{RequestID: "prepare-orphan", Fingerprint: "orphan", Worker: orphan, At: now}); err == nil || !strings.Contains(err.Error(), "parent worker not found") {
		t.Fatalf("missing parent error = %v", err)
	}
}

func TestReserveNativeTaskAllowsNonTerminalCrossRepoParent(t *testing.T) {
	state := NewJSONStore(t.TempDir() + "/state.db")
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	parent := Worker{ID: "parent", ProjectRoot: t.TempDir(), Engine: "tracker", ThreadID: "parent-thread", Status: WorkerIdle, CreatedAt: now, UpdatedAt: now}
	if err := state.SaveWorker(parent); err != nil {
		t.Fatal(err)
	}
	worker := reservedNativeTaskWorker("worker-cross-repo", parent.ID, t.TempDir(), now)
	if _, err := state.ReserveNativeTask(NativeTaskReservationRequest{RequestID: "prepare-cross-repo", Fingerprint: "cross-repo", Worker: worker, At: now}); err != nil {
		t.Fatalf("ReserveNativeTask(cross repo) error = %v", err)
	}
}

func TestBindNativeTaskRecordsHostIdentityAndCheckout(t *testing.T) {
	state := NewJSONStore(t.TempDir() + "/state.db")
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	repo := t.TempDir()
	worker := reservedNativeTaskWorker("worker-1", "", repo, now)
	if _, err := state.ReserveNativeTask(NativeTaskReservationRequest{RequestID: "prepare-1", Fingerprint: "prepare-fingerprint", Worker: worker, At: now}); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	request := NativeTaskBindingRequest{
		RequestID: "bind-worker-1", Fingerprint: "bind-fingerprint", WorkerID: worker.ID,
		HostID: "local", ThreadID: "thread-1", TurnID: "turn-1", CWD: worktree, Branch: "codex/task", At: now.Add(time.Second),
	}
	first, err := state.BindNativeTask(request)
	if err != nil {
		t.Fatalf("BindNativeTask() error = %v", err)
	}
	if first.Replayed || first.Worker.Status != WorkerRunning || first.Worker.HostID != "local" || first.Worker.ThreadID != "thread-1" || first.Worker.TurnID != "turn-1" || first.Worker.Worktree != worktree || first.Worker.Branch != "codex/task" {
		t.Fatalf("binding = %#v", first)
	}
	if len(first.Worker.Events) < 2 || first.Worker.Events[len(first.Worker.Events)-1].Type != "native.task.bound" {
		t.Fatalf("binding events = %#v", first.Worker.Events)
	}

	replayed, err := state.BindNativeTask(request)
	if err != nil {
		t.Fatalf("BindNativeTask(replay) error = %v", err)
	}
	if !replayed.Replayed || replayed.Worker.ThreadID != first.Worker.ThreadID {
		t.Fatalf("replayed binding = %#v", replayed)
	}
	request.RequestID = "bind-other"
	request.Fingerprint = "bind-other-fingerprint"
	request.ThreadID = "thread-other"
	if _, err := state.BindNativeTask(request); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("identity replacement error = %v", err)
	}
}

func TestBindNativeTaskLocalCheckoutDoesNotClaimWorktree(t *testing.T) {
	state := NewJSONStore(t.TempDir() + "/state.db")
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	repo := t.TempDir()
	worker := reservedNativeTaskWorker("worker-local", "", repo, now)
	worker.TaskEnvironment = NativeTaskEnvironmentLocal
	if _, err := state.ReserveNativeTask(NativeTaskReservationRequest{RequestID: "prepare-local", Fingerprint: "prepare-local", Worker: worker, At: now}); err != nil {
		t.Fatal(err)
	}
	result, err := state.BindNativeTask(NativeTaskBindingRequest{
		RequestID: "bind-local", Fingerprint: "bind-local", WorkerID: worker.ID,
		HostID: "local", ThreadID: "thread-local", CWD: repo, At: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("BindNativeTask(local) error = %v", err)
	}
	if result.Worker.Worktree != "" || result.Worker.Branch != "" || result.Worker.Status != WorkerIdle {
		t.Fatalf("local binding = %#v", result.Worker)
	}
}

func reservedNativeTaskWorker(id, parent, repo string, now time.Time) Worker {
	worker := Worker{
		ID: id, ParentID: parent, ProjectRoot: repo, Engine: "appserver", RuntimeOwner: RuntimeOwnerExternal,
		TaskEnvironment: NativeTaskEnvironmentWorktree, Status: WorkerPending, Prompt: "do the work", CreatedAt: now, UpdatedAt: now,
		Events: []Event{{At: now, Type: "native.task.reserved", WorkerID: id}},
	}
	worker.ApplyStatusAt(WorkerPending, now)
	return worker
}
