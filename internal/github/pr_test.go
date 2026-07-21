package github

import "testing"

func TestPullRequestStatusNextActionTerminalStates(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  string
	}{
		{name: "merged", state: "MERGED", want: "complete"},
		{name: "closed", state: "CLOSED", want: "closed"},
		{name: "unknown", state: "", want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (PullRequestStatus{State: tt.state}).NextAction(); got != tt.want {
				t.Fatalf("NextAction() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPullRequestStatusNextActionIgnoresAdvisoryCodeRabbitFailure(t *testing.T) {
	status := PullRequestStatus{State: "OPEN", ChecksPassed: 3, CodeRabbitStatus: "FAILURE"}
	if got := status.NextAction(); got != "merge-ready" {
		t.Fatalf("NextAction() = %q, want merge-ready", got)
	}
}
