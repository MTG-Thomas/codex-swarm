package github

import (
	"context"
	"fmt"

	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
)

type ErrorIssueMetadataProvider struct {
	Err error
}

func (p ErrorIssueMetadataProvider) IssueMetadata(context.Context, string) (readiness.Issue, error) {
	return readiness.Issue{}, fmt.Errorf("configure GitHub issue metadata provider: %w", p.Err)
}
