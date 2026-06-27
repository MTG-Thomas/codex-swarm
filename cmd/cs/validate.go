package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const (
	ValidationPending  = "pending"
	ValidationApproved = "approved"
	ValidationRejected = "rejected"
)

func (c cli) validate(args []string) error {
	if len(args) == 0 {
		return errors.New("validate requires <start>")
	}
	switch args[0] {
	case "start":
		return c.validateStart(args[1:])
	default:
		return fmt.Errorf("unknown validate command %q", args[0])
	}
}

func (c cli) validateStart(args []string) error {
	fs := c.flagSet("validate start")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	issueValue := fs.String("issue", "", "GitHub issue reference, for example owner/repo#123")
	prompt := fs.String("prompt", "", "implementer prompt")
	engine := fs.String("engine", "mock", "worker engine: mock")
	gates := fs.String("gate", "", "comma-separated quality gate ids for the validator")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*prompt) == "" {
		return errors.New("validate start requires --prompt")
	}
	if strings.TrimSpace(*issueValue) == "" {
		return errors.New("validate start requires --issue")
	}
	if *engine != "mock" {
		return errors.New("validate start currently supports --engine mock")
	}
	ref, err := gh.ParseIssueRef(*issueValue)
	if err != nil {
		return err
	}
	issue := ref.String()
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	now := c.now().UTC()
	gateList := splitGateIDs(*gates)
	implementer, validator, err := newValidationPair(issue, repoRoot, *engine, *prompt, gateList, now)
	if err != nil {
		return err
	}

	st := store.NewJSONStore(*statePath)
	if err := st.SaveWorker(implementer); err != nil {
		return err
	}
	if err := st.SaveWorker(validator); err != nil {
		return err
	}

	fmt.Fprintf(c.out, "validation issue=%s implementer=%s validator=%s status=%s\n", issue, implementer.ID, validator.ID, validator.ValidationStatus)
	fmt.Fprintf(c.out, "implementer: cs send %s \"continue\"\n", implementer.ID)
	if len(gateList) > 0 {
		for _, gate := range gateList {
			fmt.Fprintf(c.out, "gate: cs gate record --repo %s --worker %s --gate %s --exit-code <code> --output <summary>\n", repoRoot, validator.ID, gate)
		}
	} else {
		fmt.Fprintf(c.out, "gate: cs gate record --repo %s --worker %s --gate <gate-id> --exit-code <code> --output <summary>\n", repoRoot, validator.ID)
	}
	fmt.Fprintf(c.out, "approve: cs report --note \"approved: <summary>\" %s done\n", validator.ID)
	fmt.Fprintf(c.out, "reject: cs report --note \"rejected: <findings>\" %s failed\n", validator.ID)
	fmt.Fprintf(c.out, "issue report: cs issue report --issue %s --worker %s\n", issue, validator.ID)
	return nil
}

func newValidationPair(issue, repoRoot, engine, prompt string, gates []string, now time.Time) (store.Worker, store.Worker, error) {
	implementerID, err := newWorkerID(now)
	if err != nil {
		return store.Worker{}, store.Worker{}, fmt.Errorf("generate implementer id for issue %s repo %s: %w", issue, repoRoot, err)
	}
	validatorID, err := newWorkerID(now)
	if err != nil {
		return store.Worker{}, store.Worker{}, fmt.Errorf("generate validator id for issue %s repo %s: %w", issue, repoRoot, err)
	}
	implementer := newValidationWorker(implementerID, "", "implementer", issue, repoRoot, engine, prompt, now)
	validatorPrompt := validatorPrompt(implementer, gates)
	validator := newValidationWorker(validatorID, implementer.ID, "validator", issue, repoRoot, engine, validatorPrompt, now)
	validator.ValidationOf = implementer.ID
	validator.ValidationStatus = ValidationPending
	validator.Events = append(validator.Events, store.Event{
		At:      now,
		Type:    "validation.started",
		Message: fmt.Sprintf("validation_of=%s gates=%s", implementer.ID, strings.Join(gates, ",")),
	})
	return implementer, validator, nil
}

func newValidationWorker(id, parentID, role, issue, repoRoot, engine, prompt string, now time.Time) store.Worker {
	worker := store.Worker{
		ID:          id,
		ParentID:    parentID,
		Role:        role,
		Issue:       issue,
		ProjectRoot: repoRoot,
		Worktree:    filepath.Join(repoRoot, ".codex-swarm", "worktrees", id),
		Branch:      "cs/" + id,
		ThreadID:    fmt.Sprintf("mock-thread-%s", id),
		Engine:      engine,
		Status:      store.WorkerIdle,
		Prompt:      strings.TrimSpace(prompt),
		LastMessage: mockSummary(prompt),
		CreatedAt:   now,
		UpdatedAt:   now,
		Events: []store.Event{
			{At: now, Type: "spawned", Message: "worker created"},
			{At: now, Type: "mock.turn.completed", Message: mockSummary(prompt)},
		},
	}
	worker.ApplyStatusAt(store.WorkerIdle, now)
	return worker
}

func validatorPrompt(implementer store.Worker, gates []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Validate worker %s for issue %s.\n", implementer.ID, implementer.Issue)
	fmt.Fprintf(&b, "Repo: %s\n", implementer.ProjectRoot)
	if implementer.Worktree != "" {
		fmt.Fprintf(&b, "Implementation worktree: %s\n", implementer.Worktree)
	}
	if implementer.Branch != "" {
		fmt.Fprintf(&b, "Implementation branch: %s\n", implementer.Branch)
	}
	if len(gates) > 0 {
		fmt.Fprintf(&b, "Required gates: %s\n", strings.Join(gates, ", "))
	}
	b.WriteString("Review the final diff and gate evidence. Approve with actionable proof or reject with actionable findings.")
	return b.String()
}

func splitGateIDs(value string) []string {
	parts := strings.Split(value, ",")
	gates := []string(nil)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			gates = append(gates, part)
		}
	}
	return gates
}
