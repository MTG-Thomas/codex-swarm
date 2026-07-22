package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestCLIDecisionRecordReplayListShowAndSupersede(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	repo := t.TempDir()
	now := time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC)
	st := store.NewJSONStore(state)
	if err := st.SaveWorker(store.Worker{ID: "w-author", Issue: "MTG-Thomas/codex-swarm#76", ProjectRoot: repo, Status: store.WorkerIdle, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveGateEvidence(store.GateEvidence{ID: "g-tests", GateID: "test", WorkerID: "w-author", Repo: repo, Command: "go test ./...", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	recordArgs := []string{"decision", "record", "--state", state, "--request-id", "decision-request", "--author", "w-author", "--summary", "Keep decisions explicit", "--rationale", "Auditable evidence survives task history", "--evidence", "gate:g-tests", "--dissent", "Revisit retention later"}
	if err := c.run(recordArgs); err != nil {
		t.Fatalf("decision record error = %v", err)
	}
	if !strings.Contains(out.String(), "current=true replayed=false") || !strings.Contains(out.String(), `operation="issue:mtg-thomas/codex-swarm#76"`) {
		t.Fatalf("decision record output = %q", out.String())
	}
	decisions, err := st.ListDecisions(store.DecisionListFilter{})
	if err != nil || len(decisions) != 1 {
		t.Fatalf("ListDecisions() = %#v, err=%v", decisions, err)
	}
	first := decisions[0]
	if first.Issue != "mtg-thomas/codex-swarm#76" || first.Repo != repo || first.Evidence[0].State != store.DecisionEvidenceAvailable || first.Dissent == "" {
		t.Fatalf("recorded decision = %#v", first)
	}

	if _, err := st.UpdateWorker("w-author", func(worker *store.Worker) error {
		worker.Issue = "MTG-Thomas/codex-swarm#75"
		worker.ProjectRoot = t.TempDir()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	out.Reset()
	if err := c.run(recordArgs); err != nil {
		t.Fatalf("decision replay error = %v", err)
	}
	if !strings.Contains(out.String(), "decision "+first.ID) || !strings.Contains(out.String(), "replayed=true") {
		t.Fatalf("decision replay output = %q", out.String())
	}

	out.Reset()
	if err := c.run([]string{"decision", "show", "--state", state, first.ID}); err != nil {
		t.Fatalf("decision show error = %v", err)
	}
	for _, want := range []string{`summary="Keep decisions explicit"`, `rationale="Auditable evidence survives task history"`, `dissent="Revisit retention later"`, `evidence="gate:g-tests"` + "\t" + `state="available"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("decision show output missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := c.run([]string{"decision", "list", "--state", state, "--issue", "MTG-Thomas/CODEX-SWARM#76", "--json"}); err != nil {
		t.Fatalf("decision list error = %v", err)
	}
	if !strings.Contains(out.String(), `"id": "`+first.ID+`"`) || !strings.Contains(out.String(), `"author_worker": "w-author"`) {
		t.Fatalf("decision list JSON = %s", out.String())
	}

	now = now.Add(time.Hour)
	out.Reset()
	if err := c.run([]string{"decision", "supersede", first.ID, "--state", state, "--request-id", "supersede-request", "--author", "w-author", "--summary", "Keep only bounded references", "--rationale", "Avoid transcript indexing"}); err != nil {
		t.Fatalf("decision supersede error = %v", err)
	}
	if !strings.Contains(out.String(), `supersedes="`+first.ID+`"`) {
		t.Fatalf("decision supersede output = %q", out.String())
	}
	current, err := st.ListDecisions(store.DecisionListFilter{Issue: "mtg-thomas/codex-swarm#76", CurrentOnly: true})
	if err != nil || len(current) != 1 || current[0].SupersedesID != first.ID || current[0].Dissent != first.Dissent {
		t.Fatalf("current decisions = %#v, err=%v", current, err)
	}
	old, err := st.GetDecision(first.ID)
	if err != nil || old.Current() || old.SupersededAt == nil {
		t.Fatalf("old decision = %#v, err=%v", old, err)
	}
}

func TestCLIDecisionPreservesMissingProvenance(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	repo := t.TempDir()
	now := time.Date(2026, 7, 22, 19, 30, 0, 0, time.UTC)
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := c.run([]string{
		"decision", "record", "--state", state, "--request-id", "missing-provenance", "--author", "w-missing",
		"--operation", "worker:w-missing", "--repo", repo, "--summary", "Proceed conditionally", "--rationale", "The missing records must stay visible",
		"--evidence", "gate:g-missing",
	}); err != nil {
		t.Fatalf("decision record error = %v", err)
	}
	decisions, err := store.NewJSONStore(state).ListDecisions(store.DecisionListFilter{})
	if err != nil || len(decisions) != 1 {
		t.Fatalf("ListDecisions() = %#v, err=%v", decisions, err)
	}
	decision := decisions[0]
	if len(decision.ProvenanceGaps) != 3 || decision.Evidence[0].Ref != "gate:g-missing" || decision.Evidence[0].State != store.DecisionEvidenceMissing {
		t.Fatalf("missing provenance decision = %#v", decision)
	}
	out.Reset()
	if err := c.run([]string{"decision", "show", "--state", state, decision.ID}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`provenance_gap="author_worker:w-missing not found"`, `provenance_gap="evidence:gate:g-missing not found"`, `provenance_gap="operation:worker:w-missing not present in current projection"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("decision show output missing %q:\n%s", want, out.String())
		}
	}
}

func TestCLIDecisionAllowsAuthorOnlyGapAndExplicitSupersessionClears(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	now := time.Date(2026, 7, 22, 19, 45, 0, 0, time.UTC)
	var out bytes.Buffer
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	if err := c.run([]string{
		"decision", "record", "--state", state, "--request-id", "author-only", "--author", "w-missing",
		"--summary", "Unscoped decision", "--rationale", "Scope is not yet known", "--evidence", "https://example.invalid/proof", "--dissent", "Needs follow-up",
	}); err != nil {
		t.Fatalf("author-only decision error = %v", err)
	}
	decisions, err := store.NewJSONStore(state).ListDecisions(store.DecisionListFilter{})
	if err != nil || len(decisions) != 1 || len(decisions[0].ProvenanceGaps) != 1 || decisions[0].Operation != "" || decisions[0].Repo != "" || decisions[0].Issue != "" {
		t.Fatalf("author-only decision = %#v, err=%v", decisions, err)
	}
	first := decisions[0]
	now = now.Add(time.Minute)
	if err := c.run([]string{
		"decision", "supersede", first.ID, "--state", state, "--request-id", "clear", "--summary", "Scoped later",
		"--rationale", "The concern was resolved", "--operation", "worker:w-missing", "--clear-evidence", "--clear-dissent",
	}); err != nil {
		t.Fatalf("clear supersession error = %v", err)
	}
	current, err := store.NewJSONStore(state).ListDecisions(store.DecisionListFilter{CurrentOnly: true})
	if err != nil || len(current) != 1 || len(current[0].Evidence) != 0 || current[0].Dissent != "" {
		t.Fatalf("cleared supersession = %#v, err=%v", current, err)
	}
}

func TestCLIDecisionRejectsMismatchedReplayAndInvalidInput(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	now := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	st := store.NewJSONStore(state)
	if err := st.SaveWorker(store.Worker{ID: "w-author", ProjectRoot: "/repo", Status: store.WorkerIdle, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	base := []string{"decision", "record", "--state", state, "--request-id", "same", "--author", "w-author", "--summary", "first", "--rationale", "reason"}
	if err := c.run(base); err != nil {
		t.Fatal(err)
	}
	changed := append([]string(nil), base...)
	changed[len(changed)-3] = "changed"
	if err := c.run(changed); !errors.Is(err, store.ErrDecisionReplayMismatch) {
		t.Fatalf("mismatched replay error = %v", err)
	}
	for _, args := range [][]string{
		{"decision", "record", "--state", state, "--summary", "missing author", "--rationale", "reason"},
		{"decision", "supersede", "--state", state, "missing", "--summary", "summary"},
		{"decision", "list", "--state", state, "--issue", "invalid"},
	} {
		if err := c.run(args); err == nil {
			t.Fatalf("c.run(%q) error = nil", args)
		}
	}
}
