package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/dispatch"
	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
	"github.com/MTG-Thomas/codex-swarm/internal/repohints"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const (
	claimMarkerStart  = "<!-- codex-swarm:claims:v1"
	claimMarkerEnd    = "-->"
	claimMarkerSchema = "codex-swarm.claims.v1"
)

type issueClaimSnapshot struct {
	Schema      string        `json:"schema,omitempty"`
	SnapshotID  string        `json:"snapshot_id,omitempty"`
	Issue       string        `json:"issue"`
	GeneratedAt time.Time     `json:"generated_at"`
	MachineID   string        `json:"machine_id,omitempty"`
	Claims      []store.Claim `json:"claims"`
}

type claimImportPlan struct {
	Imported   int
	Skipped    int
	Conflicted int
	Entries    []claimImportPlanEntry
}

type claimImportPlanEntry struct {
	Claim  store.Claim
	Action string
	Reason string
}

func (c cli) issue(args []string) error {
	if len(args) == 0 {
		return errors.New("issue requires <export|sync|pull|report|claim|ready|dispatch>")
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
	case "ready":
		return c.issueReady(args[1:])
	case "dispatch":
		return c.issueDispatch(args[1:])
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
	dryRun := fs.Bool("dry-run", false, "print the pull plan without writing local state")
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
	plan, err := planClaimSnapshotImport(st, issue, snapshot, *force)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Fprintf(c.out, "pull dry-run issue=%s imported=%d skipped=%d conflicted=%d state=%s\n", issue, plan.Imported, plan.Skipped, plan.Conflicted, *statePath)
		return nil
	}
	if err := applyClaimImportPlan(st, &plan, *force); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "pulled issue=%s imported=%d skipped=%d conflicted=%d state=%s\n", issue, plan.Imported, plan.Skipped, plan.Conflicted, *statePath)
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
	fmt.Fprintf(c.out, "reported issue=%s worker=%s status=%s\n", issue, worker.ID, displayWorkerStatus(worker))
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

func (c cli) issueReady(args []string) error {
	fs := c.flagSet("issue ready")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	repo := fs.String("repo", ".", "repository root")
	jsonOutput := fs.Bool("json", false, "print readiness as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := c.issueReadinessReport(*statePath, *issueValue, *repo)
	if err != nil {
		return err
	}
	if *jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("encode readiness report: %w", err)
		}
		if _, err := fmt.Fprintln(c.out, string(data)); err != nil {
			return fmt.Errorf("write readiness report: %w", err)
		}
		return nil
	}
	printReadinessReport(c.out, report)
	return nil
}

func (c cli) issueDispatch(args []string) error {
	fs := c.flagSet("issue dispatch")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	issueValue := fs.String("issue", "", "GitHub issue reference")
	repo := fs.String("repo", ".", "repository root")
	prompt := fs.String("prompt", "", "implementer prompt")
	engine := fs.String("engine", "mock", "worker engine: mock")
	gates := fs.String("gate", "", "comma-separated quality gate ids")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *engine != "mock" {
		return errors.New("issue dispatch currently supports --engine mock")
	}
	report, err := c.issueReadinessReport(*statePath, *issueValue, *repo)
	if err != nil {
		return err
	}
	plan, err := dispatch.Plan(dispatch.Input{
		Readiness: report,
		Prompt:    *prompt,
		Gates:     splitGateIDs(*gates),
	})
	if err != nil {
		if !report.Ready {
			printReadinessReport(c.err, report)
		}
		return err
	}
	st := store.NewJSONStore(*statePath)
	workers, err := st.ListWorkers()
	if err != nil {
		return err
	}
	if replay, ok := findDispatchReplay(workers, plan.RequestID); ok {
		printDispatchResult(c.out, plan, replay.Implementer, replay.Validator, true)
		return nil
	}

	now := c.now().UTC()
	implementer, validator, err := newValidationPair(plan.Issue, plan.Repo, *engine, plan.Prompt, plan.Gates, now)
	if err != nil {
		return err
	}
	event := store.Event{
		At:        now,
		Type:      "issue.dispatch",
		Message:   fmt.Sprintf("issue=%s request=%s", plan.Issue, plan.RequestID),
		Issue:     plan.Issue,
		RequestID: plan.RequestID,
	}
	implementer.Events = append(implementer.Events, event)
	validator.Events = append(validator.Events, event)
	if err := st.SaveWorkers(implementer, validator); err != nil {
		return err
	}
	printDispatchResult(c.out, plan, implementer, validator, false)
	return nil
}

