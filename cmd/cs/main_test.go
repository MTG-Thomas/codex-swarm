package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/appserver"
	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
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
	if !strings.Contains(out.String(), "spawned w-20260624-120000-") {
		t.Fatalf("spawn output = %q", out.String())
	}
	if !strings.Contains(out.String(), "issue: MTG-Thomas/codex-swarm#42") {
		t.Fatalf("spawn issue output = %q", out.String())
	}
	implementerID := mustFindWorkerByPrompt(t, state, "inspect repo and report").ID

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--role", "reviewer", "--parent", implementerID, "--prompt", "review the implementation"}); err != nil {
		t.Fatalf("spawn reviewer error = %v", err)
	}
	if !strings.Contains(out.String(), "swarm: role=reviewer parent="+implementerID) {
		t.Fatalf("reviewer spawn output = %q", out.String())
	}
	reviewerID := mustFindWorkerByPrompt(t, state, "review the implementation").ID

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"claim", "create", "--state", state, "--repo", ".", "--scope", "internal/store", "--worker", implementerID, "--issue", "MTG-Thomas/codex-swarm#42", "--note", "editing store claims"}); err != nil {
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
	if err := c.run([]string{"message", "--state", state, implementerID, reviewerID, "please review the store changes"}); err != nil {
		t.Fatalf("message error = %v", err)
	}
	if !strings.Contains(out.String(), "message "+implementerID+" -> "+reviewerID) {
		t.Fatalf("message output = %q", out.String())
	}

	now = now.Add(time.Second)
	out.Reset()
	if err := c.run([]string{"handoff", "--state", state, implementerID, reviewerID, "implementation ready for review"}); err != nil {
		t.Fatalf("handoff error = %v", err)
	}
	if !strings.Contains(out.String(), "handoff "+implementerID+" -> "+reviewerID) {
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
	if err := c.run([]string{"send", "--state", state, implementerID, "continue with tests"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	if !strings.Contains(out.String(), "sent "+implementerID) {
		t.Fatalf("send output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"report", "--state", state, "--note", "demo complete", implementerID, "done"}); err != nil {
		t.Fatalf("report error = %v", err)
	}
	if !strings.Contains(out.String(), "status=done") {
		t.Fatalf("report output = %q", out.String())
	}
	reported, err := store.NewJSONStore(state).GetWorker(implementerID)
	if err != nil {
		t.Fatalf("GetWorker(reported) error = %v", err)
	}
	if reported.Status != store.WorkerDone {
		t.Fatalf("reported Status = %q, want %q", reported.Status, store.WorkerDone)
	}
	if reported.Lifecycle == nil {
		t.Fatal("reported Lifecycle = nil, want done lifecycle")
	}
	if got := reported.Lifecycle.DeriveStatus(); got != lifecycle.DisplayDone {
		t.Fatalf("reported Lifecycle.DeriveStatus() = %q, want %q", got, lifecycle.DisplayDone)
	}
	if reported.Lifecycle.Session.CompletedAt == nil || !reported.Lifecycle.Session.CompletedAt.Equal(now) {
		t.Fatalf("reported CompletedAt = %v, want %v", reported.Lifecycle.Session.CompletedAt, now)
	}
	if reported.Lifecycle.Session.TerminatedAt != nil {
		t.Fatalf("reported TerminatedAt = %v, want nil", reported.Lifecycle.Session.TerminatedAt)
	}

	out.Reset()
	if err := c.run([]string{"status", "--state", state}); err != nil {
		t.Fatalf("status error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "workers=2") || !strings.Contains(got, implementerID) || !strings.Contains(got, reviewerID) {
		t.Fatalf("status output = %q", got)
	}
}

func TestCLIReportFailedSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	now := time.Date(2026, 6, 26, 14, 30, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &errOut,
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-failed",
		ProjectRoot: "/repo",
		ThreadID:    "thread-failed",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "worker that fails",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	if err := c.run([]string{"report", "--state", state, "--note", "failed test", "w-failed", "failed"}); err != nil {
		t.Fatalf("report failed error = %v", err)
	}

	worker, err := store.NewJSONStore(state).GetWorker("w-failed")
	if err != nil {
		t.Fatalf("GetWorker() error = %v", err)
	}
	if worker.Lifecycle == nil {
		t.Fatal("Lifecycle = nil, want failed lifecycle")
	}
	if worker.Lifecycle.Session.TerminatedAt == nil || !worker.Lifecycle.Session.TerminatedAt.Equal(now) {
		t.Fatalf("TerminatedAt = %v, want %v", worker.Lifecycle.Session.TerminatedAt, now)
	}
	if worker.Lifecycle.Session.CompletedAt != nil {
		t.Fatalf("CompletedAt = %v, want nil", worker.Lifecycle.Session.CompletedAt)
	}
}

func TestCLIStatusIssuesShowsCompactOperationsDashboard(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 17, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	for _, worker := range []store.Worker{
		{
			ID:          "w-active",
			Issue:       "MTG-Thomas/codex-swarm#11",
			ProjectRoot: "/repo",
			ThreadID:    "thread-active",
			Engine:      "mock",
			Status:      store.WorkerRunning,
			Prompt:      "active issue work",
			CreatedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Hour),
		},
		{
			ID:          "w-stale",
			Issue:       "MTG-Thomas/codex-swarm#9",
			ProjectRoot: "/repo",
			ThreadID:    "thread-stale",
			Engine:      "mock",
			Status:      store.WorkerIdle,
			Prompt:      "stale issue work",
			CreatedAt:   now.Add(-49 * time.Hour),
			UpdatedAt:   now.Add(-49 * time.Hour),
		},
		{
			ID:          "w-done",
			Issue:       "MTG-Thomas/codex-swarm#10",
			ProjectRoot: "/repo",
			ThreadID:    "thread-done",
			Engine:      "mock",
			Status:      store.WorkerDone,
			Prompt:      "completed issue work",
			CreatedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Hour),
		},
	} {
		worker.ApplyStatusAt(worker.Status, worker.UpdatedAt)
		if err := st.SaveWorker(worker); err != nil {
			t.Fatalf("SaveWorker(%s) error = %v", worker.ID, err)
		}
	}
	if err := st.SaveClaim(store.Claim{
		ID:        "c-active",
		WorkerID:  "w-active",
		Repo:      "/repo",
		Scope:     "cmd/cs",
		Issue:     "MTG-Thomas/codex-swarm#11",
		Status:    store.ClaimActive,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"status", "--issues", "--state", state}); err != nil {
		t.Fatalf("status --issues error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"issues=2 active_workers=2 stale_workers=1 active_claims=1",
		"issue=MTG-Thomas/codex-swarm#9 worker=w-stale status=idle stale=true next=resume-or-release",
		"issue=MTG-Thomas/codex-swarm#11 worker=w-active status=working stale=false next=monitor",
		"claim=c-active issue=MTG-Thomas/codex-swarm#11 worker=w-active scope=cmd/cs next=release-or-sync",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status --issues output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "w-done") {
		t.Fatalf("status --issues included terminal worker:\n%s", got)
	}
}

func TestCLIStatusIssuesDetailIncludesFreshWorkers(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 17, 35, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	worker := store.Worker{
		ID:          "w-fresh",
		Issue:       "MTG-Thomas/codex-swarm#11",
		ProjectRoot: "/repo",
		ThreadID:    "thread-fresh",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "fresh issue work",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}
	worker.ApplyStatusAt(worker.Status, worker.UpdatedAt)
	if err := store.NewJSONStore(state).SaveWorker(worker); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"status", "--issues", "--detail", "--state", state}); err != nil {
		t.Fatalf("status --issues --detail error = %v", err)
	}
	if !strings.Contains(out.String(), "issue=MTG-Thomas/codex-swarm#11 worker=w-fresh status=idle stale=false next=resume") {
		t.Fatalf("detail output missing fresh idle worker:\n%s", out.String())
	}
}

func TestCLIMessageRequestIDIsIdempotentAndRecordsOneSwarmEvent(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 18, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	savePairWorkers(t, state, now, "MTG-Thomas/codex-swarm#77")
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
	}

	if err := c.run([]string{"message", "--state", state, "--request-id", "req-message-1", "w-from", "w-to", "please review"}); err != nil {
		t.Fatalf("message first error = %v", err)
	}
	firstOutput := out.String()

	now = now.Add(time.Minute)
	out.Reset()
	if err := c.run([]string{"message", "--state", state, "--request-id", "req-message-1", "w-from", "w-to", "please review"}); err != nil {
		t.Fatalf("message replay error = %v", err)
	}
	if got := out.String(); got != firstOutput {
		t.Fatalf("message replay output = %q, want original %q", got, firstOutput)
	}

	events, err := store.NewJSONStore(state).ListEvents()
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events count = %d, want 1: %#v", len(events), events)
	}
	event := events[0]
	if event.Type != "message" || event.From != "w-from" || event.To != "w-to" || event.WorkerID != "w-from" || event.Issue != "MTG-Thomas/codex-swarm#77" || event.RequestID != "req-message-1" {
		t.Fatalf("event metadata = %#v, want message/from/to/worker/issue/request id", event)
	}
	if !event.At.Equal(time.Date(2026, 6, 26, 18, 30, 0, 0, time.UTC)) {
		t.Fatalf("event At = %v, want original timestamp", event.At)
	}

	from := mustGetWorker(t, state, "w-from")
	to := mustGetWorker(t, state, "w-to")
	if len(from.Events) != 1 || len(to.Events) != 1 {
		t.Fatalf("worker events counts = from %d to %d, want one each", len(from.Events), len(to.Events))
	}
}

