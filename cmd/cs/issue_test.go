package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestIssueExportIncludesParsableClaimMarker(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	t.Setenv("CODEX_SWARM_MACHINE_ID", "opaque-machine")
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
	if snapshot.MachineID != "opaque-machine" {
		t.Fatalf("machine_id = %q, want opaque-machine", snapshot.MachineID)
	}
}

func TestIssueExportOmitsMachineIDByDefault(t *testing.T) {
	body, err := claimIssueMarkerMarkdown("MTG-Thomas/codex-swarm#42", nil, time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("claimIssueMarkerMarkdown error = %v", err)
	}
	snapshot, ok, err := extractClaimSnapshot(body)
	if err != nil {
		t.Fatalf("extractClaimSnapshot error = %v", err)
	}
	if !ok {
		t.Fatal("extractClaimSnapshot ok = false")
	}
	if snapshot.MachineID != "" {
		t.Fatalf("machine_id = %q, want empty default", snapshot.MachineID)
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

func TestImportClaimSnapshotRejectsClaimForDifferentIssue(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	st := store.NewJSONStore(state)
	updated := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	_, _, err := importClaimSnapshot(st, "MTG-Thomas/codex-swarm#42", issueClaimSnapshot{
		Issue: "MTG-Thomas/codex-swarm#42",
		Claims: []store.Claim{{
			ID:        "c-foreign",
			Issue:     "MTG-Thomas/codex-swarm#99",
			Repo:      "C:/repo",
			Status:    store.ClaimActive,
			UpdatedAt: updated,
		}},
	}, false)
	if err == nil {
		t.Fatal("importClaimSnapshot error = nil, want foreign issue rejection")
	}
	if !strings.Contains(err.Error(), "expected MTG-Thomas/codex-swarm#42") {
		t.Fatalf("importClaimSnapshot error = %v", err)
	}
	if _, getErr := st.GetClaim("c-foreign"); getErr == nil {
		t.Fatal("foreign-issue claim was imported")
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

func TestWorkerIssueReportMarkdownIncludesValidatorState(t *testing.T) {
	now := time.Date(2026, 6, 27, 16, 15, 0, 0, time.UTC)
	body := workerIssueReportMarkdown("MTG-Thomas/codex-swarm#15", store.Worker{
		ID:               "w-validator",
		Role:             "validator",
		Issue:            "MTG-Thomas/codex-swarm#15",
		ValidationOf:     "w-implementer",
		ValidationStatus: ValidationRejected,
		Status:           store.WorkerFailed,
		Engine:           "mock",
		Report:           "rejected: missing gate evidence",
	}, "", now)
	for _, want := range []string{
		"- Role: `validator`",
		"- Validation of: `w-implementer`",
		"- Validation status: `rejected`",
		"rejected: missing gate evidence",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("validator report markdown missing %q:\n%s", want, body)
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

func TestIssueReadyReportsReadyIssueWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	writeIssueReadyRepoHints(t, repo)
	installFakeGH(t, fakeGHState{
		Title: "Add issue dispatch readiness",
		Body:  "Acceptance criteria",
	})
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: time.Now}

	if err := c.run([]string{"issue", "ready", "--state", state, "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#18"}); err != nil {
		t.Fatalf("issue ready error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"ready=true issue=MTG-Thomas/codex-swarm#18",
		"blockers=0",
		"title=Add issue dispatch readiness",
		"gate=test scope=repo command=go test ./...",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("issue ready output missing %q:\n%s", want, got)
		}
	}
}

func TestIssueReadyJSONReportsMissingBodyWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	writeIssueReadyRepoHints(t, repo)
	installFakeGH(t, fakeGHState{Title: "Missing body"})
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: time.Now}

	if err := c.run([]string{"issue", "ready", "--json", "--state", state, "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#18"}); err != nil {
		t.Fatalf("issue ready --json error = %v", err)
	}
	var report struct {
		Ready    bool     `json:"ready"`
		Blockers []string `json:"blockers"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("readiness JSON parse error = %v\n%s", err, out.String())
	}
	if report.Ready {
		t.Fatal("Ready = true, want false")
	}
	if len(report.Blockers) != 1 || report.Blockers[0] != "issue body is missing" {
		t.Fatalf("Blockers = %#v", report.Blockers)
	}
}

func TestIssueReadyReportsOpenClaimWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	writeIssueReadyRepoHints(t, repo)
	installFakeGH(t, fakeGHState{
		Title: "Claimed issue",
		Body:  "Body",
	})
	now := time.Date(2026, 6, 27, 15, 30, 0, 0, time.UTC)
	if err := store.NewJSONStore(state).SaveClaim(store.Claim{
		ID:        "c-ready",
		Issue:     "MTG-Thomas/codex-swarm#18",
		Repo:      repo,
		Scope:     "cmd/cs",
		Status:    store.ClaimActive,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"issue", "ready", "--state", state, "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#18"}); err != nil {
		t.Fatalf("issue ready error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"ready=false",
		"claim=c-ready status=active",
		"blocker=issue has open claim c-ready",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("issue ready output missing %q:\n%s", want, got)
		}
	}
}

func TestIssueReadyUsesDaemonURL(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	writeIssueReadyRepoHints(t, repo)
	server := httptest.NewServer(daemon.NewServerWithIssueProvider(state, store.NewJSONStore(state), issueReadyProvider{
		issue: readiness.Issue{
			Title: "Daemon ready issue",
			Body:  "Acceptance criteria",
		},
	}).Handler())
	defer server.Close()
	t.Setenv("CODEX_SWARM_DAEMON_URL", server.URL)
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: time.Now}

	if err := c.run([]string{"issue", "ready", "--state", filepath.Join(t.TempDir(), "ignored.json"), "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#27"}); err != nil {
		t.Fatalf("issue ready daemon error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"ready=true issue=MTG-Thomas/codex-swarm#27",
		"title=Daemon ready issue",
		"gate=test scope=repo command=go test ./...",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("daemon issue ready output missing %q:\n%s", want, got)
		}
	}
}

func TestIssueReadyRejectsMalformedIssue(t *testing.T) {
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: time.Now}

	err := c.run([]string{"issue", "ready", "--repo", ".", "--issue", "not-an-issue"})
	if err == nil {
		t.Fatal("issue ready error = nil, want malformed issue error")
	}
	if !strings.Contains(err.Error(), "issue reference must look like owner/repo#123") {
		t.Fatalf("issue ready error = %v", err)
	}
}

func TestIssueDispatchCreatesImplementerValidatorWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	writeIssueReadyRepoHints(t, repo)
	installFakeGH(t, fakeGHState{
		Title: "Dispatch issue",
		Body:  "Acceptance criteria",
	})
	now := time.Date(2026, 6, 27, 16, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}

	if err := c.run([]string{"issue", "dispatch", "--state", state, "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#24", "--prompt", "implement issue #24", "--gate", "test"}); err != nil {
		t.Fatalf("issue dispatch error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"dispatch issue=MTG-Thomas/codex-swarm#24",
		"request=dispatch-",
		"replayed=false",
		"implementer: cs send",
		"gate: cs gate record --repo " + repo,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("issue dispatch output missing %q:\n%s", want, got)
		}
	}
	workers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	implementer, validator := dispatchWorkersByRole(t, workers)
	if implementer.Issue != "MTG-Thomas/codex-swarm#24" || validator.Issue != "MTG-Thomas/codex-swarm#24" {
		t.Fatalf("worker issues = %q/%q", implementer.Issue, validator.Issue)
	}
	if validator.ValidationOf != implementer.ID {
		t.Fatalf("validator.ValidationOf = %q, want %q", validator.ValidationOf, implementer.ID)
	}
	requestID := dispatchRequestID(t, implementer)
	if got := dispatchRequestID(t, validator); got != requestID {
		t.Fatalf("validator request id = %q, want %q", got, requestID)
	}
}

func TestIssueDispatchReplaysExistingRequestWithFakeGH(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	writeIssueReadyRepoHints(t, repo)
	installFakeGH(t, fakeGHState{
		Title: "Dispatch issue",
		Body:  "Acceptance criteria",
	})
	now := time.Date(2026, 6, 27, 16, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	args := []string{"issue", "dispatch", "--state", state, "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#24", "--prompt", "implement issue #24", "--gate", "test"}

	if err := c.run(args); err != nil {
		t.Fatalf("first issue dispatch error = %v", err)
	}
	firstWorkers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers(first) error = %v", err)
	}
	firstImplementer, firstValidator := dispatchWorkersByRole(t, firstWorkers)
	out.Reset()
	if err := c.run(args); err != nil {
		t.Fatalf("second issue dispatch error = %v", err)
	}
	if !strings.Contains(out.String(), "replayed=true") {
		t.Fatalf("second dispatch output missing replayed=true:\n%s", out.String())
	}
	secondWorkers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers(second) error = %v", err)
	}
	if len(secondWorkers) != 2 {
		t.Fatalf("workers after replay = %d, want 2", len(secondWorkers))
	}
	secondImplementer, secondValidator := dispatchWorkersByRole(t, secondWorkers)
	if firstImplementer.ID != secondImplementer.ID || firstValidator.ID != secondValidator.ID {
		t.Fatalf("replay worker ids = %s/%s then %s/%s", firstImplementer.ID, firstValidator.ID, secondImplementer.ID, secondValidator.ID)
	}
}

func TestIssueDispatchBlockedReadinessDoesNotCreateWorkers(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo := t.TempDir()
	writeIssueReadyRepoHints(t, repo)
	installFakeGH(t, fakeGHState{
		Title: "Claimed issue",
		Body:  "Acceptance criteria",
	})
	now := time.Date(2026, 6, 27, 16, 0, 0, 0, time.UTC)
	if err := store.NewJSONStore(state).SaveClaim(store.Claim{
		ID:        "c-dispatch",
		Issue:     "MTG-Thomas/codex-swarm#24",
		Repo:      repo,
		Scope:     "issue #24",
		Status:    store.ClaimActive,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveClaim() error = %v", err)
	}
	var out, stderr bytes.Buffer
	c := cli{out: &out, err: &stderr, now: func() time.Time { return now }}

	err := c.run([]string{"issue", "dispatch", "--state", state, "--repo", repo, "--issue", "MTG-Thomas/codex-swarm#24", "--prompt", "implement issue #24", "--gate", "test"})
	if err == nil {
		t.Fatal("issue dispatch error = nil, want blocked readiness error")
	}
	if out.String() != "" {
		t.Fatalf("blocked dispatch stdout = %q, want empty", out.String())
	}
	got := stderr.String()
	for _, want := range []string{"ready=false", "blocker=issue has open claim c-dispatch"} {
		if !strings.Contains(got, want) {
			t.Fatalf("blocked dispatch output missing %q:\n%s", want, got)
		}
	}
	workers, err := store.NewJSONStore(state).ListWorkers()
	if err != nil {
		t.Fatalf("ListWorkers() error = %v", err)
	}
	if len(workers) != 0 {
		t.Fatalf("workers after blocked dispatch = %d, want 0", len(workers))
	}
}

func dispatchWorkersByRole(t *testing.T, workers []store.Worker) (store.Worker, store.Worker) {
	t.Helper()
	if len(workers) != 2 {
		t.Fatalf("workers = %d, want 2", len(workers))
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
		t.Fatalf("workers missing implementer/validator roles: %#v", workers)
	}
	return implementer, validator
}

func dispatchRequestID(t *testing.T, worker store.Worker) string {
	t.Helper()
	for _, event := range worker.Events {
		if event.Type == "issue.dispatch" {
			if event.RequestID == "" {
				t.Fatalf("worker %s has empty dispatch request id", worker.ID)
			}
			return event.RequestID
		}
	}
	t.Fatalf("worker %s has no issue.dispatch event", worker.ID)
	return ""
}

type issueReadyProvider struct {
	issue readiness.Issue
}

func (p issueReadyProvider) IssueMetadata(ctx context.Context, issue string) (readiness.Issue, error) {
	got := p.issue
	got.Ref = issue
	return got, nil
}

type fakeGHState struct {
	Title    string          `json:"title,omitempty"`
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

func writeIssueReadyRepoHints(t *testing.T, repo string) {
	t.Helper()
	body := `{
  "quality_gates": [
    {
      "id": "test",
      "command": "go test ./...",
      "scope": "repo"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(repo, "codex-swarm.hints.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write repo hints: %v", err)
	}
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
	Title    string    ` + "`json:\"title,omitempty\"`" + `
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
		response := map[string]any{}
		for _, field := range strings.Split(flagValue(args, "--json"), ",") {
			switch strings.TrimSpace(field) {
			case "title":
				response["title"] = st.Title
			case "body":
				response["body"] = st.Body
			case "comments":
				response["comments"] = st.Comments
			}
		}
		if len(response) == 0 {
			response["body"] = st.Body
			response["comments"] = st.Comments
		}
		if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
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
