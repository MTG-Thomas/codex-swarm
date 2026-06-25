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

func TestIssueExportIncludesParsableClaimMarker(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"claim", "create", "--state", state, "--repo", ".", "--scope", "cmd/cs", "--worker", "w-1", "--issue", "MTG-Thomas/codex-swarm#42", "--note", "testing issue marker"}); err != nil {
		t.Fatalf("claim create error = %v", err)
	}
	out.Reset()

	if err := c.run([]string{"issue", "export", "--state", state, "--issue", "MTG-Thomas/codex-swarm#42"}); err != nil {
		t.Fatalf("issue export error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, claimMarkerStart) || !strings.Contains(got, "codex-swarm claims for `MTG-Thomas/codex-swarm#42`") {
		t.Fatalf("issue export output = %q", got)
	}
	snapshot, ok, err := extractClaimSnapshot(got)
	if err != nil {
		t.Fatalf("extractClaimSnapshot error = %v", err)
	}
	if !ok || snapshot.Issue != "MTG-Thomas/codex-swarm#42" || len(snapshot.Claims) != 1 {
		t.Fatalf("snapshot = %#v, ok=%t", snapshot, ok)
	}
}

func TestLatestClaimSnapshotUsesNewestMarker(t *testing.T) {
	oldBody, err := claimIssueMarkerMarkdown("MTG-Thomas/codex-swarm#42", []store.Claim{{
		ID:     "old",
		Issue:  "MTG-Thomas/codex-swarm#42",
		Status: store.ClaimActive,
	}}, time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("old marker error = %v", err)
	}
	newBody, err := claimIssueMarkerMarkdown("MTG-Thomas/codex-swarm#42", []store.Claim{{
		ID:     "new",
		Issue:  "MTG-Thomas/codex-swarm#42",
		Status: store.ClaimBlocked,
	}}, time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("new marker error = %v", err)
	}
	raw, err := json.Marshal(ghIssueView{
		Body: oldBody,
		Comments: []struct {
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"createdAt"`
		}{{
			Body:      newBody,
			CreatedAt: time.Date(2026, 6, 24, 12, 5, 0, 0, time.UTC),
		}},
	})
	if err != nil {
		t.Fatalf("marshal issue view: %v", err)
	}
	snapshot, err := latestClaimSnapshot(raw)
	if err != nil {
		t.Fatalf("latestClaimSnapshot error = %v", err)
	}
	if len(snapshot.Claims) != 1 || snapshot.Claims[0].ID != "new" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestWorkerIssueReportMarkdownUsesWorkerReport(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	body := workerIssueReportMarkdown("MTG-Thomas/codex-swarm#42", store.Worker{
		ID:          "w-1",
		Issue:       "MTG-Thomas/codex-swarm#42",
		Status:      store.WorkerDone,
		Engine:      "mock",
		ThreadID:    "mock-thread-w-1",
		ProjectRoot: `C:\repo`,
		Report:      "implemented and verified",
		LastMessage: "fallback",
	}, "", now)
	for _, want := range []string{
		"codex-swarm worker report for `MTG-Thomas/codex-swarm#42`",
		"- Worker: `w-1`",
		"- Status: `done`",
		"implemented and verified",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("report markdown missing %q:\n%s", want, body)
		}
	}
}
