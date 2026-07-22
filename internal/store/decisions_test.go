package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecordDecisionReplaysAndRejectsMismatchedRequest(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	st := NewJSONStore(state)
	now := time.Date(2026, 7, 22, 18, 20, 0, 0, time.UTC)
	input := Decision{
		ID: "d-1", RequestID: "request-1", Operation: "issue:mtg-thomas/codex-swarm#76",
		Repo: `C:\src\codex-swarm`, Issue: "mtg-thomas/codex-swarm#76", Summary: "Keep durable decisions",
		Rationale: "The operator needs explicit readback", AuthorWorker: "w-missing", CreatedAt: now,
		Evidence:       []DecisionEvidence{{Ref: "gate:g-1", State: DecisionEvidenceMissing, Detail: "gate evidence not found"}},
		ProvenanceGaps: []string{"author worker w-missing not found", "evidence gate:g-1 not found"},
	}
	created, replayed, err := st.RecordDecision(input)
	if err != nil || replayed {
		t.Fatalf("RecordDecision(create) = %#v, replayed=%t, err=%v", created, replayed, err)
	}
	if created.ID != "d-1" || !created.Current() || len(created.ProvenanceGaps) != 2 {
		t.Fatalf("created decision = %#v", created)
	}

	replay := input
	replay.ID = "d-generated-differently"
	replay.CreatedAt = now.Add(time.Hour)
	replay.Evidence[0].State = DecisionEvidenceAvailable
	replay.ProvenanceGaps = nil
	got, replayed, err := st.RecordDecision(replay)
	if err != nil || !replayed || got.ID != created.ID || got.Evidence[0].State != DecisionEvidenceMissing {
		t.Fatalf("RecordDecision(replay) = %#v, replayed=%t, err=%v", got, replayed, err)
	}

	mismatch := input
	mismatch.Summary = "different decision"
	if _, _, err := st.RecordDecision(mismatch); !errors.Is(err, ErrDecisionReplayMismatch) {
		t.Fatalf("RecordDecision(mismatch) error = %v, want ErrDecisionReplayMismatch", err)
	}
}

