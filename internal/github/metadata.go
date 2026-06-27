package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
)

type issueMetadata struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type CLIssueMetadataProvider struct{}

func (CLIssueMetadataProvider) IssueMetadata(ctx context.Context, issue string) (readiness.Issue, error) {
	ref, err := ParseIssueRef(issue)
	if err != nil {
		return readiness.Issue{}, err
	}
	cmd := exec.CommandContext(ctx, "gh", "issue", "view", fmt.Sprintf("%d", ref.Number), "--repo", ref.Owner+"/"+ref.Repo, "--json", "title,body")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return readiness.Issue{}, fmt.Errorf("read GitHub issue metadata: %s", message)
	}
	var metadata issueMetadata
	if err := json.Unmarshal(stdout.Bytes(), &metadata); err != nil {
		return readiness.Issue{}, fmt.Errorf("parse GitHub issue metadata: %w", err)
	}
	return readiness.Issue{
		Ref:   ref.String(),
		Title: metadata.Title,
		Body:  metadata.Body,
	}, nil
}
