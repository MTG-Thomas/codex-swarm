package dispatch

import (
	"testing"

	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
)

func TestPlanReadyIssue(t *testing.T) {
	plan, err := Plan(Input{
		Readiness: readyReport(),
		Prompt:    "implement issue #24",
		Gates:     []string{"test"},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.RequestID == "" {
		t.Fatal("RequestID is empty")
	}
	if plan.Issue != "MTG-Thomas/codex-swarm#24" || plan.Repo != "C:/repo" || plan.Prompt != "implement issue #24" {
		t.Fatalf("plan identity = %#v", plan)
	}
	if len(plan.Workers) != 2 {
		t.Fatalf("workers count = %d, want 2", len(plan.Workers))
	}
	if plan.Workers[0].Role != "implementer" || plan.Workers[1].Role != "validator" {
		t.Fatalf("workers = %#v", plan.Workers)
	}
	if plan.Workers[1].ValidationOfRole != "implementer" {
		t.Fatalf("validator ValidationOfRole = %q, want implementer", plan.Workers[1].ValidationOfRole)
	}
	if len(plan.Gates) != 1 || plan.Gates[0] != "test" {
		t.Fatalf("Gates = %#v, want test", plan.Gates)
	}
}

func TestPlanRejectsBlockedReadiness(t *testing.T) {
	report := readyReport()
	report.Ready = false
	report.Blockers = []string{"issue has open claim c-1"}

	_, err := Plan(Input{Readiness: report, Prompt: "implement"})
	if err == nil {
		t.Fatal("Plan() error = nil, want blocked readiness error")
	}
	if got := err.Error(); got != "issue is not ready: issue has open claim c-1" {
		t.Fatalf("Plan() error = %q", got)
	}
}

func TestPlanRejectsMissingPrompt(t *testing.T) {
	_, err := Plan(Input{Readiness: readyReport()})
	if err == nil {
		t.Fatal("Plan() error = nil, want missing prompt error")
	}
	if got := err.Error(); got != "dispatch prompt is required" {
		t.Fatalf("Plan() error = %q", got)
	}
}

func TestPlanRequestIDIsStable(t *testing.T) {
	input := Input{Readiness: readyReport(), Prompt: "implement issue #24", Gates: []string{"test", "vet"}}
	first, err := Plan(input)
	if err != nil {
		t.Fatalf("Plan(first) error = %v", err)
	}
	second, err := Plan(input)
	if err != nil {
		t.Fatalf("Plan(second) error = %v", err)
	}
	if first.RequestID != second.RequestID {
		t.Fatalf("RequestID = %q then %q, want stable", first.RequestID, second.RequestID)
	}
}

func TestPlanCanonicalizesGateIDs(t *testing.T) {
	first, err := Plan(Input{Readiness: readyReport(), Prompt: "implement issue #24", Gates: []string{"vet", "test", "test"}})
	if err != nil {
		t.Fatalf("Plan(first) error = %v", err)
	}
	second, err := Plan(Input{Readiness: readyReport(), Prompt: "implement issue #24", Gates: []string{"test,vet"}})
	if err != nil {
		t.Fatalf("Plan(second) error = %v", err)
	}
	if first.RequestID != second.RequestID {
		t.Fatalf("RequestID = %q then %q, want canonical match", first.RequestID, second.RequestID)
	}
	if len(first.Gates) != 2 || first.Gates[0] != "test" || first.Gates[1] != "vet" {
		t.Fatalf("Gates = %#v, want sorted unique test/vet", first.Gates)
	}
}

func readyReport() readiness.Report {
	return readiness.Report{
		Ready: true,
		Issue: readiness.Issue{
			Ref:   "MTG-Thomas/codex-swarm#24",
			Title: "Add explicit issue dispatch command",
			Body:  "Acceptance criteria",
		},
		Repo:  "C:/repo",
		Gates: []readiness.Gate{{ID: "test", Command: "go test ./..."}},
	}
}
