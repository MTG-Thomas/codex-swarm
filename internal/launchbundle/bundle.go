package launchbundle

import (
	"context"
	"fmt"
	"strings"

	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
	"github.com/MTG-Thomas/codex-swarm/internal/repohints"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type IssueMetadataProvider interface {
	IssueMetadata(context.Context, string) (readiness.Issue, error)
}

type ClaimStore interface {
	ListClaims() ([]store.Claim, error)
}

type Input struct {
	Worker          store.Worker
	UserPrompt      string
	Store           ClaimStore
	Provider        IssueMetadataProvider
	IncludeWorktree bool
}

type Bundle struct {
	Prompt string
	Source string
}

func BuildIssue(ctx context.Context, input Input) (Bundle, error) {
	if input.Store == nil {
		return Bundle{}, fmt.Errorf("claim store is required")
	}
	provider := input.Provider
	if provider == nil {
		var err error
		provider, err = gh.NewIssueMetadataProviderFromEnv()
		if err != nil {
			provider = gh.ErrorIssueMetadataProvider{Err: err}
		}
	}
	worker := input.Worker
	metadata, err := provider.IssueMetadata(ctx, worker.Issue)
	if err != nil {
		return Bundle{}, err
	}
	ref, err := gh.ParseIssueRef(worker.Issue)
	if err != nil {
		return Bundle{}, err
	}
	issueURL := fmt.Sprintf("https://github.com/%s/%s/issues/%d", ref.Owner, ref.Repo, ref.Number)
	claims, err := input.Store.ListClaims()
	if err != nil {
		return Bundle{}, err
	}
	hints, _, hintsOK, err := repohints.Load(worker.ProjectRoot)
	if err != nil {
		return Bundle{}, err
	}
	var b strings.Builder
	fmt.Fprintln(&b, "ISSUE_LAUNCH_BUNDLE")
	fmt.Fprintf(&b, "issue=%s\n", worker.Issue)
	fmt.Fprintf(&b, "url=%s\n", issueURL)
	if title := strings.TrimSpace(metadata.Title); title != "" {
		fmt.Fprintf(&b, "title=%s\n", title)
	}
	if body := strings.TrimSpace(metadata.Body); body != "" {
		fmt.Fprintf(&b, "body=%s\n", compactMultiline(body, 1200))
	}
	fmt.Fprintf(&b, "repo=%s\n", worker.ProjectRoot)
	if input.IncludeWorktree {
		if worker.Worktree != "" {
			fmt.Fprintf(&b, "worktree=%s\n", worker.Worktree)
		}
		if worker.Branch != "" {
			fmt.Fprintf(&b, "branch=%s\n", worker.Branch)
		}
	}
	for _, claim := range claims {
		if claim.Issue != worker.Issue || claim.Status != store.ClaimActive {
			continue
		}
		fmt.Fprintf(&b, "claim=%s status=%s scope=%s\n", claim.ID, claim.Status, emptyDash(claim.Scope))
	}
	if hintsOK {
		for _, line := range hints.Lines() {
			fmt.Fprintln(&b, line)
		}
		for _, gate := range hints.QualityGates {
			if command := strings.TrimSpace(gate.Command); command != "" {
				fmt.Fprintf(&b, "required_verification=%s\n", command)
			}
		}
	}
	fmt.Fprintln(&b, "forbidden=no merge, deploy, close, or destructive cleanup unless explicitly requested")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "USER_TASK")
	fmt.Fprintln(&b, strings.TrimSpace(input.UserPrompt))
	return Bundle{
		Prompt: strings.TrimRight(b.String(), "\n"),
		Source: fmt.Sprintf("issue=%s url=%s title=%s", worker.Issue, issueURL, strings.TrimSpace(metadata.Title)),
	}, nil
}

func compactMultiline(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		value = value[:limit] + "..."
	}
	lines := strings.Split(value, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}
