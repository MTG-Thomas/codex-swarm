package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCLICodexTasksIngestListAndStatus(t *testing.T) {
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	tasks := make([]store.CodexTaskObservation, 75)
	for i := range tasks {
		tasks[i] = store.CodexTaskObservation{ThreadID: fmt.Sprintf("task-%02d", i), Title: fmt.Sprintf("Task %02d", i), Status: "idle", Unread: i%3 == 0}
	}
	data, err := json.Marshal(protocol.CodexTaskIngestRequest{RequestID: "cli-snapshot", HostID: "local", Source: "codex.list_threads", ObservedAt: now, Tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := c.run([]string{"tasks", "ingest", "--state", state, "--file", snapshotPath}); err != nil {
		t.Fatalf("ingest error = %v", err)
	}
	if !strings.Contains(out.String(), "observed=75 inserted=75") {
		t.Fatalf("ingest output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"tasks", "list", "--state", state, "--limit", "100", "--json"}); err != nil {
		t.Fatalf("list error = %v", err)
	}
	var page protocol.CodexTaskListResponse
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v\n%s", err, out.String())
	}
	if len(page.Tasks) != 75 || page.Total != 75 || page.NextCursor != "" {
		t.Fatalf("page = %#v", page)
	}

	out.Reset()
	if err := c.run([]string{"tasks", "status", "--state", state, "--stale-for", "0"}); err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !strings.Contains(out.String(), "tasks total=75 unread=25") || !strings.Contains(out.String(), "status=idle count=75") {
		t.Fatalf("status output = %q", out.String())
	}
}

func TestCLICodexTasksDaemonListMatchesDirectRead(t *testing.T) {
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	_, err := st.IngestCodexTasks(store.CodexTaskIngestRequest{RequestID: "one", HostID: "remote", Source: "test", ObservedAt: now, Tasks: []store.CodexTaskObservation{{ThreadID: "thread-1", Title: "Remote", Status: "active", Unread: true}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemon.NewServer(state, st).Handler())
	defer server.Close()
	var direct, remote bytes.Buffer
	directCLI := cli{out: &direct, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	remoteCLI := cli{out: &remote, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	args := []string{"tasks", "list", "--state", state, "--host", "remote", "--json"}
	if err := directCLI.run(args); err != nil {
		t.Fatal(err)
	}
	args = append(args, "--daemon", server.URL)
	if err := remoteCLI.run(args); err != nil {
		t.Fatal(err)
	}
	if direct.String() != remote.String() {
		t.Fatalf("direct and daemon differ:\ndirect=%s\ndaemon=%s", direct.String(), remote.String())
	}
}
