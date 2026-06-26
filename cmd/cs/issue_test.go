package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-1",
		ProjectRoot: ".",
		ThreadID:    "thread-1",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "issue marker worker",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

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
	if snapshot.Schema != "codex-swarm.claims.v1" {
		t.Fatalf("snapshot schema = %q", snapshot.Schema)
	}
	if snapshot.SnapshotID == "" {
		t.Fatal("snapshot_id is empty")
	}
	if snapshot.MachineID == "" {
		t.Fatal("machine_id is empty")
	}
}

func TestExtractClaimSnapshotAcceptsLegacyMarker(t *testing.T) {
	body := claimMarkerStart + `
{
  "issue": "MTG-Thomas/codex-swarm#42",
  "generated_at": "2026-06-24T12:00:00Z",
  "claims": []
}
` + claimMarkerEnd

	snapshot, ok, err := extractClaimSnapshot(body)
	if err != nil {
		t.Fatalf("extractClaimSnapshot error = %v", err)
	}
	if !ok || snapshot.Issue != "MTG-Thomas/codex-swarm#42" {
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
			ID        string    `json:"id"`
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"createdAt"`
		}{{
			ID:        "new-id",
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

func TestLatestMarkerCommentID(t *testing.T) {
	oldBody, err := claimIssueMarkerMarkdown("MTG-Thomas/codex-swarm#42", nil, time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("old marker error = %v", err)
	}
	newBody, err := claimIssueMarkerMarkdown("MTG-Thomas/codex-swarm#42", nil, time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("new marker error = %v", err)
	}
	raw, err := json.Marshal(ghIssueView{
		Comments: []struct {
			ID        string    `json:"id"`
			Body      string    `json:"body"`
			CreatedAt time.Time `json:"createdAt"`
		}{
			{ID: "old-id", Body: oldBody, CreatedAt: time.Date(2026, 6, 24, 10, 5, 0, 0, time.UTC)},
			{ID: "noise-id", Body: "ordinary comment", CreatedAt: time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)},
			{ID: "new-id", Body: newBody, CreatedAt: time.Date(2026, 6, 24, 12, 5, 0, 0, time.UTC)},
		},
	})
	if err != nil {
		t.Fatalf("marshal issue view: %v", err)
	}
	got, err := latestMarkerCommentID(raw)
	if err != nil {
		t.Fatalf("latestMarkerCommentID error = %v", err)
	}
	if got != "new-id" {
		t.Fatalf("latestMarkerCommentID() = %q, want new-id", got)
	}
}

func TestImportClaimSnapshotSkipsNewerLocalByDefault(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	localUpdated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	remoteUpdated := localUpdated.Add(-time.Hour)
	if err := st.SaveClaim(store.Claim{
		ID:        "c-1",
		Issue:     "MTG-Thomas/codex-swarm#42",
		Status:    store.ClaimActive,
		Note:      "newer local",
		UpdatedAt: localUpdated,
	}); err != nil {
		t.Fatalf("save local claim: %v", err)
	}

	imported, skipped, err := importClaimSnapshot(st, "MTG-Thomas/codex-swarm#42", issueClaimSnapshot{
		Issue: "MTG-Thomas/codex-swarm#42",
		Claims: []store.Claim{{
			ID:        "c-1",
			Status:    store.ClaimReleased,
			Note:      "older remote",
			UpdatedAt: remoteUpdated,
		}},
	}, false)
	if err != nil {
		t.Fatalf("importClaimSnapshot error = %v", err)
	}
	if imported != 0 || skipped != 1 {
		t.Fatalf("imported=%d skipped=%d, want 0/1", imported, skipped)
	}
	got, err := st.GetClaim("c-1")
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.Note != "newer local" || got.Status != store.ClaimActive {
		t.Fatalf("claim was overwritten: %#v", got)
	}
}

func TestImportClaimSnapshotForceOverwritesNewerLocal(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	localUpdated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	remoteUpdated := localUpdated.Add(-time.Hour)
	if err := st.SaveClaim(store.Claim{
		ID:        "c-1",
		Issue:     "MTG-Thomas/codex-swarm#42",
		Status:    store.ClaimActive,
		Note:      "newer local",
		UpdatedAt: localUpdated,
	}); err != nil {
		t.Fatalf("save local claim: %v", err)
	}

	imported, skipped, err := importClaimSnapshot(st, "MTG-Thomas/codex-swarm#42", issueClaimSnapshot{
		Issue: "MTG-Thomas/codex-swarm#42",
		Claims: []store.Claim{{
			ID:        "c-1",
			Status:    store.ClaimReleased,
			Note:      "forced remote",
			UpdatedAt: remoteUpdated,
		}},
	}, true)
	if err != nil {
		t.Fatalf("importClaimSnapshot error = %v", err)
	}
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported=%d skipped=%d, want 1/0", imported, skipped)
	}
	got, err := st.GetClaim("c-1")
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.Note != "forced remote" || got.Status != store.ClaimReleased {
		t.Fatalf("claim was not overwritten: %#v", got)
	}
}

func TestImportClaimSnapshotMarksUnknownWorkerExternal(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	updated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	imported, skipped, err := importClaimSnapshot(st, "MTG-Thomas/codex-swarm#42", issueClaimSnapshot{
		Issue:     "MTG-Thomas/codex-swarm#42",
		MachineID: "remote-machine",
		Claims: []store.Claim{{
			ID:        "c-remote",
			WorkerID:  "w-remote",
			Status:    store.ClaimActive,
			Note:      "remote claim",
			UpdatedAt: updated,
		}},
	}, false)
	if err != nil {
		t.Fatalf("importClaimSnapshot error = %v", err)
	}
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported=%d skipped=%d, want 1/0", imported, skipped)
	}
	got, err := st.GetClaim("c-remote")
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.WorkerID != "w-remote" {
		t.Fatalf("WorkerID = %q, want historical remote worker id", got.WorkerID)
	}
	if !got.ExternalWorker || got.WorkerSource != "issue:remote-machine" {
		t.Fatalf("external provenance = external:%t source:%q, want issue remote machine", got.ExternalWorker, got.WorkerSource)
	}
	if got.Note != "remote claim" {
		t.Fatalf("Note = %q, want note preserved without external marker", got.Note)
	}
}

func TestImportClaimSnapshotKeepsKnownWorkerLocal(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	updated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	if err := st.SaveWorker(store.Worker{
		ID:          "w-local",
		ProjectRoot: "C:/repo",
		ThreadID:    "thread-local",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "local worker",
		CreatedAt:   updated,
		UpdatedAt:   updated,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	imported, skipped, err := importClaimSnapshot(st, "MTG-Thomas/codex-swarm#42", issueClaimSnapshot{
		Issue:     "MTG-Thomas/codex-swarm#42",
		MachineID: "remote-machine",
		Claims: []store.Claim{{
			ID:        "c-local",
			WorkerID:  "w-local",
			Repo:      "C:/repo",
			Status:    store.ClaimActive,
			Note:      "local claim",
			UpdatedAt: updated,
		}},
	}, false)
	if err != nil {
		t.Fatalf("importClaimSnapshot error = %v", err)
	}
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported=%d skipped=%d, want 1/0", imported, skipped)
	}
	got, err := st.GetClaim("c-local")
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.ExternalWorker || got.WorkerSource != "" {
		t.Fatalf("external provenance = external:%t source:%q, want local worker", got.ExternalWorker, got.WorkerSource)
	}
}

func TestImportClaimSnapshotMarksKnownWorkerWrongRepoExternal(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	updated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	if err := st.SaveWorker(store.Worker{
		ID:          "w-local",
		ProjectRoot: "C:/repo-a",
		ThreadID:    "thread-local",
		Engine:      "mock",
		Status:      store.WorkerIdle,
		Prompt:      "local worker",
		CreatedAt:   updated,
		UpdatedAt:   updated,
	}); err != nil {
		t.Fatalf("SaveWorker() error = %v", err)
	}

	imported, skipped, err := importClaimSnapshot(st, "MTG-Thomas/codex-swarm#42", issueClaimSnapshot{
		Issue:     "MTG-Thomas/codex-swarm#42",
		MachineID: "remote-machine",
		Claims: []store.Claim{{
			ID:        "c-wrong-repo",
			WorkerID:  "w-local",
			Repo:      "C:/repo-b",
			Status:    store.ClaimActive,
			Note:      "remote claim with colliding worker id",
			UpdatedAt: updated,
		}},
	}, false)
	if err != nil {
		t.Fatalf("importClaimSnapshot error = %v", err)
	}
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported=%d skipped=%d, want 1/0", imported, skipped)
	}
	got, err := st.GetClaim("c-wrong-repo")
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.WorkerID != "w-local" {
		t.Fatalf("WorkerID = %q, want historical worker id", got.WorkerID)
	}
	if !got.ExternalWorker || got.WorkerSource != "issue:remote-machine" {
		t.Fatalf("external provenance = external:%t source:%q, want issue remote machine", got.ExternalWorker, got.WorkerSource)
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

func TestIssueSyncUpdatesLatestMarkerCommentWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	ghStatePath := installFakeGH(t, fakeGHState{Body: "issue body"})
	issue := "MTG-Thomas/codex-swarm#42"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	st := store.NewJSONStore(state)
	if err := st.SaveClaim(store.Claim{
		ID:        "c-1",
		WorkerID:  "w-1",
		Repo:      `C:\repo`,
		Scope:     "cmd/cs",
		Issue:     issue,
		Status:    store.ClaimActive,
		Note:      "first note",
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save claim: %v", err)
	}

	if err := c.run([]string{"issue", "sync", "--state", state, "--issue", issue}); err != nil {
		t.Fatalf("first issue sync error = %v", err)
	}
	first := readFakeGHState(t, ghStatePath)
	if len(first.Comments) != 1 {
		t.Fatalf("comments after first sync = %d, want 1", len(first.Comments))
	}
	if err := st.SaveClaim(store.Claim{
		ID:        "c-1",
		WorkerID:  "w-1",
		Repo:      `C:\repo`,
		Scope:     "cmd/cs",
		Issue:     issue,
		Status:    store.ClaimActive,
		Note:      "updated note",
		ExpiresAt: now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("update claim: %v", err)
	}

	if err := c.run([]string{"issue", "sync", "--state", state, "--issue", issue}); err != nil {
		t.Fatalf("second issue sync error = %v", err)
	}
	got := readFakeGHState(t, ghStatePath)
	if len(got.Comments) != 1 {
		t.Fatalf("comments after second sync = %d, want 1", len(got.Comments))
	}
	if got.Comments[0].ID != first.Comments[0].ID {
		t.Fatalf("comment id changed from %q to %q", first.Comments[0].ID, got.Comments[0].ID)
	}
	if !strings.Contains(got.Comments[0].Body, "updated note") {
		t.Fatalf("updated marker body missing latest local claim:\n%s", got.Comments[0].Body)
	}
}

func TestIssuePullSkipsOlderRemoteClaimWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	issue := "MTG-Thomas/codex-swarm#42"
	localUpdated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	remoteUpdated := localUpdated.Add(-time.Hour)
	body, err := claimIssueMarkerMarkdown(issue, []store.Claim{{
		ID:        "c-1",
		Issue:     issue,
		Status:    store.ClaimReleased,
		Note:      "older remote",
		UpdatedAt: remoteUpdated,
	}}, remoteUpdated)
	if err != nil {
		t.Fatalf("marker error: %v", err)
	}
	installFakeGH(t, fakeGHState{Comments: []fakeGHComment{{
		ID:        "marker-1",
		Body:      body,
		CreatedAt: remoteUpdated,
	}}})
	st := store.NewJSONStore(state)
	if err := st.SaveClaim(store.Claim{
		ID:        "c-1",
		Issue:     issue,
		Status:    store.ClaimActive,
		Note:      "newer local",
		UpdatedAt: localUpdated,
	}); err != nil {
		t.Fatalf("save local claim: %v", err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return localUpdated }}

	if err := c.run([]string{"issue", "pull", "--state", state, "--issue", issue}); err != nil {
		t.Fatalf("issue pull error = %v", err)
	}
	if !strings.Contains(out.String(), "imported=0") || !strings.Contains(out.String(), "skipped=1") || !strings.Contains(out.String(), "conflicted=1") {
		t.Fatalf("pull output missing plan counts: %q", out.String())
	}
	got, err := st.GetClaim("c-1")
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.Note != "newer local" || got.Status != store.ClaimActive {
		t.Fatalf("newer local claim was overwritten: %#v", got)
	}
}

func TestIssuePullForceOverwritesNewerLocalWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	issue := "MTG-Thomas/codex-swarm#42"
	localUpdated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	remoteUpdated := localUpdated.Add(-time.Hour)
	body, err := claimIssueMarkerMarkdown(issue, []store.Claim{{
		ID:        "c-1",
		Issue:     issue,
		Status:    store.ClaimReleased,
		Note:      "forced remote",
		UpdatedAt: remoteUpdated,
	}}, remoteUpdated)
	if err != nil {
		t.Fatalf("marker error: %v", err)
	}
	installFakeGH(t, fakeGHState{Comments: []fakeGHComment{{
		ID:        "marker-1",
		Body:      body,
		CreatedAt: remoteUpdated,
	}}})
	st := store.NewJSONStore(state)
	if err := st.SaveClaim(store.Claim{
		ID:        "c-1",
		Issue:     issue,
		Status:    store.ClaimActive,
		Note:      "newer local",
		UpdatedAt: localUpdated,
	}); err != nil {
		t.Fatalf("save local claim: %v", err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return localUpdated }}

	if err := c.run([]string{"issue", "pull", "--force", "--state", state, "--issue", issue}); err != nil {
		t.Fatalf("issue pull --force error = %v", err)
	}
	if !strings.Contains(out.String(), "imported=1") || !strings.Contains(out.String(), "skipped=0") || !strings.Contains(out.String(), "conflicted=0") {
		t.Fatalf("pull output missing force counts: %q", out.String())
	}
	got, err := st.GetClaim("c-1")
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if got.Note != "forced remote" || got.Status != store.ClaimReleased {
		t.Fatalf("force did not overwrite local claim: %#v", got)
	}
}

func TestIssuePullDryRunPrintsPlanAndDoesNotWriteWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	issue := "MTG-Thomas/codex-swarm#42"
	localUpdated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	remoteUpdated := localUpdated.Add(-time.Hour)
	body, err := claimIssueMarkerMarkdown(issue, []store.Claim{
		{
			ID:        "c-1",
			Issue:     issue,
			Status:    store.ClaimReleased,
			Note:      "older remote",
			UpdatedAt: remoteUpdated,
		},
		{
			ID:        "c-2",
			Issue:     issue,
			Status:    store.ClaimActive,
			Note:      "new remote",
			UpdatedAt: remoteUpdated,
		},
	}, remoteUpdated)
	if err != nil {
		t.Fatalf("marker error: %v", err)
	}
	installFakeGH(t, fakeGHState{Comments: []fakeGHComment{{
		ID:        "marker-1",
		Body:      body,
		CreatedAt: remoteUpdated,
	}}})
	st := store.NewJSONStore(state)
	if err := st.SaveClaim(store.Claim{
		ID:        "c-1",
		Issue:     issue,
		Status:    store.ClaimActive,
		Note:      "newer local",
		UpdatedAt: localUpdated,
	}); err != nil {
		t.Fatalf("save local claim: %v", err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return localUpdated }}

	if err := c.run([]string{"issue", "pull", "--dry-run", "--state", state, "--issue", issue}); err != nil {
		t.Fatalf("issue pull --dry-run error = %v", err)
	}
	for _, want := range []string{"dry-run", "imported=1", "skipped=1", "conflicted=1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q: %q", want, out.String())
		}
	}
	got, err := st.GetClaim("c-1")
	if err != nil {
		t.Fatalf("get local claim: %v", err)
	}
	if got.Note != "newer local" || got.Status != store.ClaimActive {
		t.Fatalf("dry-run overwrote local claim: %#v", got)
	}
	if _, err := st.GetClaim("c-2"); err == nil {
		t.Fatal("dry-run imported c-2")
	}
}

func TestIssueReportPostsWorkerReportWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	ghStatePath := installFakeGH(t, fakeGHState{})
	issue := "MTG-Thomas/codex-swarm#42"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	if err := store.NewJSONStore(state).SaveWorker(store.Worker{
		ID:          "w-1",
		Issue:       issue,
		ProjectRoot: `C:\repo`,
		ThreadID:    "mock-thread-w-1",
		Engine:      "mock",
		Status:      store.WorkerDone,
		Report:      "implemented and verified",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("save worker: %v", err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"issue", "report", "--state", state, "--issue", issue, "--worker", "w-1"}); err != nil {
		t.Fatalf("issue report error = %v", err)
	}
	got := readFakeGHState(t, ghStatePath)
	if len(got.Comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(got.Comments))
	}
	for _, want := range []string{"codex-swarm worker report", "- Worker: `w-1`", "implemented and verified"} {
		if !strings.Contains(got.Comments[0].Body, want) {
			t.Fatalf("posted report missing %q:\n%s", want, got.Comments[0].Body)
		}
	}
}

type fakeGHState struct {
	Body     string          `json:"body"`
	Comments []fakeGHComment `json:"comments"`
	Calls    []string        `json:"calls,omitempty"`
}

type fakeGHComment struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

func installFakeGH(t *testing.T, initial fakeGHState) string {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "fake-gh-state.json")
	writeFakeGHState(t, statePath, initial)
	sourcePath := filepath.Join(dir, "fake-gh.go")
	if err := os.WriteFile(sourcePath, []byte(fakeGHSource), 0o600); err != nil {
		t.Fatalf("write fake gh source: %v", err)
	}
	exeName := "gh"
	if runtime.GOOS == "windows" {
		exeName = "gh.exe"
	}
	exePath := filepath.Join(dir, exeName)
	cmd := exec.Command("go", "build", "-o", exePath, sourcePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake gh: %v\n%s", err, output)
	}
	t.Setenv("FAKE_GH_STATE", statePath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return statePath
}

func writeFakeGHState(t *testing.T, path string, state fakeGHState) {
	t.Helper()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal fake gh state: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fake gh state: %v", err)
	}
}

func readFakeGHState(t *testing.T, path string) fakeGHState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake gh state: %v", err)
	}
	var state fakeGHState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("parse fake gh state: %v", err)
	}
	return state
}

const fakeGHSource = `package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type state struct {
	Body     string    ` + "`json:\"body\"`" + `
	Comments []comment ` + "`json:\"comments\"`" + `
	Calls    []string  ` + "`json:\"calls,omitempty\"`" + `
}

type comment struct {
	ID        string    ` + "`json:\"id\"`" + `
	Body      string    ` + "`json:\"body\"`" + `
	CreatedAt time.Time ` + "`json:\"createdAt\"`" + `
}

func main() {
	path := os.Getenv("FAKE_GH_STATE")
	if path == "" {
		fail("FAKE_GH_STATE is required")
	}
	st := read(path)
	args := os.Args[1:]
	switch {
	case len(args) >= 2 && args[0] == "issue" && args[1] == "view":
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Body     string    ` + "`json:\"body\"`" + `
			Comments []comment ` + "`json:\"comments\"`" + `
		}{Body: st.Body, Comments: st.Comments}); err != nil {
			fail(err.Error())
		}
	case len(args) >= 2 && args[0] == "issue" && args[1] == "comment":
		body := flagValue(args, "--body")
		st.Calls = append(st.Calls, "comment")
		st.Comments = append(st.Comments, comment{
			ID:        fmt.Sprintf("fake-comment-%d", len(st.Comments)+1),
			Body:      body,
			CreatedAt: time.Date(2026, 6, 24, 12, len(st.Comments), 0, 0, time.UTC),
		})
		write(path, st)
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql":
		id := fieldValue(args, "id=")
		body := fieldValue(args, "body=")
		if id == "" {
			fail("missing id field")
		}
		for i := range st.Comments {
			if st.Comments[i].ID == id {
				st.Comments[i].Body = body
				st.Calls = append(st.Calls, "update:"+id)
				write(path, st)
				return
			}
		}
		fail("comment not found: " + id)
	default:
		fail("unsupported gh args: " + strings.Join(args, " "))
	}
}

func read(path string) state {
	data, err := os.ReadFile(path)
	if err != nil {
		fail(err.Error())
	}
	var st state
	if len(data) > 0 {
		if err := json.Unmarshal(data, &st); err != nil {
			fail(err.Error())
		}
	}
	return st
}

func write(path string, st state) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		fail(err.Error())
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		fail(err.Error())
	}
}

func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func fieldValue(args []string, prefix string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-f" && strings.HasPrefix(args[i+1], prefix) {
			return strings.TrimPrefix(args[i+1], prefix)
		}
	}
	return ""
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
`
