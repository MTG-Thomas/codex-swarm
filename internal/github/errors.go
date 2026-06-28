package github

import (
	"context"
	"fmt"

	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
)

type ErrorIssueMetadataProvider struct {
	Err error
}

func (p ErrorIssueMetadataProvider) IssueMetadata(_ context.Context, issue string) (readiness.Issue, error) {
	return readiness.Issue{}, fmt.Errorf("configure GitHub issue metadata provider for %q: %w", issue, p.Err)
}
