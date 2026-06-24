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

	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--prompt", "inspect repo and report"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	if !strings.Contains(out.String(), "spawned w-20260624-120000") {
		t.Fatalf("spawn output = %q", out.String())
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
	if !strings.Contains(got, "workers=1") || !strings.Contains(got, "w-20260624-120000") {
		t.Fatalf("status output = %q", got)
	}
}
