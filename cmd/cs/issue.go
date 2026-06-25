package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const (
	claimMarkerStart = "<!-- codex-swarm:claims:v1"
	claimMarkerEnd   = "-->"
)

type issueClaimSnapshot struct {
	Issue       string        `json:"issue"`
	GeneratedAt time.Time     `json:"generated_at"`
	Claims      []store.Claim `json:"claims"`
}

func (c cli) issue(args []string) error {
	if len(args) == 0 {
		return errors.New("issue requires <export|sync|pull|claim>")
	}
	switch args[0] {
	case "export":
		return c.issueExport(args[1:])
	case "sync":
		return c.issueSync(args[1:])
	case "pull":
		return c.issuePull(args[1:])
	case "claim":
		return c.issueClaim(args[1:])
	default:
		return fmt.Errorf("unknown issue command %q", args[0])
	}
}

func (c cli) issueExport(args []string) error {
	fs := c.flagSet("issue export")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	if err := fs.Parse(args); err != nil {
		return err
	}
	issue, err := normalizeRequiredIssue(*issueValue)
	if err != nil {
		return err
	}
	claimsForIssue, err := c.claimsForIssue(*statePath, issue)
	if err != nil {
		return err
	}
	body, err := claimIssueMarkerMarkdown(issue, claimsForIssue, c.now().UTC())
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, body)
	return nil
}

func (c cli) issueSync(args []string) error {
	fs := c.flagSet("issue sync")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	if err := fs.Parse(args); err != nil {
		return err
	}
	issue, err := normalizeRequiredIssue(*issueValue)
	if err != nil {
		return err
	}
	claimsForIssue, err := c.claimsForIssue(*statePath, issue)
	if err != nil {
		return err
	}
	body, err := claimIssueMarkerMarkdown(issue, claimsForIssue, c.now().UTC())
	if err != nil {
		return err
	}
	if err := postIssueComment(context.Background(), issue, body); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "synced issue=%s claims=%d\n", issue, len(claimsForIssue))
	return nil
}

func (c cli) issuePull(args []string) error {
	fs := c.flagSet("issue pull")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	if err := fs.Parse(args); err != nil {
		return err
	}
	issue, err := normalizeRequiredIssue(*issueValue)
	if err != nil {
		return err
	}
	raw, err := fetchIssueJSON(context.Background(), issue)
	if err != nil {
		return err
	}
	snapshot, err := latestClaimSnapshot(raw)
	if err != nil {
		return err
	}
	if snapshot.Issue != issue {
		return fmt.Errorf("latest claim marker is for %s, expected %s", snapshot.Issue, issue)
	}
	st := store.NewJSONStore(*statePath)
	imported := 0
	for _, claim := range snapshot.Claims {
		if claim.Issue == "" {
			claim.Issue = issue
		}
		if err := st.SaveClaim(claim); err != nil {
			return err
		}
		imported++
	}
	fmt.Fprintf(c.out, "pulled issue=%s claims=%d state=%s\n", issue, imported, *statePath)
	return nil
}

func (c cli) issueClaim(args []string) error {
	if len(args) == 0 {
		return errors.New("issue claim requires <create>")
	}
	if args[0] != "create" {
		return fmt.Errorf("unknown issue claim command %q", args[0])
	}
	return c.claimCreate(args[1:])
}

func claimIssueMarkerMarkdown(issue string, all []store.Claim, now time.Time) (string, error) {
	payload, err := json.MarshalIndent(issueClaimSnapshot{
		Issue:       issue,
		GeneratedAt: now,
		Claims:      all,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode claim snapshot: %w", err)
	}
	return claimMarkerStart + "\n" + string(payload) + "\n" + claimMarkerEnd + "\n\n" + claimIssueMarkdown(issue, all, now), nil
}

type ghIssueView struct {
	Body     string `json:"body"`
	Comments []struct {
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"comments"`
}

func latestClaimSnapshot(raw []byte) (issueClaimSnapshot, error) {
	var view ghIssueView
	if err := json.Unmarshal(raw, &view); err != nil {
		return issueClaimSnapshot{}, fmt.Errorf("parse GitHub issue JSON: %w", err)
	}
	type candidate struct {
		body string
		at   time.Time
	}
	candidates := []candidate{{body: view.Body}}
	for _, comment := range view.Comments {
		candidates = append(candidates, candidate{body: comment.Body, at: comment.CreatedAt})
	}
	var latest issueClaimSnapshot
	var latestAt time.Time
	found := false
	for _, item := range candidates {
		snapshot, ok, err := extractClaimSnapshot(item.body)
		if err != nil {
			return issueClaimSnapshot{}, err
		}
		if !ok {
			continue
		}
		if !found || item.at.After(latestAt) || snapshot.GeneratedAt.After(latest.GeneratedAt) {
			latest = snapshot
			latestAt = item.at
			found = true
		}
	}
	if !found {
		return issueClaimSnapshot{}, errors.New("no codex-swarm claim marker found on issue")
	}
	return latest, nil
}

func extractClaimSnapshot(body string) (issueClaimSnapshot, bool, error) {
	start := strings.LastIndex(body, claimMarkerStart)
	if start < 0 {
		return issueClaimSnapshot{}, false, nil
	}
	contentStart := start + len(claimMarkerStart)
	end := strings.Index(body[contentStart:], claimMarkerEnd)
	if end < 0 {
		return issueClaimSnapshot{}, false, errors.New("unterminated codex-swarm claim marker")
	}
	payload := strings.TrimSpace(body[contentStart : contentStart+end])
	var snapshot issueClaimSnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return issueClaimSnapshot{}, false, fmt.Errorf("parse codex-swarm claim marker: %w", err)
	}
	return snapshot, true, nil
}

func fetchIssueJSON(ctx context.Context, issue string) ([]byte, error) {
	ref, err := gh.ParseIssueRef(issue)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "gh", "issue", "view", fmt.Sprintf("%d", ref.Number), "--repo", ref.Owner+"/"+ref.Repo, "--json", "body,comments")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("read GitHub issue: %s", message)
	}
	return stdout.Bytes(), nil
}
