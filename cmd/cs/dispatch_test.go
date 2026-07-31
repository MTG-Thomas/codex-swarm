package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCLIDispatchPrepareAndBindNativeTask(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	statePath := filepath.Join(t.TempDir(), "state.db")
	repo := t.TempDir()
	parent := store.Worker{ID: "parent", ProjectRoot: repo, Engine: "tracker", ThreadID: "parent-thread", Status: store.WorkerIdle, CreatedAt: now, UpdatedAt: now}
	if err := store.NewJSONStore(statePath).SaveWorker(parent); err != nil {
		t.Fatal(err)
	}

	prepareArgs := []string{"dispatch", "prepare", "--json", "--state", statePath, "--repo", repo, "--request-id", "prepare-one", "--parent", parent.ID, "--role", "tester", "--prompt", "verify the native task path"}
	if err := c.run(prepareArgs); err != nil {
		t.Fatalf("dispatch prepare error = %v", err)
	}
	var prepared protocol.DispatchPrepareResponse
	if err := json.Unmarshal(out.Bytes(), &prepared); err != nil {
		t.Fatalf("decode prepare response: %v\n%s", err, out.String())
	}
	if prepared.Replayed || prepared.Worker.Status != store.WorkerPending || prepared.Worker.RuntimeOwner != store.RuntimeOwnerExternal || prepared.Worker.TaskEnvironment != store.NativeTaskEnvironmentWorktree {
		t.Fatalf("prepared worker = %#v", prepared.Worker)
	}
	if prepared.NativeTaskCreate.WorkerID != prepared.Worker.ID || prepared.NativeTaskCreate.BindRequestID != "dispatch-bind-"+prepared.Worker.ID || prepared.NativeTaskCreate.ProjectRoot != repo {
		t.Fatalf("native task create = %#v", prepared.NativeTaskCreate)
	}
	for _, expected := range []string{prepared.Worker.ID, parent.ID, "Do not create or attach a duplicate", "confirm its thread matches CODEX_THREAD_ID", "Ensure CODEX_SWARM_DAEMON_URL serves state", "cs close --json"} {
		if !strings.Contains(prepared.NativeTaskCreate.Prompt, expected) {
			t.Fatalf("native prompt missing %q: %s", expected, prepared.NativeTaskCreate.Prompt)
		}
	}

	out.Reset()
	if err := c.run(prepareArgs); err != nil {
		t.Fatalf("dispatch prepare replay error = %v", err)
	}
	var replayed protocol.DispatchPrepareResponse
	if err := json.Unmarshal(out.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Worker.ID != prepared.Worker.ID {
		t.Fatalf("prepare replay = %#v", replayed)
	}

	now = now.Add(time.Second)
	out.Reset()
	worktree := t.TempDir()
	bindArgs := []string{"dispatch", "bind", "--json", "--state", statePath, "--worker", prepared.Worker.ID, "--host-id", "local", "--thread", "thread-native", "--turn", "turn-native", "--cwd", worktree, "--branch", "codex/native-task"}
	if err := c.run(bindArgs); err != nil {
		t.Fatalf("dispatch bind error = %v", err)
	}
	var bound protocol.DispatchBindResponse
	if err := json.Unmarshal(out.Bytes(), &bound); err != nil {
		t.Fatalf("decode bind response: %v\n%s", err, out.String())
	}
	if bound.Replayed || bound.Worker.Status != store.WorkerRunning || bound.Worker.HostID != "local" || bound.Worker.ThreadID != "thread-native" || bound.Worker.TurnID != "turn-native" || bound.Worker.Worktree != worktree || bound.Worker.Branch != "codex/native-task" {
		t.Fatalf("bound worker = %#v", bound.Worker)
	}

	out.Reset()
	if err := c.run(bindArgs); err != nil {
		t.Fatalf("dispatch bind replay error = %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &bound); err != nil {
		t.Fatal(err)
	}
	if !bound.Replayed {
		t.Fatalf("bind replay = %#v", bound)
	}
}

func TestCLIDispatchPrepareRejectsUnknownEnvironment(t *testing.T) {
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: time.Now}
	err := c.run([]string{"dispatch", "prepare", "--state", filepath.Join(t.TempDir(), "state.db"), "--repo", t.TempDir(), "--environment", "shared", "--prompt", "work"})
	if err == nil || !strings.Contains(err.Error(), "worktree or local") {
		t.Fatalf("error = %v", err)
	}
}
