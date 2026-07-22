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

func TestCodexTaskRoutesAndClientRoundTrip(t *testing.T) {
	st := store.NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	now := time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC)
	tasks := make([]store.CodexTaskObservation, 61)
	for i := range tasks {
		tasks[i] = store.CodexTaskObservation{ThreadID: fmt.Sprintf("thread-%02d", i), Title: fmt.Sprintf("Task %02d", i), Status: "idle", Unread: i%2 == 0}
	}
	server := httptest.NewServer(NewServer("state.json", st).Handler())
	defer server.Close()
	client := Client{BaseURL: server.URL}
	result, err := client.IngestCodexTasks(context.Background(), protocol.CodexTaskIngestRequest{
		RequestID: "daemon-snapshot", HostID: "local", Source: "codex.list_threads", ObservedAt: now, Tasks: tasks,
	})
	if err != nil {
		t.Fatalf("IngestCodexTasks() error = %v", err)
	}
	if result.Inserted != 61 {
		t.Fatalf("result = %#v", result)
	}
	daemonStatus, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if daemonStatus.CodexTaskCount != 61 || !strings.Contains(daemonStatus.String(), "tasks=61") {
		t.Fatalf("daemon status = %#v text=%q", daemonStatus, daemonStatus.String())
	}
	page, err := client.CodexTasks(context.Background(), store.CodexTaskListFilter{HostID: "local", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 50 || page.Total != 61 || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	next, err := client.CodexTasks(context.Background(), store.CodexTaskListFilter{HostID: "local", Limit: 50, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Tasks) != 11 || next.Total != 61 || next.NextCursor != "" {
		t.Fatalf("next = %#v", next)
	}
	stats, err := client.CodexTaskStatus(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 61 || stats.Unread != 31 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestCodexTaskIngestRequiresLoopbackAndRejectsReplayMismatch(t *testing.T) {
	st := store.NewJSONStore(filepath.Join(t.TempDir(), "state.json"))
	server := NewServer("state.json", st)
	request := protocol.CodexTaskIngestRequest{RequestID: "same", HostID: "local", Source: "test", ObservedAt: time.Now().UTC(), Tasks: []store.CodexTaskObservation{{ThreadID: "one", Title: "One"}}}
	body, _ := json.Marshal(request)
	req := httptest.NewRequest(http.MethodPost, "/v1/codex-tasks/ingest", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "loopback_required") {
		t.Fatalf("non-loopback response = %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/codex-tasks/ingest", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first ingest = %d %s", rec.Code, rec.Body.String())
	}
	request.Tasks[0].Title = "Different"
	body, _ = json.Marshal(request)
	req = httptest.NewRequest(http.MethodPost, "/v1/codex-tasks/ingest", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "request_replay_mismatch") {
		t.Fatalf("mismatch response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCodexTaskRoutesUnavailableForLegacyStore(t *testing.T) {
	server := NewServer("state.json", &memoryStore{})
	req := httptest.NewRequest(http.MethodGet, "/v1/codex-tasks", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "task_index_unavailable") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
