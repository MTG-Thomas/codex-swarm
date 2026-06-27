package readiness

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/repohints"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type IssueMetadataProvider interface {
	IssueMetadata(context.Context, string) (Issue, error)
}

type ClaimStore interface {
	ListClaims() ([]store.Claim, error)
}

type BuildInput struct {
	Issue    string
	Repo     string
	Store    ClaimStore
	Provider IssueMetadataProvider
}

func Build(ctx context.Context, input BuildInput) (Report, error) {
	issue := strings.TrimSpace(input.Issue)
	if issue == "" {
		return Report{}, fmt.Errorf("issue reference is required")
	}
	repoRoot, err := filepath.Abs(input.Repo)
	if err != nil {
		return Report{}, fmt.Errorf("resolve repo: %w", err)
	}
	if input.Store == nil {
		return Report{}, fmt.Errorf("claim store is required")
	}
	if input.Provider == nil {
		return Report{}, fmt.Errorf("issue metadata provider is required")
	}
	metadata, err := input.Provider.IssueMetadata(ctx, issue)
	if err != nil {
		return Report{}, err
	}
	metadata.Ref = issue
	hints, _, ok, err := repohints.Load(repoRoot)
	if err != nil {
		return Report{}, err
	}
	gates := []Gate(nil)
	if ok {
		for _, gate := range hints.QualityGates {
			gates = append(gates, Gate{
				ID:      strings.TrimSpace(gate.ID),
				Command: strings.TrimSpace(gate.Command),
				Scope:   strings.TrimSpace(gate.Scope),
			})
		}
	}
	claims, err := input.Store.ListClaims()
	if err != nil {
		return Report{}, err
	}
	readinessClaims := []Claim(nil)
	for _, claim := range claims {
		if claim.Issue != issue {
			continue
		}
		readinessClaims = append(readinessClaims, Claim{
			ID:       claim.ID,
			WorkerID: claim.WorkerID,
			Scope:    claim.Scope,
			Status:   string(claim.Status),
		})
	}
	return Evaluate(Input{
		Issue:  metadata,
		Repo:   repoRoot,
		Gates:  gates,
		Claims: readinessClaims,
	}), nil
}
