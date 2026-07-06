package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCLITraceLifecycle(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"trace", "start", "--state", state, "--agent", "tester", "--key", "root", "--json", "Release swarm"}); err != nil {
		t.Fatalf("trace start error = %v", err)
	}
	var payload traceOutput
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("trace start JSON = %q: %v", out.String(), err)
	}
	if !payload.Created || payload.StackDepth != 1 || payload.Current != "Release swarm" {
		t.Fatalf("trace start payload = %#v", payload)
	}

	out.Reset()
	if err := c.run([]string{"trace", "start", "--state", state, "--agent", "tester", "--key", "root", "--json", "Release swarm"}); err != nil {
		t.Fatalf("trace duplicate start error = %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("trace duplicate JSON = %q: %v", out.String(), err)
	}
	if payload.Created || payload.StackDepth != 1 {
		t.Fatalf("trace duplicate payload = %#v", payload)
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"trace", "into", "--state", state, "--agent", "tester", "--key", "ci", "Watch CI"}); err != nil {
		t.Fatalf("trace into error = %v", err)
	}
	if !strings.Contains(out.String(), "depth=2") || !strings.Contains(out.String(), "Watch CI") {
		t.Fatalf("trace into output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"trace", "log", "--state", state, "--agent", "tester", "CI pending"}); err != nil {
		t.Fatalf("trace log error = %v", err)
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"trace", "done", "--state", state, "--agent", "tester", "CI passed"}); err != nil {
		t.Fatalf("trace done error = %v", err)
	}
	if !strings.Contains(out.String(), "depth=1") || !strings.Contains(out.String(), "Release swarm") {
		t.Fatalf("trace done output = %q", out.String())
	}

	lane, err := store.NewJSONStore(state).GetTraceLane("tester")
	if err != nil {
		t.Fatalf("GetTraceLane() error = %v", err)
	}
	if len(lane.Stack) != 1 || len(lane.Events) != 4 {
		t.Fatalf("lane = %#v", lane)
	}
}

func TestCLIJANITORStaleReadOnlyAndReleaseApply(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)

	freshWorker := store.Worker{ID: "w-fresh", ProjectRoot: "C:/repo", Status: store.WorkerRunning, Prompt: "fresh", CreatedAt: now, UpdatedAt: now}
	staleWorker := store.Worker{ID: "w-stale", ProjectRoot: "C:/repo", Status: store.WorkerRunning, Prompt: "stale", CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour)}
	doneWorker := store.Worker{ID: "w-done", ProjectRoot: "C:/repo", Status: store.WorkerDone, Prompt: "done", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	if err := st.SaveWorkers(freshWorker, staleWorker, doneWorker); err != nil {
		t.Fatalf("SaveWorkers() error = %v", err)
	}
	claimsList := []store.Claim{
		{ID: "c-fresh", WorkerID: "w-fresh", Repo: "C:/repo", Scope: "fresh", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now},
		{ID: "c-expired", WorkerID: "w-fresh", Repo: "C:/repo", Scope: "expired", Status: store.ClaimActive, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "c-stale", WorkerID: "w-stale", Repo: "C:/repo", Scope: "stale", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour)},
		{ID: "c-terminal", WorkerID: "w-done", Repo: "C:/repo", Scope: "terminal", Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
	}
	for _, claim := range claimsList {
		if err := st.SaveClaim(claim); err != nil {
			t.Fatalf("SaveClaim(%s) error = %v", claim.ID, err)
		}
	}

	if err := c.run([]string{"janitor", "stale", "--state", state, "--older", "24h"}); err != nil {
		t.Fatalf("janitor stale error = %v", err)
	}
	if !strings.Contains(out.String(), "stale_workers=1") || !strings.Contains(out.String(), "releasable_claims=3") {
		t.Fatalf("janitor stale output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"janitor", "release", "--state", state, "--older", "24h"}); err != nil {
		t.Fatalf("janitor release dry-run error = %v", err)
	}
	if !strings.Contains(out.String(), "dry_run=true") {
		t.Fatalf("janitor release dry-run output = %q", out.String())
	}
	got, err := st.GetClaim("c-expired")
	if err != nil {
		t.Fatalf("GetClaim(c-expired) error = %v", err)
	}
	if got.Status != store.ClaimActive {
		t.Fatalf("dry-run changed status = %s", got.Status)
	}

	out.Reset()
	if err := c.run([]string{"janitor", "release", "--state", state, "--older", "24h", "--apply", "--note", "cleanup"}); err != nil {
		t.Fatalf("janitor release apply error = %v", err)
	}
	if !strings.Contains(out.String(), "released_claims=3") {
		t.Fatalf("janitor release apply output = %q", out.String())
	}
	got, err = st.GetClaim("c-expired")
	if err != nil {
		t.Fatalf("GetClaim(c-expired) after apply error = %v", err)
	}
	if got.Status != store.ClaimReleased || got.Note != "cleanup" {
		t.Fatalf("released claim = %#v", got)
	}
	fresh, err := st.GetClaim("c-fresh")
	if err != nil {
		t.Fatalf("GetClaim(c-fresh) error = %v", err)
	}
	if fresh.Status != store.ClaimActive {
		t.Fatalf("fresh claim status = %s", fresh.Status)
	}
}
