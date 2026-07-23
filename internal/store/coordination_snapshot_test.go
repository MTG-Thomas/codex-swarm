package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadCoordinationSnapshotIncludesOperationRecords(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	if err := st.SaveWorker(Worker{ID: "worker", Status: WorkerRunning, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveClaim(Claim{ID: "claim", WorkerID: "worker", Status: ClaimActive, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.CreateMessage(Message{ID: "message", RequestID: "request", Kind: MessageDirect, From: "worker", Body: "hello", CreatedAt: now}, []string{"worker"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveGateEvidence(GateEvidence{ID: "gate", WorkerID: "worker", GateID: "test", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.IngestCodexTasks(CodexTaskIngestRequest{RequestID: "tasks", HostID: "local", Source: "test", ObservedAt: now, Tasks: []CodexTaskObservation{{ThreadID: "thread"}}}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := st.ReadCoordinationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workers) != 1 || len(snapshot.Claims) != 1 || len(snapshot.Messages) != 1 || len(snapshot.GateEvidence) != 1 || len(snapshot.CodexTasks) != 1 {
		t.Fatalf("snapshot counts = workers:%d claims:%d messages:%d gates:%d tasks:%d", len(snapshot.Workers), len(snapshot.Claims), len(snapshot.Messages), len(snapshot.GateEvidence), len(snapshot.CodexTasks))
	}
}
