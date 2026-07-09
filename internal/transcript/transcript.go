package transcript

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type Transcript struct {
	Worker       store.Worker             `json:"worker"`
	GeneratedAt  time.Time                `json:"generated_at"`
	Events       []store.Event            `json:"events,omitempty"`
	Reports      []string                 `json:"reports,omitempty"`
	PullRequests []store.PullRequestState `json:"pull_requests,omitempty"`
}

func Build(worker store.Worker, now time.Time) Transcript {
	events := append([]store.Event(nil), worker.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].At.Before(events[j].At)
	})
	reports := []string(nil)
	if strings.TrimSpace(worker.Report) != "" {
		reports = append(reports, worker.Report)
	}
	return Transcript{
		Worker:       worker,
		GeneratedAt:  now.UTC(),
		Events:       events,
		Reports:      reports,
		PullRequests: append([]store.PullRequestState(nil), worker.PullRequests...),
	}
}

func (t Transcript) Text() string {
	var b strings.Builder
	worker := t.Worker
	fmt.Fprintf(&b, "worker=%s status=%s role=%s\n", worker.ID, displayStatus(worker), dash(worker.Role))
	fmt.Fprintf(&b, "repo=%s worktree=%s branch=%s\n", dash(worker.ProjectRoot), dash(worker.Worktree), dash(worker.Branch))
	fmt.Fprintf(&b, "issue=%s thread=%s parent=%s\n", dash(worker.Issue), dash(worker.ThreadID), dash(worker.ParentID))
	if strings.TrimSpace(worker.Prompt) != "" {
		fmt.Fprintf(&b, "prompt=%s\n", worker.Prompt)
	}
	if strings.TrimSpace(worker.Report) != "" {
		fmt.Fprintf(&b, "report=%s\n", worker.Report)
	}
	for _, pr := range t.PullRequests {
		fmt.Fprintf(&b, "pr=%s state=%s review=%s checks=%s next=%s\n", pr.URL, dash(pr.State), dash(pr.ReviewDecision), dash(pr.CheckSummary), dash(pr.NextAction))
	}
	for _, event := range t.Events {
		fmt.Fprintf(&b, "%s\t%s\tfrom=%s\tto=%s\trequest=%s\t%s\n", event.At.Format(time.RFC3339), event.Type, dash(event.From), dash(event.To), dash(event.RequestID), event.Message)
	}
	return b.String()
}

func displayStatus(worker store.Worker) string {
	if worker.Lifecycle != nil {
		return string(worker.Lifecycle.DeriveStatus())
	}
	return string(worker.Status)
}

func dash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