func TestRecordDecisionSupersedesAtomicallyAndPreservesHistory(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	st := NewJSONStore(state)
	now := time.Date(2026, 7, 22, 18, 30, 0, 0, time.UTC)
	first, _, err := st.RecordDecision(Decision{
		ID: "d-first", RequestID: "request-first", Operation: "worker:w-root", Repo: "/repo",
		Summary: "Use the first path", Rationale: "initial evidence", AuthorWorker: "w-root", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, replayed, err := st.RecordDecision(Decision{
		ID: "d-second", RequestID: "request-second", Summary: "Use the safer path", Rationale: "new evidence",
		AuthorWorker: "w-reviewer", SupersedesID: first.ID, CreatedAt: now.Add(time.Minute),
	})
	if err != nil || replayed {
		t.Fatalf("RecordDecision(supersede) = %#v, replayed=%t, err=%v", second, replayed, err)
	}
	if second.Operation != first.Operation || second.Repo != first.Repo || second.SupersedesID != first.ID || !second.Current() {
		t.Fatalf("superseding decision = %#v", second)
	}
	old, err := st.GetDecision(first.ID)
	if err != nil || old.SupersededByID != second.ID || old.SupersededAt == nil || !old.SupersededAt.Equal(second.CreatedAt) || old.Current() {
		t.Fatalf("old decision = %#v, err=%v", old, err)
	}

	_, _, err = st.RecordDecision(Decision{
		ID: "d-third", RequestID: "request-third", Summary: "third", Rationale: "competing successor",
		AuthorWorker: "w-third", SupersedesID: first.ID, CreatedAt: now.Add(2 * time.Minute),
	})
	if !errors.Is(err, ErrDecisionSuperseded) {
		t.Fatalf("second supersession error = %v, want ErrDecisionSuperseded", err)
	}
	history, err := st.ListDecisions(DecisionListFilter{Operation: first.Operation})
	if err != nil || len(history) != 2 || history[0].ID != second.ID || history[1].ID != first.ID {
		t.Fatalf("ListDecisions(history) = %#v, err=%v", history, err)
	}
	current, err := st.ListDecisions(DecisionListFilter{Operation: first.Operation, CurrentOnly: true})
	if err != nil || len(current) != 1 || current[0].ID != second.ID {
		t.Fatalf("ListDecisions(current) = %#v, err=%v", current, err)
	}
}

func TestListDecisionsFiltersByOperationRepoAndIssue(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	st := NewJSONStore(state)
	now := time.Date(2026, 7, 22, 18, 40, 0, 0, time.UTC)
	for _, decision := range []Decision{
		{ID: "d-a", RequestID: "r-a", Operation: "issue:owner/repo#1", Repo: `C:\Repo`, Issue: "owner/repo#1", Summary: "A", Rationale: "A reason", AuthorWorker: "w-a", CreatedAt: now},
		{ID: "d-b", RequestID: "r-b", Operation: "worker:w-b", Repo: `C:\Other`, Summary: "B", Rationale: "B reason", AuthorWorker: "w-b", CreatedAt: now.Add(time.Minute)},
	} {
		if _, _, err := st.RecordDecision(decision); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		filter DecisionListFilter
		want   string
	}{
		{"operation", DecisionListFilter{Operation: "worker:w-b"}, "d-b"},
		{"repository path normalization", DecisionListFilter{Repo: `c:\repo`}, "d-a"},
		{"issue", DecisionListFilter{Issue: "owner/repo#1"}, "d-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := st.ListDecisions(test.filter)
			if err != nil || len(got) != 1 || got[0].ID != test.want {
				t.Fatalf("ListDecisions(%#v) = %#v, err=%v", test.filter, got, err)
			}
		})
	}
}

func TestRecordDecisionValidatesBoundedExplicitInput(t *testing.T) {
	st := NewJSONStore(filepath.Join(t.TempDir(), "state.db"))
	base := Decision{ID: "d", RequestID: "r", Repo: "/repo", Summary: "summary", Rationale: "reason", AuthorWorker: "w", CreatedAt: time.Now().UTC()}
	for _, mutate := range []func(*Decision){
		func(d *Decision) { d.RequestID = "" },
		func(d *Decision) { d.Summary = "" },
		func(d *Decision) { d.Rationale = "" },
		func(d *Decision) { d.AuthorWorker = "" },
		func(d *Decision) { d.Evidence = []DecisionEvidence{{Ref: "x", State: "invented"}} },
	} {
		input := base
		mutate(&input)
		if _, _, err := st.RecordDecision(input); err == nil {
			t.Fatalf("RecordDecision(%#v) error = nil, want validation error", input)
		}
	}
}

func TestRecordDecisionConcurrentSupersessionKeepsOneCurrentSuccessor(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	now := time.Date(2026, 7, 22, 18, 50, 0, 0, time.UTC)
	first, _, err := NewJSONStore(state).RecordDecision(Decision{
		ID: "d-root", RequestID: "r-root", Operation: "worker:w-root", Summary: "root", Rationale: "root reason",
		AuthorWorker: "w-root", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		decision Decision
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i, id := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, _, err := NewJSONStore(state).RecordDecision(Decision{
				ID: "d-" + id, RequestID: "r-" + id, Summary: "successor " + id, Rationale: "new evidence",
				AuthorWorker: "w-" + id, SupersedesID: first.ID, CreatedAt: now.Add(time.Duration(i+1) * time.Second),
			})
			results <- result{decision: decision, err: err}
		}()
	}
	wg.Wait()
	close(results)
	successes, supersededErrors := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrDecisionSuperseded):
			supersededErrors++
		default:
			t.Fatalf("concurrent supersession error = %v", result.err)
		}
	}
	if successes != 1 || supersededErrors != 1 {
		t.Fatalf("concurrent results: successes=%d superseded=%d", successes, supersededErrors)
	}
	current, err := NewJSONStore(state).ListDecisions(DecisionListFilter{Operation: first.Operation, CurrentOnly: true})
	if err != nil || len(current) != 1 || current[0].SupersedesID != first.ID {
		t.Fatalf("current decision = %#v, err=%v", current, err)
	}
}
