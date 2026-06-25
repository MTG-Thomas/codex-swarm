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

	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--role", "implementer", "--prompt", "inspect repo and report"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	if !strings.Contains(out.String(), "spawned w-20260624-120000") {
		t.Fatalf("spawn output = %q", out.String())
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