func TestCLIMessageRequestIDRejectsMismatchedReplay(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 18, 45, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	savePairWorkers(t, state, now, "")
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-other",
		ProjectRoot: "/repo",
		ThreadID:    "thread-other",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "other worker",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker(w-other) error = %v", err)
	}
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"message", "--state", state, "--request-id", "req-message-mismatch", "w-from", "w-to", "first"}); err != nil {
		t.Fatalf("message first error = %v", err)
	}
	for _, args := range [][]string{
		{"message", "--state", state, "--request-id", "req-message-mismatch", "w-from", "w-to", "second"},
		{"message", "--state", state, "--request-id", "req-message-mismatch", "w-from", "w-other", "first"},
	} {
		err := c.run(args)
		if err == nil {
			t.Fatalf("message replay args %v error = nil, want mismatch failure", args)
		}
		if !strings.Contains(err.Error(), "does not match original mutation fingerprint") {
			t.Fatalf("message replay args %v error = %v", args, err)
		}
	}
	events, err := store.NewJSONStore(state).ListEvents()
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events count = %d, want original event only", len(events))
	}
}

func TestCLIHandoffRequestIDReplaysOriginalResult(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 19, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	savePairWorkers(t, state, now, "MTG-Thomas/codex-swarm#78")
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
	}

	if err := c.run([]string{"handoff", "--state", state, "--request-id", "req-handoff-1", "w-from", "w-to", "original handoff"}); err != nil {
		t.Fatalf("handoff first error = %v", err)
	}
	firstOutput := out.String()

	now = now.Add(time.Minute)
	out.Reset()
	if err := c.run([]string{"handoff", "--state", state, "--request-id", "req-handoff-1", "w-from", "w-to", "original handoff"}); err != nil {
		t.Fatalf("handoff replay error = %v", err)
	}
	if got := out.String(); got != firstOutput {
		t.Fatalf("handoff replay output = %q, want original %q", got, firstOutput)
	}

	from := mustGetWorker(t, state, "w-from")
	if from.Report != "handoff to w-to: original handoff" {
		t.Fatalf("from Report = %q, want original handoff report", from.Report)
	}
	events, err := store.NewJSONStore(state).ListEvents()
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events count = %d, want 1: %#v", len(events), events)
	}
	event := events[0]
	if event.Type != "handoff" || event.From != "w-from" || event.To != "w-to" || event.WorkerID != "w-from" || event.Issue != "MTG-Thomas/codex-swarm#78" || event.RequestID != "req-handoff-1" {
		t.Fatalf("event metadata = %#v, want handoff/from/to/worker/issue/request id", event)
	}
}

