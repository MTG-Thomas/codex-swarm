package coordination

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type recordingSteerer struct {
	calls []steerCall
	err   error
}

func TestSendSteeringFailureRemainsQueuedWithDurableErrorHistory(t *testing.T) {
	st := testStore(t)
	at := time.Date(2026, 7, 22, 14, 0, 0, 0, time.UTC)
	saveWorkers(t, st,
		store.Worker{ID: "sender", Engine: "mock", Status: store.WorkerIdle, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "recipient", Engine: "appserver", Status: store.WorkerRunning, ThreadID: "thread-1", TurnID: "turn-stale", ProjectRoot: t.TempDir(), CreatedAt: at, UpdatedAt: at},
	)
	steerer := &recordingSteerer{err: errors.New("stale turn")}
	result, err := (Service{Store: st, Steerer: steerer, Now: func() time.Time { return at }}).Send(context.Background(), SendRequest{
		RequestID: "stale-1", Kind: store.MessageDirect, From: "sender", To: "recipient", Body: "nonce",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deliveries) != 1 || result.Deliveries[0].State != store.DeliveryQueued || result.Deliveries[0].LastError != "stale turn" {
		t.Fatalf("send result = %#v", result)
	}
	inbox, err := st.ListMessages("recipient")
	if err != nil {
		t.Fatal(err)
	}
	history := inbox[0].Delivery.History
	if len(history) != 2 || history[0].State != store.DeliveryQueued || history[1].State != store.DeliveryQueued || history[1].LastError != "stale turn" {
		t.Fatalf("delivery history = %#v", history)
	}
}

type steerCall struct {
	cwd, thread, turn, message string
}

func (s *recordingSteerer) SteerTurn(_ context.Context, cwd, thread, turn, message string) error {
	s.calls = append(s.calls, steerCall{cwd: cwd, thread: thread, turn: turn, message: message})
	return s.err
}

func TestSendSubtreeSteersActiveWorkerAndQueuesIdleDescendant(t *testing.T) {
	st := testStore(t)
	at := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	saveWorkers(t, st,
		store.Worker{ID: "parent", Engine: "mock", Status: store.WorkerIdle, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "active", ParentID: "parent", Engine: "appserver", Status: store.WorkerRunning, ThreadID: "thread-1", TurnID: "turn-1", ProjectRoot: `C:\repo`, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "idle", ParentID: "active", Engine: "appserver", Status: store.WorkerIdle, ThreadID: "thread-2", ProjectRoot: `C:\repo`, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "future", ParentID: "active", Engine: "future-engine", Status: store.WorkerRunning, ThreadID: "thread-3", TurnID: "turn-3", ProjectRoot: `C:\repo`, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "sender", Engine: "mock", Status: store.WorkerIdle, CreatedAt: at, UpdatedAt: at},
	)
	steerer := &recordingSteerer{}
	service := Service{Store: st, Steerer: steerer, Now: func() time.Time { return at }}
	result, err := service.Send(context.Background(), SendRequest{RequestID: "r-1", Kind: store.MessageSubtree, From: "sender", To: "active", Body: "coordinate now"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(result.Deliveries) != 3 || len(steerer.calls) != 1 {
		t.Fatalf("deliveries=%#v steer calls=%#v", result.Deliveries, steerer.calls)
	}
	states := map[string]store.DeliveryState{}
	for _, delivery := range result.Deliveries {
		states[delivery.RecipientID] = delivery.State
	}
	if states["active"] != store.DeliverySteered || states["idle"] != store.DeliveryQueued || states["future"] != store.DeliveryQueued {
		t.Fatalf("delivery states = %#v", states)
	}
}

func TestSendPreservesAttachedWorkerRuntimeIdentity(t *testing.T) {
	st := testStore(t)
	at := time.Date(2026, 7, 22, 14, 15, 0, 0, time.UTC)
	root := t.TempDir()
	worktree := t.TempDir()
	saveWorkers(t, st,
		store.Worker{ID: "sender", Engine: "tracker", Status: store.WorkerIdle, ProjectRoot: root, CreatedAt: at, UpdatedAt: at},
		store.Worker{
			ID: "recipient", Role: "operator", Engine: "appserver", Status: store.WorkerRunning,
			ThreadID: "thread-live", TurnID: "turn-live", ProjectRoot: root, Worktree: worktree, Branch: "codex/live",
			Remote:    &store.RemoteExecution{Host: "agent@example", JumpHost: "jump@example", CodexBinary: "/opt/codex", RepoURL: "git@example/repo", BaseRef: "main"},
			CreatedAt: at, UpdatedAt: at,
		},
	)
	steerer := &recordingSteerer{}
	result, err := (Service{Store: st, Steerer: steerer, Now: func() time.Time { return at.Add(time.Second) }}).Send(context.Background(), SendRequest{
		RequestID: "preserve-runtime", Kind: store.MessageDirect, From: "sender", To: "recipient", Body: "continue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Deliveries) != 1 || result.Deliveries[0].State != store.DeliverySteered || len(steerer.calls) != 1 {
		t.Fatalf("result=%#v calls=%#v", result, steerer.calls)
	}
	got, err := st.GetWorker("recipient")
	if err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "thread-live" || got.TurnID != "turn-live" || got.Engine != "appserver" || got.Status != store.WorkerRunning {
		t.Fatalf("runtime identity changed: %#v", got)
	}
	if got.ProjectRoot != root || got.Worktree != worktree || got.Branch != "codex/live" || got.Remote == nil || got.Remote.Host != "agent@example" {
		t.Fatalf("execution identity changed: %#v", got)
	}
	if call := steerer.calls[0]; call.cwd != worktree || call.thread != "thread-live" || call.turn != "turn-live" {
		t.Fatalf("steer call = %#v", call)
	}
}

func TestSendReturnsNativeSteeringBridgeForExternalActiveTurnAndReplay(t *testing.T) {
	st := testStore(t)
	at := time.Date(2026, 7, 22, 14, 20, 0, 0, time.UTC)
	root := t.TempDir()
	saveWorkers(t, st,
		store.Worker{ID: "sender", Engine: "tracker", Status: store.WorkerIdle, ProjectRoot: root, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "recipient", Engine: "appserver", RuntimeOwner: store.RuntimeOwnerExternal, Status: store.WorkerRunning, HostID: "host-local", ThreadID: "thread-live", TurnID: "turn-live", ProjectRoot: root, CreatedAt: at, UpdatedAt: at},
	)
	steerer := &recordingSteerer{}
	service := Service{Store: st, Steerer: steerer, Now: func() time.Time { return at }}
	queued, err := service.Send(context.Background(), SendRequest{
		RequestID: "queue-first", Kind: store.MessageDirect, From: "sender", To: "recipient", Body: "continue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(queued.Deliveries) != 1 || queued.Deliveries[0].State != store.DeliveryQueued || len(queued.NativeSteering) != 1 || len(steerer.calls) != 0 {
		t.Fatalf("queued result = %#v", queued)
	}
	bridge := queued.NativeSteering[0]
	if bridge.DeliveryID != queued.Deliveries[0].ID || bridge.MessageID != queued.Message.ID || bridge.RecipientID != "recipient" || bridge.HostID != "host-local" || bridge.ThreadID != "thread-live" || bridge.TurnID != "turn-live" {
		t.Fatalf("native steering = %#v", bridge)
	}
	if !strings.Contains(bridge.Prompt, "SWARM_DM from=sender message_id="+queued.Message.ID) || !strings.Contains(bridge.Prompt, "continue") {
		t.Fatalf("native prompt = %q", bridge.Prompt)
	}
	replayed, err := service.Send(context.Background(), SendRequest{
		RequestID: "queue-first", Kind: store.MessageDirect, From: "sender", To: "recipient", Body: "continue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || len(replayed.NativeSteering) != 1 || replayed.NativeSteering[0].DeliveryID != bridge.DeliveryID {
		t.Fatalf("replayed result = %#v", replayed)
	}
}

func TestForwardCompletionCreatesParentDelivery(t *testing.T) {
	st := testStore(t)
	at := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	saveWorkers(t, st,
		store.Worker{ID: "parent", Engine: "mock", Status: store.WorkerIdle, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "child", ParentID: "parent", Engine: "mock", Status: store.WorkerDone, CreatedAt: at, UpdatedAt: at},
	)
	result, forwarded, err := (Service{Store: st, Now: func() time.Time { return at }}).ForwardCompletion(context.Background(), "complete-1", "child", "tests green")
	if err != nil || !forwarded {
		t.Fatalf("ForwardCompletion() forwarded=%t err=%v", forwarded, err)
	}
	if result.Message.Kind != store.MessageCompletion || len(result.Deliveries) != 1 || result.Deliveries[0].RecipientID != "parent" {
		t.Fatalf("completion result = %#v", result)
	}
}

func TestTouchCreatesBilateralWarningWithoutBlocking(t *testing.T) {
	st := testStore(t)
	at := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	saveWorkers(t, st,
		store.Worker{ID: "w-1", Engine: "mock", Status: store.WorkerIdle, CreatedAt: at, UpdatedAt: at},
		store.Worker{ID: "w-2", Engine: "mock", Status: store.WorkerIdle, CreatedAt: at, UpdatedAt: at},
	)
	service := Service{Store: st, Now: func() time.Time { return at }}
	if _, err := service.Touch(context.Background(), TouchRequest{RequestID: "touch-1", WorkerID: "w-1", Repo: `C:\repo`, Path: `C:\repo\main.go`, Operation: "write", Intent: "change parser"}); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	result, err := service.Touch(context.Background(), TouchRequest{RequestID: "touch-2", WorkerID: "w-2", Repo: `C:\repo`, Path: `C:\repo\main.go`, Operation: "write", Intent: "change serializer"})
	if err != nil {
		t.Fatalf("Touch() error = %v", err)
	}
	if len(result.Conflicts) != 1 || len(result.Warnings) != 1 || len(result.Warnings[0].Deliveries) != 2 {
		t.Fatalf("touch result = %#v", result)
	}
	for _, workerID := range []string{"w-1", "w-2"} {
		inbox, err := st.ListQueuedMessages(workerID)
		if err != nil || len(inbox) != 1 || inbox[0].Message.Kind != store.MessageConflict {
			t.Fatalf("inbox %s = %#v err=%v", workerID, inbox, err)
		}
	}
}

func testStore(t *testing.T) *store.JSONStore {
	t.Helper()
	return store.NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
}

func saveWorkers(t *testing.T, st *store.JSONStore, workers ...store.Worker) {
	t.Helper()
	if err := st.SaveWorkers(workers...); err != nil {
		t.Fatalf("SaveWorkers() error = %v", err)
	}
}
