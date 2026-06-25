package github

import "testing"

func TestParseIssueRef(t *testing.T) {
	ref, err := ParseIssueRef("MTG-Thomas/codex-swarm#42")
	if err != nil {
		t.Fatalf("ParseIssueRef() error = %v", err)
	}
	if ref.Owner != "MTG-Thomas" || ref.Repo != "codex-swarm" || ref.Number != 42 {
		t.Fatalf("ref = %#v", ref)
	}
	if ref.String() != "MTG-Thomas/codex-swarm#42" {
		t.Fatalf("String() = %q", ref.String())
	}
}

func TestParseIssueRefRejectsInvalid(t *testing.T) {
	for _, value := range []string{"", "repo#1", "owner/repo", "owner/repo#nope", "owner/repo#0"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseIssueRef(value); err == nil {
				t.Fatal("ParseIssueRef() error = nil, want error")
			}
		})
	}
}