func TestCLIHandoffRequestIDRejectsMismatchedReplay(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 19, 15, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	savePairWorkers(t, state, now, "")
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-other",
		ProjectRoot: "/repo",
		ThreadID:    "thread-other",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "other worker",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker(w-other) error = %v", err)
	}
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"handoff", "--state", state, "--request-id", "req-handoff-mismatch", "w-from", "w-to", "first"}); err != nil {
		t.Fatalf("handoff first error = %v", err)
	}
	for _, args := range [][]string{
		{"handoff", "--state", state, "--request-id", "req-handoff-mismatch", "w-from", "w-to", "second"},
		{"handoff", "--state", state, "--request-id", "req-handoff-mismatch", "w-from", "w-other", "first"},
	} {
		err := c.run(args)
		if err == nil {
			t.Fatalf("handoff replay args %v error = nil, want mismatch failure", args)
		}
		if !strings.Contains(err.Error(), "does not match original mutation fingerprint") {
			t.Fatalf("handoff replay args %v error = %v", args, err)
		}
	}
	events, err := store.NewJSONStore(state).ListEvents()
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events count = %d, want original event only", len(events))
	}
}

func TestCLISwarmEventHistoryIsBoundedAndOrdered(t *testing.T) {
	var out bytes.Buffer
	base := time.Date(2026, 6, 26, 20, 0, 0, 0, time.UTC)
	now := base
	state := filepath.Join(t.TempDir(), "state.json")
	savePairWorkers(t, state, base, "")
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
	}

	for i := 0; i < store.SwarmEventCap+5; i++ {
		now = base.Add(time.Duration(i) * time.Second)
		out.Reset()
		requestID := fmt.Sprintf("req-%03d", i)
		if err := c.run([]string{"message", "--state", state, "--request-id", requestID, "w-from", "w-to", "message"}); err != nil {
			t.Fatalf("message %d error = %v", i, err)
		}
	}

	events, err := store.NewJSONStore(state).ListEvents()
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != store.SwarmEventCap {
		t.Fatalf("events count = %d, want cap %d", len(events), store.SwarmEventCap)
	}
	if events[0].RequestID != "req-005" {
		t.Fatalf("first retained request ID = %q, want req-005", events[0].RequestID)
	}
	if events[len(events)-1].RequestID != "req-504" {
		t.Fatalf("last retained request ID = %q, want req-504", events[len(events)-1].RequestID)
	}
	for i := 1; i < len(events); i++ {
		if events[i].At.Before(events[i-1].At) {
			t.Fatalf("events out of order at %d: %v before %v", i, events[i].At, events[i-1].At)
		}
	}
}

func TestCLISpawnSameSecondCreatesDistinctWorkers(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 18, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--prompt", "first"}); err != nil {
		t.Fatalf("first spawn error = %v", err)
	}
	if err := c.run([]string{"spawn", "--state", state, "--repo", ".", "--prompt", "second"}); err != nil {
		t.Fatalf("second spawn error = %v", err)
	}

	workers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	if len(workers) != 2 {
		t.Fatalf("workers = %d, want 2: %#v", len(workers), workers)
	}
	if workers[0].ID == workers[1].ID {
		t.Fatalf("worker IDs both = %q, want distinct IDs", workers[0].ID)
	}
}

