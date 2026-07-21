package coordination

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type recordingSteerer struct {
	calls []steerCall
	err   error
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
