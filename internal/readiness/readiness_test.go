package readiness

import (
	"reflect"
	"testing"
)

func TestEvaluateReadyIssue(t *testing.T) {
	report := Evaluate(Input{
		Issue: Issue{
			Ref:   "MTG-Thomas/codex-swarm#18",
			Title: "Add issue dispatch readiness",
			Body:  "Acceptance criteria",
		},
		Repo:  "C:/repo",
		Gates: []Gate{{ID: "test", Command: "go test ./..."}},
	})
	if !report.Ready {
		t.Fatalf("Ready = false, blockers = %#v", report.Blockers)
	}
	if report.Issue.Ref != "MTG-Thomas/codex-swarm#18" || report.Repo != "C:/repo" || len(report.Gates) != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestEvaluateReportsDeterministicBlockers(t *testing.T) {
	report := Evaluate(Input{
		Issue: Issue{Ref: "not-an-issue"},
		Claims: []Claim{{
			ID:     "c-1",
			Status: "active",
			Scope:  "repo",
		}},
	})
	want := []string{
		"issue reference must look like owner/repo#123",
		"issue title is missing",
		"issue body is missing",
		"repo path is missing",
		"no quality gates configured",
		"issue has open claim c-1",
	}
	if report.Ready {
		t.Fatalf("Ready = true, want false")
	}
	if !reflect.DeepEqual(report.Blockers, want) {
		t.Fatalf("Blockers = %#v, want %#v", report.Blockers, want)
	}
}

func TestEvaluateIgnoresReleasedClaims(t *testing.T) {
	report := Evaluate(Input{
		Issue: Issue{
			Ref:   "MTG-Thomas/codex-swarm#18",
			Title: "Ready",
			Body:  "Body",
		},
		Repo:  "C:/repo",
		Gates: []Gate{{ID: "test", Command: "go test ./..."}},
		Claims: []Claim{{
			ID:     "c-1",
			Status: "released",
		}},
	})
	if !report.Ready {
		t.Fatalf("Ready = false, blockers = %#v", report.Blockers)
	}
	if len(report.Claims) != 1 {
		t.Fatalf("Claims = %#v, want released claim preserved for context", report.Claims)
	}
}
