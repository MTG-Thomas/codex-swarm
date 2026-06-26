package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) claim(args []string) error {
	if len(args) == 0 {
		return errors.New("claim requires <create|list|conflicts|show|release|block|export|push>")
	}
	switch args[0] {
	case "create":
		return c.claimCreate(args[1:])
	case "list":
		return c.claimList(args[1:])
	case "conflicts":
		return c.claimConflicts(args[1:])
	case "show":
		return c.claimShow(args[1:])
	case "release":
		return c.claimRelease(args[1:])
	case "block":
		return c.claimBlock(args[1:])
	case "export":
		return c.claimExport(args[1:])
	case "push":
		return c.claimPush(args[1:])
	default:
		return fmt.Errorf("unknown claim command %q", args[0])
	}
}

func (c cli) claimCreate(args []string) error {
	fs := c.flagSet("claim create")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	scope := fs.String("scope", "", "claimed path or task scope")
	workerID := fs.String("worker", "", "worker id")
	issueValue := fs.String("issue", "", "GitHub issue reference, for example owner/repo#123")
	note := fs.String("note", "", "claim note")
	ttl := fs.Duration("ttl", 24*time.Hour, "claim time to live")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*scope) == "" {
		return errors.New("claim create requires --scope")
	}
	workerIDValue := strings.TrimSpace(*workerID)
	st := store.NewJSONStore(*statePath)
	workers, err := st.ListWorkers()
	if err != nil {
		return err
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	if err := claims.ValidateWorkerForRepo(workerIDValue, repoRoot, workers); err != nil {
		return err
	}
	issue, err := normalizeIssue(*issueValue)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	claim := store.Claim{
		ID:        fmt.Sprintf("c-%s", now.Format("20060102-150405")),
		WorkerID:  workerIDValue,
		Repo:      repoRoot,
		Scope:     *scope,
		Issue:     issue,
		Status:    store.ClaimActive,
		Note:      *note,
		ExpiresAt: now.Add(*ttl),
		CreatedAt: now,
		UpdatedAt: now,
	}
	all, err := st.ListClaims()
	if err != nil {
		return err
	}
	conflicts := claims.FindConflicts(all, claim, now)
	if err := st.SaveClaim(claim); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "claim %s status=%s scope=%s repo=%s\n", claim.ID, claim.Status, claim.Scope, claim.Repo)
	if claim.WorkerID != "" {
		fmt.Fprintf(c.out, "worker=%s\n", claim.WorkerID)
	}
	if claim.Issue != "" {
		fmt.Fprintf(c.out, "issue=%s\n", claim.Issue)
	}
	if len(conflicts) > 0 {
		fmt.Fprintf(c.out, "conflicts=%d\n", len(conflicts))
		for _, conflict := range conflicts {
			printClaimLine(c.out, conflict, now)
		}
	} else {
		fmt.Fprintln(c.out, "conflicts=0")
	}
	return nil
}

func (c cli) claimList(args []string) error {
	fs := c.flagSet("claim list")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "filter by GitHub issue")
	openOnly := fs.Bool("open", false, "show only open claims")
	if err := fs.Parse(args); err != nil {
		return err
	}
	issue, err := normalizeIssue(*issueValue)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	all, err := store.NewJSONStore(*statePath).ListClaims()
	if err != nil {
		return err
	}
	var filtered []store.Claim
	for _, claim := range all {
		if issue != "" && claim.Issue != issue {
			continue
		}
		if *openOnly && !claims.IsOpen(claim, now) {
			continue
		}
		filtered = append(filtered, claim)
	}
	fmt.Fprintf(c.out, "claims=%d state=%s\n", len(filtered), *statePath)
	for _, claim := range filtered {
		printClaimLine(c.out, claim, now)
	}
	return nil
}

func (c cli) claimConflicts(args []string) error {
	fs := c.flagSet("claim conflicts")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	scope := fs.String("scope", "", "candidate path or task scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*scope) == "" {
		return errors.New("claim conflicts requires --scope")
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	now := c.now().UTC()
	candidate := store.Claim{Repo: repoRoot, Scope: *scope, Status: store.ClaimActive, ExpiresAt: now.Add(time.Hour)}
	all, err := store.NewJSONStore(*statePath).ListClaims()
	if err != nil {
		return err
	}
	conflicts := claims.FindConflicts(all, candidate, now)
	fmt.Fprintf(c.out, "conflicts=%d repo=%s scope=%s\n", len(conflicts), repoRoot, *scope)
	for _, conflict := range conflicts {
		printClaimLine(c.out, conflict, now)
	}
	return nil
}

func (c cli) claimShow(args []string) error {
	fs := c.flagSet("claim show")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("claim show requires <claim-id>")
	}
	claim, err := store.NewJSONStore(*statePath).GetClaim(rest[0])
	if err != nil {
		if errors.Is(err, store.ErrClaimNotFound) {
			return fmt.Errorf("claim %q not found", rest[0])
		}
		return err
	}
	printClaimDetail(c.out, claim, c.now().UTC())
	return nil
}

func (c cli) claimRelease(args []string) error {
	fs := c.flagSet("claim release")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	note := fs.String("note", "", "release note")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("claim release requires <claim-id>")
	}
	return c.updateClaim(*statePath, rest[0], func(claim *store.Claim, now time.Time) {
		claim.Status = store.ClaimReleased
		claim.Note = *note
		claim.Blocker = ""
		claim.Next = ""
	}, func(claim store.Claim) {
		fmt.Fprintf(c.out, "released %s status=%s\n", claim.ID, claim.Status)
	})
}

