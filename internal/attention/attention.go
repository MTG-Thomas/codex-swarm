// Package attention derives actionable open loops from the authoritative
// coordination records. It does not persist a separate ledger.
package attention

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const (
	KindQueuedMessage     = "queued_message"
	KindBlockedClaim      = "blocked_claim"
	KindStaleWorker       = "stale_worker"
	KindValidatorRejected = "validator_rejected"
	KindFailedGate        = "failed_gate"
	KindPullRequest       = "pr_next_action"
)

// Input is the current authoritative state needed to derive attention items.
type Input struct {
	Workers      []store.Worker
	Claims       []store.Claim
	Messages     []store.DeliveredMessage
	GateEvidence []store.GateEvidence
	Now          time.Time
	StaleAfter   time.Duration
}

// Item is one actionable open loop. ID identifies the source record while
// WorkerID identifies the worker expected to take the next action.
type Item struct {
	Kind       string    `json:"kind"`
	ID         string    `json:"id"`
	WorkerID   string    `json:"worker_id,omitempty"`
	Issue      string    `json:"issue,omitempty"`
	Repo       string    `json:"repo,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	NextAction string    `json:"next_action"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Derive projects open loops from existing records. Resolved history is
// suppressed: terminal workers are not stale, and only the newest result for a
// worker/gate/repo/scope tuple can produce a failed-gate item.
func Derive(input Input) []Item {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	staleAfter := input.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 24 * time.Hour
	}

	workers := make(map[string]store.Worker, len(input.Workers))
	for _, worker := range input.Workers {
		workers[worker.ID] = worker
	}

	items := make([]Item, 0)
	for _, message := range input.Messages {
		if message.Delivery.State != store.DeliveryQueued {
			continue
		}
		worker := workers[message.Delivery.RecipientID]
		detail := fmt.Sprintf("from=%s kind=%s", message.Message.From, message.Message.Kind)
		if message.Delivery.LastError != "" {
			detail += " error=" + message.Delivery.LastError
		}
		items = append(items, Item{
			Kind: KindQueuedMessage, ID: message.Delivery.ID, WorkerID: message.Delivery.RecipientID,
			Issue: worker.Issue, Repo: worker.ProjectRoot, Detail: detail,
			NextAction: "inspect-inbox", UpdatedAt: message.Delivery.UpdatedAt,
		})
	}

	for _, claim := range input.Claims {
		if claim.Status != store.ClaimBlocked {
			continue
		}
		next := strings.TrimSpace(claim.Next)
		if next == "" {
			next = "resolve-or-release"
		}
		detail := fmt.Sprintf("scope=%s", claim.Scope)
		if claim.Blocker != "" {
			detail += " blocker=" + claim.Blocker
		}
		items = append(items, Item{
			Kind: KindBlockedClaim, ID: claim.ID, WorkerID: claim.WorkerID,
			Issue: claim.Issue, Repo: claim.Repo, Detail: detail,
			NextAction: next, UpdatedAt: claim.UpdatedAt,
		})
	}

	for _, worker := range input.Workers {
		if !terminal(worker) && !worker.UpdatedAt.IsZero() && now.Sub(worker.UpdatedAt.UTC()) > staleAfter {
			items = append(items, Item{
				Kind: KindStaleWorker, ID: worker.ID, WorkerID: worker.ID,
				Issue: worker.Issue, Repo: worker.ProjectRoot,
				Detail:     fmt.Sprintf("status=%s stale_for=%s", displayStatus(worker), now.Sub(worker.UpdatedAt.UTC()).Round(time.Second)),
				NextAction: "resume-or-close", UpdatedAt: worker.UpdatedAt,
			})
		}
		if strings.EqualFold(strings.TrimSpace(worker.ValidationStatus), "rejected") {
			target, ok := workers[worker.ValidationOf]
			if ok && !terminal(target) {
				items = append(items, Item{
					Kind: KindValidatorRejected, ID: worker.ID, WorkerID: target.ID,
					Issue: firstNonEmpty(target.Issue, worker.Issue), Repo: firstNonEmpty(target.ProjectRoot, worker.ProjectRoot),
					Detail: "validator=" + worker.ID, NextAction: "revise-and-revalidate", UpdatedAt: worker.UpdatedAt,
				})
			}
		}
		for _, pr := range worker.PullRequests {
			next := strings.ToLower(strings.TrimSpace(pr.NextAction))
			if next == "" || next == "complete" || next == "closed" {
				continue
			}
			items = append(items, Item{
				Kind: KindPullRequest, ID: pr.URL, WorkerID: worker.ID,
				Issue: worker.Issue, Repo: worker.ProjectRoot,
				Detail:     fmt.Sprintf("state=%s checks=%s review=%s", emptyDash(pr.State), emptyDash(pr.CheckSummary), emptyDash(pr.ReviewDecision)),
				NextAction: next, UpdatedAt: pr.UpdatedAt,
			})
		}
	}

	latestGates := make(map[string]store.GateEvidence)
	for _, evidence := range input.GateEvidence {
		key := strings.Join([]string{evidence.WorkerID, evidence.GateID, evidence.Repo, evidence.Scope}, "\x00")
		current, ok := latestGates[key]
		if !ok || evidence.CreatedAt.After(current.CreatedAt) {
			latestGates[key] = evidence
		}
	}
	for _, evidence := range latestGates {
		if evidence.ExitCode == 0 {
			continue
		}
		worker, hasWorker := workers[evidence.WorkerID]
		if hasWorker && terminal(worker) {
			continue
		}
		items = append(items, Item{
			Kind: KindFailedGate, ID: evidence.ID, WorkerID: evidence.WorkerID,
			Issue: worker.Issue, Repo: firstNonEmpty(evidence.Repo, worker.ProjectRoot),
			Detail:     fmt.Sprintf("gate=%s exit_code=%d scope=%s", evidence.GateID, evidence.ExitCode, emptyDash(evidence.Scope)),
			NextAction: "fix-and-rerun", UpdatedAt: evidence.CreatedAt,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		left, right := priority(items[i].Kind), priority(items[j].Kind)
		if left != right {
			return left < right
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.Before(items[j].UpdatedAt)
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func terminal(worker store.Worker) bool {
	status := displayStatus(worker)
	return status == string(store.WorkerDone) || status == string(store.WorkerFailed)
}

func displayStatus(worker store.Worker) string {
	if worker.Lifecycle != nil {
		return string(worker.Lifecycle.DeriveStatus())
	}
	return string(worker.Status)
}

func priority(kind string) int {
	switch kind {
	case KindValidatorRejected, KindFailedGate, KindBlockedClaim:
		return 0
	case KindQueuedMessage, KindPullRequest:
		return 1
	default:
		return 2
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}