func TestCLIDisplaysLifecycleStatusForStaleWorkers(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	now := time.Date(2026, 6, 26, 15, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &errOut,
		now: func() time.Time { return now },
	}
	state := filepath.Join(t.TempDir(), "state.json")
	staleLifecycle := lifecycle.NewWorkerLifecycle()
	staleLifecycle.Runtime.State = lifecycle.RuntimeDead
	staleLifecycle.Runtime.Reason = lifecycle.ReasonRuntimeLost
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-stale",
		ProjectRoot: "/repo",
		ThreadID:    "thread-stale",
		Engine:      "mock",
		Status:      store.WorkerRunning,
		Lifecycle:   &staleLifecycle,
		Prompt:      "stale worker",
		Report:      "stale report",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	if err := c.run([]string{"status", "--state", state}); err != nil {
		t.Fatalf("status error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "w-stale\tstale\t") {
		t.Fatalf("status output = %q, want stale display status", got)
	}

	out.Reset()
	if err := c.run([]string{"show", "--state", state, "w-stale"}); err != nil {
		t.Fatalf("show error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "status=stale\n") {
		t.Fatalf("show output = %q, want stale display status", got)
	}

	body := workerIssueReportMarkdown("MTG-Thomas/codex-swarm#42", mustGetWorker(t, state, "w-stale"), "", now)
	if !strings.Contains(body, "- Status: `stale`") {
		t.Fatalf("worker report markdown = %q, want stale display status", body)
	}
}

func TestCLIShowSnapshotTextAndJSON(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 27, 18, 10, 0, 0, time.UTC)
	st := store.NewJSONStore(state)
	worker := store.Worker{
		ID:          "w-snapshot",
		Role:        "implementer",
		Issue:       "MTG-Thomas/codex-swarm#17",
		ProjectRoot: "/repo",
		Worktree:    "/repo/.codex-swarm/worktrees/w-snapshot",
		Branch:      "cs/w-snapshot",
		ThreadID:    "thread-snapshot",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "snapshot work",
		LastMessage: "latest update",
		Report:      "ready",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now,
		Events: []store.Event{
			{At: now, Type: "reported", Message: "ready"},
		},
	}
	worker.ApplyStatusAt(store.WorkerIdle, now)
	if err := st.SaveWorker(worker); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
	if err := st.SaveClaim(store.Claim{
		ID:       "c-snapshot",
		WorkerID: "w-snapshot",
		Issue:    "MTG-Thomas/codex-swarm#17",
		Scope:    "cmd/cs",
		Status:   store.ClaimActive,
	}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	if err := st.SaveGateEvidence(store.GateEvidence{
		ID:        "g-snapshot",
		GateID:    "test",
		WorkerID:  "w-snapshot",
		Repo:      "/repo",
		Command:   "go test ./...",
		ExitCode:  0,
		Commit:    "abc123",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveGateEvidence() error = %v", err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"show", "--snapshot", "--state", state, "w-snapshot"}); err != nil {
		t.Fatalf("show --snapshot error = %v", err)
	}
	for _, want := range []string{
		"STATE_SNAPSHOT worker=w-snapshot status=idle role=implementer issue=MTG-Thomas/codex-swarm#17",
		"claim=c-snapshot status=active scope=cmd/cs",
		"gate=test exit=0 commit=abc123 command=go test ./...",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("show --snapshot missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := c.run([]string{"show", "--snapshot", "--json", "--state", state, "w-snapshot"}); err != nil {
		t.Fatalf("show --snapshot --json error = %v", err)
	}
	var decoded struct {
		Schema string `json:"schema"`
		Worker struct {
			ID string `json:"id"`
		} `json:"worker"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("snapshot JSON parse error = %v\n%s", err, out.String())
	}
	if decoded.Schema != "codex-swarm.worker-snapshot.v1" || decoded.Worker.ID != "w-snapshot" {
		t.Fatalf("decoded snapshot = %#v", decoded)
	}
}

func TestCLIStatusPrefersDaemonWithLifecycleAndConflictCounts(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 15, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	staleLifecycle := lifecycle.NewWorkerLifecycle()
	staleLifecycle.Runtime.State = lifecycle.RuntimeDead
	staleLifecycle.Runtime.Reason = lifecycle.ReasonRuntimeLost
	if err := st.SaveWorker(store.Worker{
		ID:          "w-stale",
		ProjectRoot: "/repo",
		Worktree:    "/repo/.codex-swarm/worktrees/w-stale",
		ThreadID:    "thread-stale",
		Engine:      "mock",
		Status:      store.WorkerRunning,
		Lifecycle:   &staleLifecycle,
		Issue:       "MTG-Thomas/codex-swarm#42",
		Prompt:      "stale worker",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
	for _, claim := range []store.Claim{
		{
			ID:        "c-parent",
			WorkerID:  "w-stale",
			Repo:      "/repo",
			Scope:     "internal",
			Status:    store.ClaimActive,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "c-child",
			WorkerID:  "w-other",
			Repo:      "/repo",
			Scope:     "internal/daemon",
			Status:    store.ClaimActive,
			CreatedAt: now,
			UpdatedAt: now,
		},
	} {
		if err := st.SaveClaim(claim); err != nil {
			t.Fatalf("SaveClaim(%s) error = %v", claim.ID, err)
		}
	}
	server := httptest.NewServer(daemon.NewServer(state, st).Handler())
	defer server.Close()

	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := c.run([]string{"status", "--daemon", server.URL, "--state", filepath.Join(t.TempDir(), "ignored.json")}); err != nil {
		t.Fatalf("status --daemon error = %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "daemon=running") || !strings.Contains(got, "workers=1") || !strings.Contains(got, "claims=2") || !strings.Contains(got, "conflicts=1") || !strings.Contains(got, "state="+state) {
		t.Fatalf("status daemon summary = %q", got)
	}
	if !strings.Contains(got, "w-stale\tstale\tMTG-Thomas/codex-swarm#42\t/repo/.codex-swarm/worktrees/w-stale\tthread-stale") {
		t.Fatalf("status daemon worker line = %q", got)
	}
}

func TestCLISpawnAppserverPrintsLifecycleDisplayStatus(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 0, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{ThreadID: "thread-1", TurnID: "turn-1", Status: "inProgress"}, nil
			},
		},
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", ".", "--prompt", "continue"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "status=working") {
		t.Fatalf("spawn output = %q, want lifecycle display status", got)
	}
}

func TestCLIRepoHints(t *testing.T) {
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: time.Now}
	repo := t.TempDir()
	writeRepoHints(t, repo)

	if err := c.run([]string{"repo", "hints", "--repo", repo}); err != nil {
		t.Fatalf("repo hints error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"hints=1",
		`repo hint: remote devcontainer command: just talos-dev-run "just --list"`,
		"repo hint: remote devcontainer image: ghcr.io/mtg-thomas/bifrost-devcontainer:devcontainer-main-172fb07bd73f",
		"prefer immutable image tags",
		"No secrets are injected by default.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repo hints output missing %q:\n%s", want, got)
		}
	}
}

func TestCLIGateListAndRecord(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 15, 0, 0, 0, time.UTC)
	repo := initCLITestRepo(t)
	writeRepoHintsWithQualityGate(t, repo)
	state := filepath.Join(t.TempDir(), "state.json")
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-gate",
		ProjectRoot: repo,
		ThreadID:    "thread-gate",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "run proof",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"gate", "list", "--repo", repo}); err != nil {
		t.Fatalf("gate list error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"gates=1", "test\trepo\tgo test ./...\tunit test suite"} {
		if !strings.Contains(got, want) {
			t.Fatalf("gate list output missing %q:\n%s", want, got)
		}
	}

	out.Reset()
	if err := c.run([]string{"gate", "record", "--state", state, "--repo", repo, "--worker", "w-gate", "--gate", "test", "--exit-code", "0", "--output", "ok ./..."}); err != nil {
		t.Fatalf("gate record error = %v", err)
	}
	got = out.String()
	if !strings.Contains(got, "gate evidence g-20260627-150000-") || !strings.Contains(got, "gate=test exit=0 repo="+repo) || !strings.Contains(got, "commit=") {
		t.Fatalf("gate record output = %q", got)
	}

	evidence, err := store.NewJSONStore(state).ListGateEvidence()
	if err != nil {
		t.Fatalf("ListGateEvidence() error = %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence count = %d, want 1", len(evidence))
	}
	if evidence[0].GateID != "test" || evidence[0].Command != "go test ./..." || evidence[0].Scope != "repo" || evidence[0].Output != "ok ./..." || evidence[0].WorkerID != "w-gate" {
		t.Fatalf("evidence = %#v", evidence[0])
	}
	worker := mustGetWorker(t, state, "w-gate")
	if len(worker.Events) != 1 || worker.Events[0].Type != "quality.gate" || !strings.Contains(worker.Events[0].Message, "gate=test exit=0 command=go test ./...") {
		t.Fatalf("worker events = %#v", worker.Events)
	}
}

func TestCLIGateRecordRejectsMissingWorkerBeforeEvidenceWrite(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 15, 30, 0, 0, time.UTC)
	repo := t.TempDir()
	writeRepoHintsWithQualityGate(t, repo)
	state := filepath.Join(t.TempDir(), "state.json")
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	err := c.run([]string{"gate", "record", "--state", state, "--repo", repo, "--worker", "w-missing", "--gate", "test", "--exit-code", "0"})
	if err == nil {
		t.Fatal("gate record error = nil, want missing worker error")
	}
	if !strings.Contains(err.Error(), `worker "w-missing" not found`) {
		t.Fatalf("gate record error = %v", err)
	}
	evidence, listErr := store.NewJSONStore(state).ListGateEvidence()
	if listErr != nil {
		t.Fatalf("ListGateEvidence() error = %v", listErr)
	}
	if len(evidence) != 0 {
		t.Fatalf("evidence count = %d, want 0: %#v", len(evidence), evidence)
	}
}

func TestCLIValidateStartCreatesIssueLinkedPair(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 16, 0, 0, 0, time.UTC)
	repo := t.TempDir()
	state := filepath.Join(t.TempDir(), "state.json")
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"validate", "start", "--state", state, "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#15", "--prompt", "implement issue #15", "--gate", "test,vet"}); err != nil {
		t.Fatalf("validate start error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"validation issue=MTG-Thomas/codex-swarm#15 implementer=w-20260627-160000-",
		"validator=w-20260627-160000-",
		"status=pending",
		"cs gate record",
		"cs issue report --issue MTG-Thomas/codex-swarm#15 --worker",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("validate start output missing %q:\n%s", want, got)
		}
	}

	workers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	if len(workers) != 2 {
		t.Fatalf("workers count = %d, want 2: %#v", len(workers), workers)
	}
	var implementer, validator store.Worker
	for _, worker := range workers {
		switch worker.Role {
		case "implementer":
			implementer = worker
		case "validator":
			validator = worker
		}
	}
	if implementer.ID == "" || validator.ID == "" {
		t.Fatalf("implementer=%#v validator=%#v", implementer, validator)
	}
	if validator.ParentID != implementer.ID || validator.ValidationOf != implementer.ID || validator.ValidationStatus != ValidationPending {
		t.Fatalf("validator linkage = parent:%q validation_of:%q status:%q want implementer %q pending", validator.ParentID, validator.ValidationOf, validator.ValidationStatus, implementer.ID)
	}
	if implementer.Issue != "MTG-Thomas/codex-swarm#15" || validator.Issue != implementer.Issue {
		t.Fatalf("issues = implementer:%q validator:%q", implementer.Issue, validator.Issue)
	}
	if validator.Prompt == implementer.Prompt || !strings.Contains(validator.Prompt, "Required gates: test, vet") || !strings.Contains(validator.Prompt, "STATE_SNAPSHOT worker="+implementer.ID) {
		t.Fatalf("validator prompt = %q, implementer prompt = %q last = %q", validator.Prompt, implementer.Prompt, implementer.LastMessage)
	}

	out.Reset()
	if err := c.run([]string{"report", "--state", state, "--note", "rejected: missing test proof", validator.ID, "failed"}); err != nil {
		t.Fatalf("report validator rejection error = %v", err)
	}
	validator = mustGetWorker(t, state, validator.ID)
	if validator.ValidationStatus != ValidationRejected {
		t.Fatalf("ValidationStatus = %q, want %q", validator.ValidationStatus, ValidationRejected)
	}
}

func TestCLISpawnPrintsRepoHintsWhenConfigured(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 4, 0, 0, time.UTC)
	repo := t.TempDir()
	writeRepoHints(t, repo)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--repo", repo, "--prompt", "inspect repo"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `repo hint: remote devcontainer command: just talos-dev-run "just --list"`) {
		t.Fatalf("spawn output missing repo hint:\n%s", got)
	}
}

func TestCLISpawnAppserverWithWorktreeRunsInManagedWorktree(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 5, 0, 0, time.UTC)
	repo := initCLITestRepo(t)
	var runCWD string
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(_ context.Context, cwd, _ string) (appserver.RunResult, error) {
				runCWD = cwd
				return appserver.RunResult{ThreadID: "thread-1", TurnID: "turn-1", Status: "completed"}, nil
			},
		},
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", repo, "--worktree", "--prompt", "continue"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	worker := mustFindWorkerByPrompt(t, state, "continue")
	if worker.ProjectRoot != repo {
		t.Fatalf("ProjectRoot = %q, want canonical repo root %q", worker.ProjectRoot, repo)
	}
	if worker.Worktree == "" {
		t.Fatal("Worktree = empty")
	}
	if runCWD != worker.Worktree {
		t.Fatalf("RunTurn cwd = %q, want managed worktree %q", runCWD, worker.Worktree)
	}
	if _, err := os.Stat(filepath.Join(worker.Worktree, ".git")); err != nil {
		t.Fatalf("managed worktree missing .git: %v", err)
	}
}

func TestCLISpawnAppserverFailurePersistsWorkerState(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 7, 0, 0, time.UTC)
	repo := initCLITestRepo(t)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{}, errors.New("appserver unavailable")
			},
		},
	}
	state := filepath.Join(t.TempDir(), "state.json")

	err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", repo, "--worktree", "--prompt", "continue"})
	if err == nil {
		t.Fatal("spawn error = nil, want appserver failure")
	}
	worker := mustFindWorkerByPrompt(t, state, "continue")
	if worker.Worktree == "" {
		t.Fatal("Worktree = empty, want failed worker to retain managed worktree path")
	}
	if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayFailed {
		t.Fatalf("DeriveStatus() = %q, want failed", got)
	}
	if !strings.Contains(worker.LastMessage, "app-server spawn failed") {
		t.Fatalf("LastMessage = %q, want app-server spawn failed", worker.LastMessage)
	}
}

func TestCLISendAndResumeAppserverUseExistingWorktree(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 10, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	worktree := filepath.Join(repo, ".codex-swarm", "worktrees", "w-app")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("create worktree dir: %v", err)
	}
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-app",
		ProjectRoot: repo,
		Worktree:    worktree,
		ThreadID:    "thread-app",
		Engine:      "appserver",
		Status:      store.WorkerIdle,
		Prompt:      "app worker",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
	var sendCWD, resumeCWD string
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			sendTurn: func(_ context.Context, cwd, _, _ string) (appserver.RunResult, error) {
				sendCWD = cwd
				return appserver.RunResult{ThreadID: "thread-app", TurnID: "turn-send", Status: "completed"}, nil
			},
			resume: func(_ context.Context, cwd, _ string) (appserver.RunResult, error) {
				resumeCWD = cwd
				return appserver.RunResult{ThreadID: "thread-app", Status: "completed"}, nil
			},
		},
	}

	if err := c.run([]string{"send", "--state", state, "w-app", "continue"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	if err := c.run([]string{"resume", "--state", state, "w-app"}); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if sendCWD != worktree {
		t.Fatalf("SendTurn cwd = %q, want %q", sendCWD, worktree)
	}
	if resumeCWD != worktree {
		t.Fatalf("Resume cwd = %q, want %q", resumeCWD, worktree)
	}
}

func TestCLISendAppserverIncludesCompactSnapshot(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 18, 20, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	saveAppserverWorker(t, state, now)
	var sentPrompt string
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			sendTurn: func(_ context.Context, _, _, prompt string) (appserver.RunResult, error) {
				sentPrompt = prompt
				return appserver.RunResult{ThreadID: "thread-app", TurnID: "turn-send", Status: "completed"}, nil
			},
		},
	}

	if err := c.run([]string{"send", "--state", state, "w-app", "continue with verification"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	for _, want := range []string{
		"STATE_SNAPSHOT worker=w-app",
		"USER_MESSAGE\ncontinue with verification",
	} {
		if !strings.Contains(sentPrompt, want) {
			t.Fatalf("appserver prompt missing %q:\n%s", want, sentPrompt)
		}
	}
}

func TestCLISpawnAppserverIssueIncludesLaunchBundle(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 18, 35, 0, 0, time.UTC)
	repo := initCLITestRepo(t)
	writeRepoHintsWithQualityGate(t, repo)
	state := filepath.Join(t.TempDir(), "state.json")
	installFakeGH(t, fakeGHState{
		Title: "Build richer appserver bundles",
		Body:  "Acceptance criteria\n- Include issue context\n- Keep forbidden actions clear",
	})
	if err := store.NewJSONStore(state).SaveClaim(store.Claim{
		ID:     "c-existing",
		Repo:   repo,
		Scope:  "cmd/cs",
		Issue:  "MTG-Thomas/codex-swarm#9",
		Status: store.ClaimActive,
		Note:   "existing issue claim",
	}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	var runPrompt string
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(_ context.Context, _, prompt string) (appserver.RunResult, error) {
				runPrompt = prompt
				return appserver.RunResult{ThreadID: "thread-bundle", TurnID: "turn-bundle", Status: "completed"}, nil
			},
		},
	}

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", repo, "--worktree", "--issue", "MTG-Thomas/codex-swarm#9", "--prompt", "implement issue #9"}); err != nil {
		t.Fatalf("spawn appserver issue error = %v", err)
	}
	for _, want := range []string{
		"ISSUE_LAUNCH_BUNDLE",
		"issue=MTG-Thomas/codex-swarm#9",
		"url=https://github.com/MTG-Thomas/codex-swarm/issues/9",
		"title=Build richer appserver bundles",
		"Acceptance criteria",
		"repo=" + repo,
		"worktree=" + filepath.Join(repo, ".codex-swarm", "worktrees", "w-20260627-183500-"),
		"claim=c-existing status=active scope=cmd/cs",
		"repo hint: quality gate test: go test ./...",
		"required_verification=go test ./...",
		"forbidden=no merge, deploy, close, or destructive cleanup unless explicitly requested",
		"USER_TASK\nimplement issue #9",
	} {
		if !strings.Contains(runPrompt, want) {
			t.Fatalf("launch bundle missing %q:\n%s", want, runPrompt)
		}
	}
	worker := mustFindWorkerByPrompt(t, state, "implement issue #9")
	found := false
	for _, event := range worker.Events {
		if event.Type == "appserver.launch.bundle" && strings.Contains(event.Message, "issue=MTG-Thomas/codex-swarm#9") {
			found = true
		}
	}
	if !found {
		t.Fatalf("worker events missing launch bundle source reference: %#v", worker.Events)
	}
}

func TestCLISpawnAppserverIssueWithoutWorktreeOmitsCheckoutFields(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 18, 37, 0, 0, time.UTC)
	repo := initCLITestRepo(t)
	state := filepath.Join(t.TempDir(), "state.json")
	installFakeGH(t, fakeGHState{
		Title: "Build richer appserver bundles",
		Body:  "Acceptance criteria\n- Include issue context",
	})
	var runPrompt string
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(_ context.Context, _, prompt string) (appserver.RunResult, error) {
				runPrompt = prompt
				return appserver.RunResult{ThreadID: "thread-bundle", TurnID: "turn-bundle", Status: "completed"}, nil
			},
		},
	}

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#9", "--prompt", "implement issue #9"}); err != nil {
		t.Fatalf("spawn appserver issue error = %v", err)
	}
	for _, want := range []string{
		"ISSUE_LAUNCH_BUNDLE",
		"issue=MTG-Thomas/codex-swarm#9",
		"repo=" + repo,
		"USER_TASK\nimplement issue #9",
	} {
		if !strings.Contains(runPrompt, want) {
			t.Fatalf("launch bundle missing %q:\n%s", want, runPrompt)
		}
	}
	for _, notWant := range []string{"worktree=", "branch="} {
		if strings.Contains(runPrompt, notWant) {
			t.Fatalf("launch bundle unexpectedly contained %q:\n%s", notWant, runPrompt)
		}
	}
}

func TestCLISpawnAppserverIssueBundleFailurePersistsWorkerState(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 18, 38, 0, 0, time.UTC)
	repo := initCLITestRepo(t)
	state := filepath.Join(t.TempDir(), "state.json")
	installFakeGH(t, fakeGHState{IssueViewFail: "issue view unavailable"})
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{}, errors.New("RunTurn should not be called")
			},
		},
	}

	err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#9", "--prompt", "implement issue #9"})
	if err == nil {
		t.Fatal("spawn error = nil, want launch bundle failure")
	}
	for _, want := range []string{
		"build app-server launch bundle",
		"worker=w-20260627-183800-",
		"thread=mock-thread-w-20260627-183800-",
		"repo=" + repo,
		"issue=MTG-Thomas/codex-swarm#9",
		"issue view unavailable",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("spawn error missing %q:\n%v", want, err)
		}
	}
	worker := mustFindWorkerByPrompt(t, state, "implement issue #9")
	if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayFailed {
		t.Fatalf("DeriveStatus() = %q, want failed", got)
	}
	if !strings.Contains(worker.LastMessage, "app-server launch bundle failed:") || !strings.Contains(worker.LastMessage, "issue view unavailable") {
		t.Fatalf("LastMessage = %q, want launch bundle failure", worker.LastMessage)
	}
	found := false
	for _, event := range worker.Events {
		if event.Type == "appserver.launch.bundle.failed" && strings.Contains(event.Message, "issue view unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("worker events missing launch bundle failure: %#v", worker.Events)
	}
}

func TestCLISpawnAppserverWithoutIssueKeepsRawPrompt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 27, 18, 40, 0, 0, time.UTC)
	var runPrompt string
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(_ context.Context, _, prompt string) (appserver.RunResult, error) {
				runPrompt = prompt
				return appserver.RunResult{ThreadID: "thread-raw", TurnID: "turn-raw", Status: "completed"}, nil
			},
		},
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", ".", "--prompt", "raw appserver task"}); err != nil {
		t.Fatalf("spawn appserver non-issue error = %v", err)
	}
	if runPrompt != "raw appserver task" {
		t.Fatalf("RunTurn prompt = %q, want raw prompt", runPrompt)
	}
}

func TestCLISpawnAppserverFailedResultSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 15, 0, 0, time.UTC)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			runTurn: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{ThreadID: "thread-1", TurnID: "turn-1", Status: "failed"}, nil
			},
		},
	}
	state := filepath.Join(t.TempDir(), "state.json")

	if err := c.run([]string{"spawn", "--state", state, "--engine", "appserver", "--repo", ".", "--prompt", "continue"}); err != nil {
		t.Fatalf("spawn error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, mustFindWorkerByPrompt(t, state, "continue").ID, now)
	if got := out.String(); !strings.Contains(got, "status=failed") {
		t.Fatalf("spawn output = %q, want failed display status", got)
	}
}

func TestCLISendAppserverFailureSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 16, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	saveAppserverWorker(t, state, now)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			sendTurn: func(context.Context, string, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{}, errors.New("transport failed")
			},
		},
	}

	if err := c.run([]string{"send", "--state", state, "w-app", "continue"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, "w-app", now)
}

func TestCLISendAppserverFailedResultSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 17, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	saveAppserverWorker(t, state, now)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			sendTurn: func(context.Context, string, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{ThreadID: "thread-app", TurnID: "turn-failed", Status: "failed"}, nil
			},
		},
	}

	if err := c.run([]string{"send", "--state", state, "w-app", "continue"}); err != nil {
		t.Fatalf("send error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, "w-app", now)
}

func TestCLIResumeAppserverFailureSetsTerminatedAt(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 6, 26, 17, 30, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.json")
	saveAppserverWorker(t, state, now)
	c := cli{
		out: &out,
		err: &bytes.Buffer{},
		now: func() time.Time { return now },
		appserverRunner: fakeAppserverRunner{
			resume: func(context.Context, string, string) (appserver.RunResult, error) {
				return appserver.RunResult{}, errors.New("resume failed")
			},
		},
	}

	if err := c.run([]string{"resume", "--state", state, "w-app"}); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	assertWorkerTerminatedAt(t, state, "w-app", now)
}

func mustGetWorker(t *testing.T, state, id string) store.Worker {
	t.Helper()
	worker, err := store.NewJSONStore(state).GetWorker(id)
	if err != nil {
		t.Fatalf("GetWorker(%q) error = %v", id, err)
	}
	return worker
}

func mustFindWorkerByPrompt(t *testing.T, state, prompt string) store.Worker {
	t.Helper()
	workers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	for _, worker := range workers {
		if worker.Prompt == prompt {
			return worker
		}
	}
	t.Fatalf("worker with prompt %q not found in %#v", prompt, workers)
	return store.Worker{}
}

func saveAppserverWorker(t *testing.T, state string, now time.Time) {
	t.Helper()
	worker := store.Worker{
		ID:          "w-app",
		ProjectRoot: "/repo",
		ThreadID:    "thread-app",
		Engine:      "appserver",
		Status:      store.WorkerIdle,
		Prompt:      "app worker",
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}
	if err := store.NewJSONStore(state).SaveWorker(worker); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}
}

func savePairWorkers(t *testing.T, state string, now time.Time, issue string) {
	t.Helper()
	for _, worker := range []store.Worker{
		{
			ID:          "w-from",
			Issue:       issue,
			ProjectRoot: "/repo",
			ThreadID:    "thread-from",
			Engine:      "mock",
			Status:      store.WorkerIdle,
			Prompt:      "from worker",
			CreatedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Hour),
		},
		{
			ID:          "w-to",
			Issue:       issue,
			ProjectRoot: "/repo",
			ThreadID:    "thread-to",
			Engine:      "mock",
			Status:      store.WorkerIdle,
			Prompt:      "to worker",
			CreatedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Hour),
		},
	} {
		if err := store.NewJSONStore(state).SaveWorker(worker); err != nil {
			t.Fatalf("SaveWorker(%s) error = %v", worker.ID, err)
		}
	}
}

func initCLITestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runCLITestGit(t, root, "init")
	runCLITestGit(t, root, "config", "user.email", "test@example.com")
	runCLITestGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runCLITestGit(t, root, "add", "README.md")
	runCLITestGit(t, root, "commit", "-m", "initial")
	return root
}

func runCLITestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func writeRepoHints(t *testing.T, repo string) {
	t.Helper()
	body := `{
  "remote_devcontainer": {
    "command": "just talos-dev-run \"just --list\"",
    "image": "ghcr.io/mtg-thomas/bifrost-devcontainer:devcontainer-main-172fb07bd73f",
    "docs": "docs/devcontainer.md",
    "note": "No secrets are injected by default."
  }
}`
	if err := os.WriteFile(filepath.Join(repo, "codex-swarm.hints.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write repo hints: %v", err)
	}
}

func writeRepoHintsWithQualityGate(t *testing.T, repo string) {
	t.Helper()
	body := `{
  "quality_gates": [
    {
      "id": "test",
      "command": "go test ./...",
      "scope": "repo",
      "description": "unit test suite"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(repo, "codex-swarm.hints.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write repo hints: %v", err)
	}
}

func assertWorkerTerminatedAt(t *testing.T, state, id string, want time.Time) {
	t.Helper()
	worker := mustGetWorker(t, state, id)
	if worker.Lifecycle == nil {
		t.Fatal("Lifecycle = nil, want failed lifecycle")
	}
	if got := worker.Lifecycle.DeriveStatus(); got != lifecycle.DisplayFailed {
		t.Fatalf("DeriveStatus() = %q, want %q", got, lifecycle.DisplayFailed)
	}
	if worker.Lifecycle.Session.TerminatedAt == nil || !worker.Lifecycle.Session.TerminatedAt.Equal(want) {
		t.Fatalf("TerminatedAt = %v, want %v", worker.Lifecycle.Session.TerminatedAt, want)
	}
	if worker.Lifecycle.Session.CompletedAt != nil {
		t.Fatalf("CompletedAt = %v, want nil", worker.Lifecycle.Session.CompletedAt)
	}
}

type fakeAppserverRunner struct {
	runTurn  func(context.Context, string, string) (appserver.RunResult, error)
	sendTurn func(context.Context, string, string, string) (appserver.RunResult, error)
	resume   func(context.Context, string, string) (appserver.RunResult, error)
}

func (f fakeAppserverRunner) RunTurn(ctx context.Context, cwd, prompt string) (appserver.RunResult, error) {
	if f.runTurn == nil {
		return appserver.RunResult{}, errors.New("unexpected RunTurn")
	}
	return f.runTurn(ctx, cwd, prompt)
}

func (f fakeAppserverRunner) SendTurn(ctx context.Context, cwd, threadID, prompt string) (appserver.RunResult, error) {
	if f.sendTurn == nil {
		return appserver.RunResult{}, errors.New("unexpected SendTurn")
	}
	return f.sendTurn(ctx, cwd, threadID, prompt)
}

func (f fakeAppserverRunner) Resume(ctx context.Context, cwd, threadID string) (appserver.RunResult, error) {
	if f.resume == nil {
		return appserver.RunResult{}, errors.New("unexpected Resume")
	}
	return f.resume(ctx, cwd, threadID)
}