func (c cli) issueReadinessReport(statePath, issueValue, repo string) (readiness.Report, error) {
	issue, err := normalizeRequiredIssue(issueValue)
	if err != nil {
		return readiness.Report{}, err
	}
	repoRoot, err := filepath.Abs(repo)
	if err != nil {
		return readiness.Report{}, fmt.Errorf("resolve repo: %w", err)
	}
	metadata, err := fetchIssueMetadata(context.Background(), issue)
	if err != nil {
		return readiness.Report{}, err
	}
	hints, _, ok, err := repohints.Load(repoRoot)
	if err != nil {
		return readiness.Report{}, err
	}
	var gates []readiness.Gate
	if ok {
		for _, gate := range hints.QualityGates {
			gates = append(gates, readiness.Gate{
				ID:      strings.TrimSpace(gate.ID),
				Command: strings.TrimSpace(gate.Command),
				Scope:   strings.TrimSpace(gate.Scope),
			})
		}
	}
	claimsForIssue, err := c.claimsForIssue(statePath, issue)
	if err != nil {
		return readiness.Report{}, err
	}
	var readinessClaims []readiness.Claim
	for _, claim := range claimsForIssue {
		readinessClaims = append(readinessClaims, readiness.Claim{
			ID:       claim.ID,
			WorkerID: claim.WorkerID,
			Scope:    claim.Scope,
			Status:   string(claim.Status),
		})
	}
	return readiness.Evaluate(readiness.Input{
		Issue: readiness.Issue{
			Ref:   issue,
			Title: metadata.Title,
			Body:  metadata.Body,
		},
		Repo:   repoRoot,
		Gates:  gates,
		Claims: readinessClaims,
	}), nil
}

type dispatchReplay struct {
	Implementer store.Worker
	Validator   store.Worker
}

func findDispatchReplay(workers []store.Worker, requestID string) (dispatchReplay, bool) {
	var replay dispatchReplay
	for _, worker := range workers {
		if !workerHasDispatchRequest(worker, requestID) {
			continue
		}
		switch worker.Role {
		case "implementer":
			replay.Implementer = worker
		case "validator":
			replay.Validator = worker
		}
	}
	return replay, replay.Implementer.ID != "" && replay.Validator.ID != ""
}

func workerHasDispatchRequest(worker store.Worker, requestID string) bool {
	for _, event := range worker.Events {
		if event.Type == "issue.dispatch" && event.RequestID == requestID {
			return true
		}
	}
	return false
}

func printDispatchResult(out io.Writer, plan dispatch.PlanResult, implementer, validator store.Worker, replayed bool) {
	fmt.Fprintf(out, "dispatch issue=%s implementer=%s validator=%s request=%s replayed=%t\n", plan.Issue, implementer.ID, validator.ID, plan.RequestID, replayed)
	fmt.Fprintf(out, "implementer: cs send %s \"continue\"\n", implementer.ID)
	if len(plan.Gates) > 0 {
		for _, gate := range plan.Gates {
			fmt.Fprintf(out, "gate: cs gate record --repo %s --worker %s --gate %s --exit-code <code> --output <summary>\n", plan.Repo, validator.ID, gate)
		}
	} else {
		fmt.Fprintf(out, "gate: cs gate record --repo %s --worker %s --gate <gate-id> --exit-code <code> --output <summary>\n", plan.Repo, validator.ID)
	}
	fmt.Fprintf(out, "issue report: cs issue report --issue %s --worker %s\n", plan.Issue, validator.ID)
}

