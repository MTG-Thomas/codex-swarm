package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinationMetricsReportsLiveCoverage(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	appserver := Worker{ID: "w-app", Engine: "appserver", ThreadID: "thread-1", TurnID: "turn-1", Status: WorkerRunning, CreatedAt: now, UpdatedAt: now}
	appserver.ApplyStatusAt(WorkerRunning, now)
	tracker := Worker{ID: "w-tracker", Engine: "tracker", Status: WorkerIdle, CreatedAt: now, UpdatedAt: now}
	tracker.ApplyStatusAt(WorkerIdle, now)
	if err := st.SaveWorkers(appserver, tracker); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveClaim(Claim{ID: "c-1", WorkerID: tracker.ID, Repo: "/repo", Scope: "path:cmd/cs", Status: ClaimActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	message := Message{ID: "m-1", RequestID: "request-1", Kind: MessageDirect, From: appserver.ID, Body: "review", CreatedAt: now}
	if _, _, _, err := st.CreateMessage(message, []string{tracker.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordFileTouch(FileTouch{ID: "t-1", WorkerID: tracker.ID, Repo: "/repo", Path: "/repo/main.go", Operation: "write", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	metrics, err := st.CoordinationMetrics(now)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Backend != "sqlite" || metrics.WorkerCount != 2 || metrics.ActiveWorkers != 2 || metrics.AppserverWorkers != 1 || metrics.SteerableWorkers != 1 || metrics.TrackerWorkers != 1 {
		t.Fatalf("worker metrics = %#v", metrics)
	}
	if metrics.ClaimCount != 1 || metrics.ActiveClaims != 1 || metrics.MessageCount != 1 || metrics.QueuedMessages != 1 || metrics.RecentTouches != 1 {
		t.Fatalf("coordination metrics = %#v", metrics)
	}
}
