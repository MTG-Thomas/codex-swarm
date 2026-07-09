package workpacket

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const Schema = "codex-swarm:workpacket:v1"

type Packet struct {
	Schema           string                   `json:"schema"`
	GeneratedAt      time.Time                `json:"generated_at"`
	WorkerID         string                   `json:"worker_id"`
	ParentID         string                   `json:"parent_id,omitempty"`
	Role             string                   `json:"role,omitempty"`
	Issue            string                   `json:"issue,omitempty"`
	Repo             string                   `json:"repo,omitempty"`
	Worktree         string                   `json:"worktree,omitempty"`
	Branch           string                   `json:"branch,omitempty"`
	ThreadID         string                   `json:"thread_id,omitempty"`
	Status           string                   `json:"status,omitempty"`
	Prompt           string                   `json:"prompt,omitempty"`
	Report           string                   `json:"report,omitempty"`
	Claims           []store.Claim            `json:"claims,omitempty"`
	RecentEvents     []store.Event            `json:"recent_events,omitempty"`
	PullRequests     []store.PullRequestState `json:"pull_requests,omitempty"`
	ForbiddenActions []string                 `json:"forbidden_actions,omitempty"`
	NextAction       string                   `json:"next_action,omitempty"`
}

type Input struct {
	Worker store.Worker
	Claims []store.Claim
	Now    time.Time
	Limit  int
}

func Build(input Input) Packet {
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	worker := input.Worker
	events := append([]store.Event(nil), worker.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].At.Before(events[j].At)
	})
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	relevantClaims := []store.Claim(nil)
	now := input.Now.UTC()
	for _, claim := range input.Claims {
		if claim.Repo == worker.ProjectRoot || claim.Issue == worker.Issue || claim.WorkerID == worker.ID {
			if claims.IsOpen(claim, now) {
				relevantClaims = append(relevantClaims, claim)
			}
		}
	}
	return Packet{
		Schema:           Schema,
		GeneratedAt:      now,
		WorkerID:         worker.ID,
		ParentID:         worker.ParentID,
		Role:             worker.Role,
		Issue:            worker.Issue,
		Repo:             worker.ProjectRoot,
		Worktree:         worker.Worktree,
		Branch:           worker.Branch,
		ThreadID:         worker.ThreadID,
		Status:           displayStatus(worker),
		Prompt:           worker.Prompt,
		Report:           worker.Report,
		Claims:           relevantClaims,
		RecentEvents:     events,
		PullRequests:     append([]store.PullRequestState(nil), worker.PullRequests...),
		ForbiddenActions: []string{"Do not mutate unrelated repo files or user changes.", "Do not treat warning-only claims as hard locks.", "Do not post to GitHub unless explicitly requested."},
		NextAction:       nextAction(worker),
	}
}

func (p Packet) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema=%s worker=%s status=%s role=%s\n", p.Schema, p.WorkerID, dash(p.Status), dash(p.Role))
	fmt.Fprintf(&b, "repo=%s worktree=%s branch=%s\n", dash(p.Repo), dash(p.Worktree), dash(p.Branch))
	fmt.Fprintf(&b, "issue=%s thread=%s parent=%s\n", dash(p.Issue), dash(p.ThreadID), dash(p.ParentID))
	if p.Prompt != "" {
		fmt.Fprintf(&b, "prompt=%s\n", p.Prompt)
	}
	if p.Report != "" {
		fmt.Fprintf(&b, "report=%s\n", p.Report)
	}
	fmt.Fprintf(&b, "next=%s\n", dash(p.NextAction))
	for _, claim := range p.Claims {
		fmt.Fprintf(&b, "claim=%s status=%s worker=%s scope=%s\n", claim.ID, claim.Status, dash(claim.WorkerID), dash(claim.Scope))
	}
	for _, event := range p.RecentEvents {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", event.At.Format(time.RFC3339), event.Type, event.Message)
	}
	return b.String()
}

func displayStatus(worker store.Worker) string {
	if worker.Lifecycle != nil {
		return string(worker.Lifecycle.DeriveStatus())
	}
	return string(worker.Status)
}

func nextAction(worker store.Worker) string {
	if strings.TrimSpace(worker.Report) != "" {
		return "inspect report and verify remaining gates"
	}
	if strings.TrimSpace(worker.Worktree) == "" {
		return "assign or verify filesystem isolation before mutation"
	}
	return "continue from latest event"
}

func dash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