func (c cli) claimBlock(args []string) error {
	fs := c.flagSet("claim block")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	reason := fs.String("reason", "", "blocker reason")
	next := fs.String("next", "", "smallest next action")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*reason) == "" {
		return errors.New("claim block requires --reason")
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("claim block requires <claim-id>")
	}
	return c.updateClaim(*statePath, rest[0], func(claim *store.Claim, now time.Time) {
		claim.Status = store.ClaimBlocked
		claim.Blocker = *reason
		claim.Next = *next
	}, func(claim store.Claim) {
		fmt.Fprintf(c.out, "blocked %s status=%s\n", claim.ID, claim.Status)
	})
}

func (c cli) claimExport(args []string) error {
	fs := c.flagSet("claim export")
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
	fmt.Fprint(c.out, claimIssueMarkdown(issue, claimsForIssue, c.now().UTC()))
	return nil
}

func (c cli) claimPush(args []string) error {
	fs := c.flagSet("claim push")
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
	body := claimIssueMarkdown(issue, claimsForIssue, c.now().UTC())
	if err := postIssueComment(context.Background(), issue, body); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "pushed claims issue=%s claims=%d\n", issue, len(claimsForIssue))
	return nil
}

func (c cli) claimsForIssue(statePath, issue string) ([]store.Claim, error) {
	all, err := store.NewJSONStore(statePath).ListClaims()
	if err != nil {
		return nil, err
	}
	var filtered []store.Claim
	for _, claim := range all {
		if claim.Issue == issue {
			filtered = append(filtered, claim)
		}
	}
	return filtered, nil
}

func (c cli) updateClaim(statePath, id string, mutate func(*store.Claim, time.Time), print func(store.Claim)) error {
	st := store.NewJSONStore(statePath)
	claim, err := st.GetClaim(id)
	if err != nil {
		if errors.Is(err, store.ErrClaimNotFound) {
			return fmt.Errorf("claim %q not found", id)
		}
		return err
	}
	now := c.now().UTC()
	mutate(&claim, now)
	claim.UpdatedAt = now
	if err := st.SaveClaim(claim); err != nil {
		return err
	}
	print(claim)
	return nil
}

func normalizeIssue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	ref, err := gh.ParseIssueRef(value)
	if err != nil {
		return "", err
	}
	return ref.String(), nil
}

func normalizeRequiredIssue(value string) (string, error) {
	issue, err := normalizeIssue(value)
	if err != nil {
		return "", err
	}
	if issue == "" {
		return "", errors.New("--issue is required")
	}
	return issue, nil
}

func claimIssueMarkdown(issue string, all []store.Claim, now time.Time) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "## codex-swarm claims for `%s`\n\n", issue)
	fmt.Fprintf(&buf, "_Generated: %s_\n\n", now.Format(time.RFC3339))
	if len(all) == 0 {
		buf.WriteString("No local claims are linked to this issue.\n")
		return buf.String()
	}
	buf.WriteString("| Claim | Status | Worker | Scope | Expires | Note |\n")
	buf.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, claim := range all {
		status := string(claim.Status)
		if claim.Status == store.ClaimActive && !claims.IsOpen(claim, now) {
			status = "expired"
		}
		fmt.Fprintf(
			&buf,
			"| `%s` | %s | `%s` | `%s` | %s | %s |\n",
			claim.ID,
			status,
			emptyDash(claim.WorkerID),
			claim.Scope,
			claim.ExpiresAt.Format(time.RFC3339),
			markdownCell(claim.Note),
		)
	}
	return buf.String()
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func postIssueComment(ctx context.Context, issue, body string) error {
	ref, err := gh.ParseIssueRef(issue)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "gh", "issue", "comment", fmt.Sprintf("%d", ref.Number), "--repo", ref.Owner+"/"+ref.Repo, "--body", body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("post GitHub issue comment: %s", message)
	}
	return nil
}

func printClaimLine(out interface{ Write([]byte) (int, error) }, claim store.Claim, now time.Time) {
	status := string(claim.Status)
	if claim.Status == store.ClaimActive && !claims.IsOpen(claim, now) {
		status = "expired"
	}
	fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n", claim.ID, status, emptyDash(claim.WorkerID), claim.Scope, emptyDash(claim.Issue), claim.Note)
}

func printClaimDetail(out interface{ Write([]byte) (int, error) }, claim store.Claim, now time.Time) {
	status := string(claim.Status)
	if claim.Status == store.ClaimActive && !claims.IsOpen(claim, now) {
		status = "expired"
	}
	fmt.Fprintf(out, "id=%s\nstatus=%s\nworker=%s\nrepo=%s\nscope=%s\n", claim.ID, status, emptyDash(claim.WorkerID), claim.Repo, claim.Scope)
	if claim.Issue != "" {
		fmt.Fprintf(out, "issue=%s\n", claim.Issue)
	}
	if claim.Note != "" {
		fmt.Fprintf(out, "note=%s\n", claim.Note)
	}
	if claim.Blocker != "" {
		fmt.Fprintf(out, "blocker=%s\n", claim.Blocker)
	}
	if claim.Next != "" {
		fmt.Fprintf(out, "next=%s\n", claim.Next)
	}
	fmt.Fprintf(out, "expires=%s\ncreated=%s\nupdated=%s\n", claim.ExpiresAt.Format(time.RFC3339), claim.CreatedAt.Format(time.RFC3339), claim.UpdatedAt.Format(time.RFC3339))
}
