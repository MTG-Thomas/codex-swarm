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
		return errors.New("issue requires <export|sync|pull|report|claim>")
	}
	switch args[0] {
	case "export":
		return c.issueExport(args[1:])
	case "sync":
		return c.issueSync(args[1:])
	case "pull":
		return c.issuePull(args[1:])
	case "report":
		return c.issueReport(args[1:])
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
	updated, err := upsertIssueMarkerComment(context.Background(), issue, body)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "synced issue=%s claims=%d mode=%s\n", issue, len(claimsForIssue), updated)
	return nil
}

func (c cli) issuePull(args []string) error {
	fs := c.flagSet("issue pull")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	force := fs.Bool("force", false, "overwrite newer local claims with issue marker claims")
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
	imported, skippedNewerLocal, err := importClaimSnapshot(store.NewJSONStore(*statePath), issue, snapshot, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "pulled issue=%s imported=%d skipped_newer_local=%d state=%s\n", issue, imported, skippedNewerLocal, *statePath)
	return nil
}

func (c cli) issueReport(args []string) error {
	fs := c.flagSet("issue report")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	workerID := fs.String("worker", "", "worker id")
	note := fs.String("note", "", "optional report note override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	issue, err := normalizeRequiredIssue(*issueValue)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*workerID) == "" {
		return errors.New("issue report requires --worker")
	}
	worker, err := store.NewJSONStore(*statePath).GetWorker(*workerID)
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", *workerID)
		}
		return err
	}
	if worker.Issue != "" && worker.Issue != issue {
		return fmt.Errorf("worker %s is linked to %s, not %s", worker.ID, worker.Issue, issue)
	}
	body := workerIssueReportMarkdown(issue, worker, *note, c.now().UTC())
	if err := postIssueComment(context.Background(), issue, body); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "reported issue=%s worker=%s status=%s\n", issue, worker.ID, worker.Status)
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

func importClaimSnapshot(st *store.JSONStore, issue string, snapshot issueClaimSnapshot, force bool) (int, int, error) {
	imported := 0
	skippedNewerLocal := 0
	for _, claim := range snapshot.Claims {
		if claim.Issue == "" {
			claim.Issue = issue
		}
		local, err := st.GetClaim(claim.ID)
		if err != nil && !errors.Is(err, store.ErrClaimNotFound) {
			return imported, skippedNewerLocal, err
		}
		if err == nil && !force && local.UpdatedAt.After(claim.UpdatedAt) {
			skippedNewerLocal++
			continue
		}
		if err := st.SaveClaim(claim); err != nil {
			return imported, skippedNewerLocal, err
		}
		imported++
	}
	return imported, skippedNewerLocal, nil
}

func workerIssueReportMarkdown(issue string, worker store.Worker, note string, now time.Time) string {
	report := strings.TrimSpace(note)
	if report == "" {
		report = strings.TrimSpace(worker.Report)
	}
	if report == "" {
		report = strings.TrimSpace(worker.LastMessage)
	}
	if report == "" {
		report = "No report text recorded."
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "## codex-swarm worker report for `%s`\n\n", issue)
	fmt.Fprintf(&buf, "_Generated: %s_\n\n", now.Format(time.RFC3339))
	fmt.Fprintf(&buf, "- Worker: `%s`\n", worker.ID)
	fmt.Fprintf(&buf, "- Status: `%s`\n", worker.Status)
	fmt.Fprintf(&buf, "- Engine: `%s`\n", worker.Engine)
	if worker.ThreadID != "" {
		fmt.Fprintf(&buf, "- Thread: `%s`\n", worker.ThreadID)
	}
	if worker.ProjectRoot != "" {
		fmt.Fprintf(&buf, "- Repo: `%s`\n", worker.ProjectRoot)
	}
	fmt.Fprintf(&buf, "\n%s\n", report)
	return buf.String()
}

type ghIssueView struct {
	Body     string `json:"body"`
	Comments []struct {
		ID        string    `json:"id"`
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

func upsertIssueMarkerComment(ctx context.Context, issue, body string) (string, error) {
	raw, err := fetchIssueJSON(ctx, issue)
	if err != nil {
		return "", err
	}
	commentID, err := latestMarkerCommentID(raw)
	if err != nil {
		return "", err
	}
	if commentID == "" {
		if err := postIssueComment(ctx, issue, body); err != nil {
			return "", err
		}
		return "created", nil
	}
	if err := updateIssueComment(ctx, commentID, body); err != nil {
		return "", err
	}
	return "updated", nil
}

func latestMarkerCommentID(raw []byte) (string, error) {
	var view ghIssueView
	if err := json.Unmarshal(raw, &view); err != nil {
		return "", fmt.Errorf("parse GitHub issue JSON: %w", err)
	}
	var latestID string
	var latestAt time.Time
	for _, comment := range view.Comments {
		if !strings.Contains(comment.Body, claimMarkerStart) {
			continue
		}
		if latestID == "" || comment.CreatedAt.After(latestAt) {
			latestID = comment.ID
			latestAt = comment.CreatedAt
		}
	}
	return latestID, nil
}

func updateIssueComment(ctx context.Context, commentID, body string) error {
	cmd := exec.CommandContext(
		ctx,
		"gh",
		"api",
		"graphql",
		"-f",
		"id="+commentID,
		"-f",
		"body="+body,
		"-f",
		"query=mutation($id:ID!,$body:String!){updateIssueComment(input:{id:$id,body:$body}){issueComment{id}}}",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("update GitHub issue comment: %s", message)
	}
	return nil
}
