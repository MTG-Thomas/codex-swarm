package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCodexTaskCollectionRoutesAndClientRoundTrip(t *testing.T) {
	st := store.NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	server := httptest.NewServer(NewServer("state.json", st).Handler())
	defer server.Close()
	client := Client{BaseURL: server.URL}
	now := time.Date(2026, 7, 22, 21, 0, 0, 0, time.UTC)
	first := make([]protocol.CodexTaskHostObservation, 50)
	second := make([]protocol.CodexTaskHostObservation, 25)
	for i := range first {
		first[i] = protocol.CodexTaskHostObservation{ThreadID: fmt.Sprintf("thread-%02d", i), Status: "active"}
	}
	for i := range second {
		second[i] = protocol.CodexTaskHostObservation{ThreadID: fmt.Sprintf("thread-%02d", i+50), Status: "idle", Unread: i == 24}
	}
	page, err := client.AddCodexTaskCollectionPage(context.Background(), protocol.CodexTaskCollectionPageRequest{
		HostID: "desktop", ObservationID: "heartbeat-1", ObservedAt: now,
		Page: 1, NextCursor: "page-2", Tasks: first,
	})
	if err != nil || page.Tasks != 50 || page.Replayed {
		t.Fatalf("page 1 result=%#v err=%v", page, err)
	}
	progress, err := client.CodexTaskCollectionStatus(context.Background(), "desktop", "heartbeat-1")
	if err != nil {
		t.Fatal(err)
	}
	if progress.Pages != 1 || progress.Tasks != 50 || progress.NextPage != 2 || progress.NextCursor != "page-2" {
		t.Fatalf("progress = %#v", progress)
	}
	_, err = client.AddCodexTaskCollectionPage(context.Background(), protocol.CodexTaskCollectionPageRequest{
		HostID: "desktop", ObservationID: "heartbeat-1", ObservedAt: now.Add(time.Minute),
		Page: 2, Cursor: "page-2", Tasks: second,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := client.FinishCodexTaskCollection(context.Background(), protocol.CodexTaskCollectionFinishRequest{
		HostID: "desktop", ObservationID: "heartbeat-1", Coverage: store.CodexTaskCoverageWindow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Pages != 2 || finished.Ingest.Observed != 75 || finished.Ingest.Inserted != 75 {
		t.Fatalf("finish = %#v", finished)
	}
	indexed, err := client.CodexTasks(context.Background(), store.CodexTaskListFilter{HostID: "desktop", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if indexed.Total != 75 || len(indexed.Tasks) != 75 {
		t.Fatalf("indexed = %#v", indexed)
	}
}

func TestCodexTaskCollectionMutationRequiresLoopbackAndStrictJSON(t *testing.T) {
	server := NewServer("state.json", store.NewJSONStore(filepath.Join(t.TempDir(), "state.json")))
	body, _ := json.Marshal(protocol.CodexTaskCollectionPageRequest{HostID: "desktop", ObservationID: "one", ObservedAt: time.Now().UTC(), Page: 1})
	req := httptest.NewRequest(http.MethodPost, "/v1/codex-tasks/collections/pages", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "loopback_required") {
		t.Fatalf("non-loopback response = %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/codex-tasks/collections/pages", strings.NewReader(`{"host_id":"desktop","observation_id":"one","observed_at":"2026-07-22T21:00:00Z","page":1,"unknown":true}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_json") {
		t.Fatalf("strict JSON response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCodexTaskCollectionErrorClassifiesNestedIngestReplayMismatch(t *testing.T) {
	status, code := codexTaskCollectionError(fmt.Errorf("finish collection: %w", store.ErrCodexTaskReplayMismatch))
	if status != http.StatusConflict || code != "request_replay_mismatch" {
		t.Fatalf("status=%d code=%q", status, code)
	}
}
