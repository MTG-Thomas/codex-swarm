package dispatch

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const (
	ValidationPending  = "pending"
	ValidationApproved = "approved"
	ValidationRejected = "rejected"
)

func NewWorkerPair(issue, repoRoot, engine, prompt string, gates []string, now time.Time) (store.Worker, store.Worker, error) {
	implementerID, err := NewWorkerID(now)
	if err != nil {
		return store.Worker{}, store.Worker{}, fmt.Errorf("generate implementer id for issue %s repo %s: %w", issue, repoRoot, err)
	}
	validatorID, err := NewWorkerID(now)
	if err != nil {
		return store.Worker{}, store.Worker{}, fmt.Errorf("generate validator id for issue %s repo %s: %w", issue, repoRoot, err)
	}
	implementer := newWorker(implementerID, "", "implementer", issue, repoRoot, engine, prompt, now)
	validatorPrompt := ValidatorPrompt(implementer, gates)
	validator := newWorker(validatorID, implementer.ID, "validator", issue, repoRoot, engine, validatorPrompt, now)
	validator.ValidationOf = implementer.ID
	validator.ValidationStatus = ValidationPending
	validator.Events = append(validator.Events, store.Event{
		At:      now,
		Type:    "validation.started",
		Message: fmt.Sprintf("validation_of=%s gates=%s", implementer.ID, strings.Join(gates, ",")),
	})
	return implementer, validator, nil
}

func NewWorkerID(now time.Time) (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("w-%s-%s", now.UTC().Format("20060102-150405"), hex.EncodeToString(buf)), nil
}

func newWorker(id, parentID, role, issue, repoRoot, engine, prompt string, now time.Time) store.Worker {
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
		LastMessage: MockSummary(prompt),
		CreatedAt:   now,
		UpdatedAt:   now,
		Events: []store.Event{
			{At: now, Type: "spawned", Message: "worker created"},
			{At: now, Type: "mock.turn.completed", Message: MockSummary(prompt)},
		},
	}
	if worker.Engine == "appserver" {
		worker.RuntimeOwner = store.RuntimeOwnerCS
	}
	worker.ApplyStatusAt(store.WorkerIdle, now)
	return worker
}

func ValidatorPrompt(implementer store.Worker, gates []string) string {
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

func MockSummary(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) > 96 {
		prompt = prompt[:96] + "..."
	}
	return "mock worker accepted: " + prompt
}
