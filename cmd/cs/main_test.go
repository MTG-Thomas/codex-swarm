package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIWorkflow(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &errOut,
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if got := defaultStatePath(); strings.Contains(got, ".codex-swarm"+string(filepath.Separator)+"state.json") {
		t.Fatalf("defaultStatePath() = %q, want machine-level config path", got)
	}

	if err := c.run([]string{"agent", "register", "--state", state, "--name", "test-thread", "--role", "implementer"}); err != nil {
		t.Fatalf("agent register error = %v", err)
	}
	if !strings.Contains(out.String(), "agent a-20260624-120000") {
		t.Fatalf("agent register output = %q", out.String())
	}
	out.Reset()
	if err := c.run([]string{"agent", "current", "--state", state}); err != nil {
		t.Fatalf("agent current error = %v", err)
	}
	if !strings.Contains(out.String(), "test-thread") || !strings.Contains(out.String(), "current=true") {
		t.Fatalf("agent current output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--role", "implementer", "--issue", "MTG-Thomas/codex-swarm#42", "--prompt", "inspect repo and report"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	if !strings.Contains(out.String(), "spawned w-20260624-120000") {
		t.Fatalf("spawn output = %q", out.String())
	}
	if !strings.Contains(out.String(), "issue: MTG-Thomas/codex-swarm#42") {
		t.Fatalf("spawn issue output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--role", "reviewer", "--parent", "w-20260624-120000", "--prompt", "review the implementation"}); err != nil {
		t.Fatalf("spawn reviewer error = %v", err)
	}
	if !strings.Contains(out.String(), "swarm: role=reviewer parent=w-20260624-120000") {
		t.Fatalf("reviewer spawn output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"claim", "create", "--state", state, "--repo", ".", "--scope", "internal/store", "--worker", "w-20260624-120000", "--issue", "MTG-Thomas/codex-swarm#42", "--note", "editing store claims"}); err != nil {
		t.Fatalf("claim create error = %v", err)
	}
	if !strings.Contains(out.String(), "claim c-20260624-120002") || !strings.Contains(out.String(), "conflicts=0") {
		t.Fatalf("claim create output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"claim", "conflicts", "--state", state, "--repo", ".", "--scope", "internal/store/json.go"}); err != nil {
		t.Fatalf("claim conflicts error = %v", err)
	}
	if !strings.Contains(out.String(), "conflicts=1") || !strings.Contains(out.String(), "c-20260624-120002") {
		t.Fatalf("claim conflicts output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "export", "--state", state, "--issue", "MTG-Thomas/codex-swarm#42"}); err != nil {
		t.Fatalf("claim export error = %v", err)
	}
	if !strings.Contains(out.String(), "codex-swarm claims for `MTG-Thomas/codex-swarm#42`") || !strings.Contains(out.String(), "c-20260624-120002") {
		t.Fatalf("claim export output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "block", "--state", state, "--reason", "waiting on review", "--next", "reviewer checks json store", "c-20260624-120002"}); err != nil {
		t.Fatalf("claim block error = %v", err)
	}
	if !strings.Contains(out.String(), "blocked c-20260624-120002") {
		t.Fatalf("claim block output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"claim", "release", "--state", state, "--note", "done", "c-20260624-120002"}); err != nil {
		t.Fatalf("claim release error = %v", err)
	}
	if !strings.Contains(out.String(), "released c-20260624-120002") {
		t.Fatalf("claim release output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"message", "--state", state, "w-20260624-120000", "w-20260624-120001", "please review the store changes"}); err != nil {
		t.Fatalf("message error = %v", err)
	}
	if !strings.Contains(out.String(), "message w-20260624-120000 -> w-20260624-120001") {
		t.Fatalf("message output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"handoff", "--state", state, "w-20260624-120000", "w-20260624-120001", "implementation ready for review"}); err != nil {
		t.Fatalf("handoff error = %v", err)
	}
	if !strings.Contains(out.String(), "handoff w-20260624-120000 -> w-20260624-120001") {
		t.Fatalf("handoff output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"schedule", "add", "--state", state, "--repo", ".", "--cron", "0 8 * * 1", "--prompt", "weekly repo check"}); err != nil {
		t.Fatalf("schedule add error = %v", err)
	}
	if !strings.Contains(out.String(), "schedule s-20260624-120006") {
		t.Fatalf("schedule add output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"schedule", "list", "--state", state}); err != nil {
		t.Fatalf("schedule list error = %v", err)
	}
	if !strings.Contains(out.String(), "schedules=1") || !strings.Contains(out.String(), "weekly repo check") {
		t.Fatalf("schedule list output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"send", "--state", state, "w-20260624-120000", "continue with tests"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	if !strings.Contains(out.String(), "sent w-20260624-120000") {
		t.Fatalf("send output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"report", "--state", state, "--note", "demo complete", "w-20260624-120000", "done"}); err != nil {
		t.Fatalf("report error = %v", err)
	}
	if !strings.Contains(out.String(), "status=done") {
		t.Fatalf("report output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"status", "--state", state}); err != nil {
		t.Fatalf("status error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "workers=2") || !strings.Contains(got, "w-20260624-120000") || !strings.Contains(got, "w-20260624-120001") {
		t.Fatalf("status output = %q", got)
	}
}
