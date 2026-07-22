package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCLICodexTaskCollectionRequiresSubcommand(t *testing.T) {
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: time.Now}
	err := c.run([]string{"tasks", "collect"})
	if err == nil || err.Error() != "tasks collect requires <page|status|finish>" {
		t.Fatalf("error = %v", err)
	}
}

func TestCLICodexTaskCollectionPersistsPagesAndFinishes(t *testing.T) {
	now := time.Date(2026, 7, 22, 20, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	writePage := func(name string, start, count int) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		tasks := make([]store.CodexTaskHostObservation, count)
		for i := range tasks {
			tasks[i] = store.CodexTaskHostObservation{ThreadID: fmt.Sprintf("thread-%02d", start+i), Status: "idle", Unread: start+i == 74}
		}
		data, err := json.Marshal(map[string]any{"tasks": tasks})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	page1, page2 := writePage("page-1.json", 0, 50), writePage("page-2.json", 50, 25)
	opaqueCursor := " \npage-2\t "
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	base := []string{"tasks", "collect", "page", "--state", state, "--host", "desktop", "--observation", "heartbeat-7"}
	args := append(append([]string(nil), base...), "--page", "1", "--next-cursor", opaqueCursor, "--file", page1)
	if err := c.run(args); err != nil {
		t.Fatalf("page 1 error = %v", err)
	}
	if !strings.Contains(out.String(), "page=1 tasks=50") {
		t.Fatalf("page 1 output = %q", out.String())
	}
	out.Reset()
	statusArgs := []string{"tasks", "collect", "status", "--state", state, "--host", "desktop", "--observation", "heartbeat-7"}
	if err := c.run(statusArgs); err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !strings.Contains(out.String(), "next_page=2") || !strings.Contains(out.String(), `next_cursor=" \npage-2\t "`) || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("status output = %q", out.String())
	}
	out.Reset()
	args = append(append([]string(nil), base...), "--page", "2", "--cursor", opaqueCursor, "--file", page2)
	if err := c.run(args); err != nil {
		t.Fatalf("page 2 error = %v", err)
	}
	out.Reset()
	finish := []string{"tasks", "collect", "finish", "--state", state, "--host", "desktop", "--observation", "heartbeat-7"}
	if err := c.run(finish); err != nil {
		t.Fatalf("finish error = %v", err)
	}
	if !strings.Contains(out.String(), "pages=2") || !strings.Contains(out.String(), "observed=75 inserted=75") {
		t.Fatalf("finish output = %q", out.String())
	}
	out.Reset()
	if err := c.run(finish); err != nil {
		t.Fatalf("finish replay error = %v", err)
	}
	if !strings.Contains(out.String(), "replayed=true") {
		t.Fatalf("finish replay output = %q", out.String())
	}
	page, err := store.NewJSONStore(state).ListCodexTasks(store.CodexTaskListFilter{HostID: "desktop", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 75 || page.Total != 75 {
		t.Fatalf("task page = %#v", page)
	}
}

func TestCLICodexTaskCollectionRequiresExplicitHost(t *testing.T) {
	t.Setenv("CODEX_HOST_ID", "")
	path := filepath.Join(t.TempDir(), "page.json")
	if err := os.WriteFile(path, []byte(`{"tasks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: time.Now}
	err := c.run([]string{"tasks", "collect", "page", "--state", filepath.Join(t.TempDir(), "state.json"), "--observation", "one", "--page", "1", "--file", path})
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("error = %v", err)
	}
}
