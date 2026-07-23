package operation

import (
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestNormalizeKey(t *testing.T) {
	for input, want := range map[string]string{
		" ISSUE:Owner/Repo#007 ": "issue:owner/repo#7",
		"WORKER:w-123":           "worker:w-123",
	} {
		got, err := NormalizeKey(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeKey(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "worker:", "room:anything", "issue:bad"} {
		if _, err := NormalizeKey(input); err == nil {
			t.Fatalf("NormalizeKey(%q) error = nil", input)
		}
	}
}

func TestResolveWorkersUsesIssueThenRootAndDegradesBrokenLineage(t *testing.T) {
	workers := []store.Worker{
		{ID: "root", Issue: " MTG-Thomas/Codex-Swarm#075 "},
		{ID: "child", ParentID: "root"},
		{ID: "nested", ParentID: "child", Issue: "Owner/Repo#9"},
		{ID: "plain-root"},
		{ID: "plain-child", ParentID: "plain-root"},
		{ID: "missing", ParentID: "gone"},
		{ID: "invalid", Issue: "not-an-issue"},
		{ID: "cycle-a", ParentID: "cycle-b"},
		{ID: "cycle-b", ParentID: "cycle-a"},
	}
	got := ResolveWorkers(workers)
	assertResolution(t, got["root"], "issue:mtg-thomas/codex-swarm#75", StateResolved)
	assertResolution(t, got["child"], "issue:mtg-thomas/codex-swarm#75", StateResolved)
	assertResolution(t, got["nested"], "issue:owner/repo#9", StateResolved)
	assertResolution(t, got["plain-child"], "worker:plain-root", StateResolved)
	assertResolution(t, got["missing"], "", StateMissingParent)
	assertResolution(t, got["invalid"], "", StateInvalidIssue)
	assertResolution(t, got["cycle-a"], "", StateParentCycle)
	assertResolution(t, got["cycle-b"], "", StateParentCycle)
}

func TestDeriveGroupsExistingRelationshipsAndLeavesUnsupportedRecordsUnscoped(t *testing.T) {
	now := time.Date(2026, 7, 22, 18, 0, 0, 0, time.UTC)
	workers := []store.Worker{
		{ID: "root", Issue: "Owner/Repo#7", ThreadID: "thread-one", HostID: "local", PullRequests: []store.PullRequestState{{URL: "https://github.test/pr/1", UpdatedAt: now}}},
		{ID: "child", ParentID: "root", ThreadID: "thread-two"},
		{ID: "other", ThreadID: "thread-shared", HostID: "remote"},
		{ID: "broken", ParentID: "missing", PullRequests: []store.PullRequestState{{URL: "https://github.test/pr/broken"}}},
	}
	message := store.DeliveredMessage{
		Message:  store.Message{ID: "m1", From: "root"},
		Delivery: store.Delivery{ID: "d1", RecipientID: "other"},
	}
	view := Derive(Input{
		Workers: workers,
		Claims: []store.Claim{
			{ID: "claim-worker", WorkerID: "child"},
			{ID: "claim-issue", Issue: "OWNER/REPO#7"},
			{ID: "claim-unlinked"},
		},
		Messages:     []store.DeliveredMessage{message},
		GateEvidence: []store.GateEvidence{{ID: "gate-linked", WorkerID: "child"}, {ID: "gate-unlinked"}},
		CodexTasks: []store.CodexTask{
			{HostID: "local", ThreadID: "thread-one"},
			{HostID: "remote", ThreadID: "thread-shared"},
			{HostID: "local", ThreadID: "unknown"},
		},
	})
	if len(view.Operations) != 2 {
		t.Fatalf("operations = %#v, want two", view.Operations)
	}
	issue := operationByKey(t, view, "issue:owner/repo#7")
	if len(issue.Workers) != 2 || len(issue.Claims) != 2 || len(issue.Messages) != 1 || len(issue.GateEvidence) != 1 || len(issue.PullRequests) != 1 || len(issue.CodexTasks) != 1 {
		t.Fatalf("issue operation counts = workers:%d claims:%d messages:%d gates:%d prs:%d tasks:%d", len(issue.Workers), len(issue.Claims), len(issue.Messages), len(issue.GateEvidence), len(issue.PullRequests), len(issue.CodexTasks))
	}
	other := operationByKey(t, view, "worker:other")
	if len(other.Messages) != 1 || len(other.CodexTasks) != 1 {
		t.Fatalf("cross-operation records missing: %#v", other)
	}
	assertUnscoped(t, view, "worker", "broken", StateMissingParent)
	assertUnscoped(t, view, "pull_request", "https://github.test/pr/broken", StateMissingParent)
	assertUnscoped(t, view, "claim", "claim-unlinked", StateUnlinked)
	assertUnscoped(t, view, "gate", "gate-unlinked", StateUnlinked)
	assertUnscoped(t, view, "codex_task", "local/unknown", StateUnlinked)
}

func TestDeriveClaimIssuePrecedenceAndTaskHostDisambiguation(t *testing.T) {
	view := Derive(Input{
		Workers: []store.Worker{
			{ID: "local-worker", Issue: "owner/local#1", HostID: "local", ThreadID: "same"},
			{ID: "remote-worker", Issue: "owner/remote#2", HostID: "remote", ThreadID: "same"},
		},
		Claims:     []store.Claim{{ID: "claim", WorkerID: "local-worker", Issue: "owner/explicit#3"}},
		CodexTasks: []store.CodexTask{{HostID: "remote", ThreadID: "same"}},
	})
	if len(operationByKey(t, view, "issue:owner/explicit#3").Claims) != 1 {
		t.Fatal("explicit claim issue did not take precedence")
	}
	if len(operationByKey(t, view, "issue:owner/remote#2").CodexTasks) != 1 {
		t.Fatal("task did not use exact host/thread match")
	}
	if len(operationByKey(t, view, "issue:owner/local#1").CodexTasks) != 0 {
		t.Fatal("task crossed host boundary")
	}
}

func TestDeriveDoesNotGuessTaskOperationFromHostlessWorker(t *testing.T) {
	view := Derive(Input{
		Workers:    []store.Worker{{ID: "legacy", ThreadID: "same"}},
		CodexTasks: []store.CodexTask{{HostID: "remote", ThreadID: "same"}},
	})
	if len(operationByKey(t, view, "worker:legacy").CodexTasks) != 0 {
		t.Fatal("hostless worker was treated as an exact task identity")
	}
	assertUnscoped(t, view, "codex_task", "remote/same", StateUnlinked)
}

func assertResolution(t *testing.T, got Resolution, key, state string) {
	t.Helper()
	if got.Key != key || got.State != state {
		t.Fatalf("resolution = %#v, want key=%q state=%q", got, key, state)
	}
}

func operationByKey(t *testing.T, view View, key string) Group {
	t.Helper()
	for _, operation := range view.Operations {
		if operation.Key == key {
			return operation
		}
	}
	t.Fatalf("operation %q not found in %#v", key, view.Operations)
	return Group{}
}

func assertUnscoped(t *testing.T, view View, kind, id, state string) {
	t.Helper()
	for _, record := range view.Unscoped {
		if record.Kind == kind && record.ID == id && record.State == state {
			return
		}
	}
	t.Fatalf("unscoped %s/%s state=%s not found in %#v", kind, id, state, view.Unscoped)
}