func claimIssueMarkerMarkdown(issue string, all []store.Claim, now time.Time) (string, error) {
	payload, err := json.MarshalIndent(issueClaimSnapshot{
		Schema:      claimMarkerSchema,
		SnapshotID:  fmt.Sprintf("snap-%d", now.UTC().UnixNano()),
		Issue:       issue,
		GeneratedAt: now,
		MachineID:   currentMachineID(),
		Claims:      all,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode claim snapshot: %w", err)
	}
	return claimMarkerStart + "\n" + string(payload) + "\n" + claimMarkerEnd + "\n\n" + claimIssueMarkdown(issue, all, now), nil
}

func importClaimSnapshot(st *store.JSONStore, issue string, snapshot issueClaimSnapshot, force bool) (int, int, error) {
	plan, err := planClaimSnapshotImport(st, issue, snapshot, force)
	if err != nil {
		return 0, 0, err
	}
	if err := applyClaimImportPlan(st, &plan, force); err != nil {
		return plan.Imported, plan.Skipped, err
	}
	return plan.Imported, plan.Skipped, nil
}

func planClaimSnapshotImport(st *store.JSONStore, issue string, snapshot issueClaimSnapshot, force bool) (claimImportPlan, error) {
	var plan claimImportPlan
	workers, err := st.ListWorkers()
	if err != nil {
		return plan, err
	}
	workerSource := importedClaimWorkerSource(snapshot)
	for _, claim := range snapshot.Claims {
		claimIssue := strings.TrimSpace(claim.Issue)
		if claimIssue != "" && claimIssue != issue {
			return plan, fmt.Errorf("claim %q is for %s, expected %s", claim.ID, claimIssue, issue)
		}
		claim.Issue = issue
		claim = normalizeImportedClaimWorker(claim, workers, workerSource)
		local, err := st.GetClaim(claim.ID)
		if err != nil && !errors.Is(err, store.ErrClaimNotFound) {
			return plan, err
		}
		if err == nil && !force && local.UpdatedAt.After(claim.UpdatedAt) {
			plan.Skipped++
			plan.Conflicted++
			plan.Entries = append(plan.Entries, claimImportPlanEntry{Claim: claim, Action: "skip", Reason: "newer local claim"})
			continue
		}
		plan.Imported++
		plan.Entries = append(plan.Entries, claimImportPlanEntry{Claim: claim, Action: "import"})
	}
	return plan, nil
}

func normalizeImportedClaimWorker(claim store.Claim, workers []store.Worker, source string) store.Claim {
	if strings.TrimSpace(claim.WorkerID) == "" {
		return claim
	}
	if claims.IsExternalWorker(claim) {
		return claims.MarkExternalWorkerWithSource(claim, source)
	}
	for _, worker := range workers {
		if worker.ID == claim.WorkerID && claims.WorkerMatchesRepo(worker, claim.Repo) {
			return claim
		}
	}
	return claims.MarkExternalWorkerWithSource(claim, source)
}

func importedClaimWorkerSource(snapshot issueClaimSnapshot) string {
	if strings.TrimSpace(snapshot.MachineID) != "" {
		return "issue:" + strings.TrimSpace(snapshot.MachineID)
	}
	if strings.TrimSpace(snapshot.SnapshotID) != "" {
		return "issue:" + strings.TrimSpace(snapshot.SnapshotID)
	}
	if strings.TrimSpace(snapshot.Issue) != "" {
		return "issue:" + strings.TrimSpace(snapshot.Issue)
	}
	return "issue"
}

func applyClaimImportPlan(st *store.JSONStore, plan *claimImportPlan, force bool) error {
	var imports []store.Claim
	for _, entry := range plan.Entries {
		if entry.Action != "import" {
			continue
		}
		imports = append(imports, entry.Claim)
	}
	imported, skipped, conflicted, err := st.ImportClaims(imports, force)
	if err != nil {
		return err
	}
	plan.Imported = imported
	plan.Skipped += skipped
	plan.Conflicted += conflicted
	return nil
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
	fmt.Fprintf(&buf, "- Status: `%s`\n", displayWorkerStatus(worker))
	if worker.Role != "" {
		fmt.Fprintf(&buf, "- Role: `%s`\n", worker.Role)
	}
	if worker.ValidationOf != "" {
		fmt.Fprintf(&buf, "- Validation of: `%s`\n", worker.ValidationOf)
	}
	if worker.ValidationStatus != "" {
		fmt.Fprintf(&buf, "- Validation status: `%s`\n", worker.ValidationStatus)
	}
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

type ghIssueMetadata struct {
	Title string `json:"title"`
	Body  string `json:"body"`
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

func currentMachineID() string {
	if id := strings.TrimSpace(os.Getenv("CODEX_SWARM_MACHINE_ID")); id != "" {
		return id
	}
	return ""
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

func fetchIssueMetadata(ctx context.Context, issue string) (ghIssueMetadata, error) {
	ref, err := gh.ParseIssueRef(issue)
	if err != nil {
		return ghIssueMetadata{}, err
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
		return ghIssueMetadata{}, fmt.Errorf("read GitHub issue metadata: %s", message)
	}
	var metadata ghIssueMetadata
	if err := json.Unmarshal(stdout.Bytes(), &metadata); err != nil {
		return ghIssueMetadata{}, fmt.Errorf("parse GitHub issue metadata: %w", err)
	}
	return metadata, nil
}

func printReadinessReport(out io.Writer, report readiness.Report) {
	fmt.Fprintf(out, "ready=%t issue=%s repo=%s blockers=%d\n", report.Ready, report.Issue.Ref, report.Repo, len(report.Blockers))
	if report.Issue.Title != "" {
		fmt.Fprintf(out, "title=%s\n", report.Issue.Title)
	}
	for _, gate := range report.Gates {
		fmt.Fprintf(out, "gate=%s scope=%s command=%s\n", gate.ID, emptyDash(gate.Scope), gate.Command)
	}
	for _, claim := range report.Claims {
		fmt.Fprintf(out, "claim=%s status=%s worker=%s scope=%s\n", claim.ID, claim.Status, emptyDash(claim.WorkerID), emptyDash(claim.Scope))
	}
	for _, blocker := range report.Blockers {
		fmt.Fprintf(out, "blocker=%s\n", blocker)
	}
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
