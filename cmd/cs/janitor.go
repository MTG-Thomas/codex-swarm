package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type janitorCandidate struct {
	Claim  store.Claim
	Reason string
}

func (c cli) janitor(args []string) error {
	if len(args) == 0 {
		return errors.New("janitor requires <stale|release>")
	}
	switch args[0] {
	case "stale":
		return c.janitorStale(args[1:])
	case "release":
		return c.janitorRelease(args[1:])
	default:
		return fmt.Errorf("unknown janitor command %q", args[0])
	}
}

func (c cli) janitorStale(args []string) error {
	fs := c.flagSet("janitor stale")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	older := fs.Duration("older", 24*time.Hour, "worker stale threshold")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st := store.NewJSONStore(*statePath)
	workers, claimsList, err := janitorState(st)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	staleWorkers := janitorStaleWorkers(workers, now, *older)
	candidates := janitorClaimCandidates(claimsList, workers, now, *older)
	fmt.Fprintf(c.out, "janitor workers=%d claims=%d stale_workers=%d releasable_claims=%d older=%s state=%s\n", len(workers), len(claimsList), len(staleWorkers), len(candidates), older.String(), *statePath)
	for _, worker := range staleWorkers {
		fmt.Fprintf(c.out, "worker=%s status=%s updated=%s issue=%s next=resume-or-report\n", worker.ID, displayWorkerStatus(worker), worker.UpdatedAt.UTC().Format(time.RFC3339), emptyDash(worker.Issue))
	}
	for _, candidate := range candidates {
		fmt.Fprintf(c.out, "claim=%s worker=%s reason=%s scope=%s next=release\n", candidate.Claim.ID, emptyDash(candidate.Claim.WorkerID), candidate.Reason, candidate.Claim.Scope)
	}
	return nil
}

func (c cli) janitorRelease(args []string) error {
	fs := c.flagSet("janitor release")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	older := fs.Duration("older", 24*time.Hour, "worker stale threshold")
	note := fs.String("note", "released by cs janitor", "release note")
	apply := fs.Bool("apply", false, "release candidate claims")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st := store.NewJSONStore(*statePath)
	workers, claimsList, err := janitorState(st)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	candidates := janitorClaimCandidates(claimsList, workers, now, *older)
	if !*apply {
		fmt.Fprintf(c.out, "dry_run=true releasable_claims=%d state=%s\n", len(candidates), *statePath)
		for _, candidate := range candidates {
			fmt.Fprintf(c.out, "claim=%s worker=%s reason=%s scope=%s next=release --apply\n", candidate.Claim.ID, emptyDash(candidate.Claim.WorkerID), candidate.Reason, candidate.Claim.Scope)
		}
		return nil
	}
	released := 0
	for _, candidate := range candidates {
		claim := candidate.Claim
		claim.Status = store.ClaimReleased
		claim.Note = strings.TrimSpace(*note)
		claim.Blocker = ""
		claim.Next = ""
		claim.UpdatedAt = now
		if err := st.SaveClaim(claim); err != nil {
			return fmt.Errorf("release claim %s: %w", claim.ID, err)
		}
		released++
		fmt.Fprintf(c.out, "released %s reason=%s\n", claim.ID, candidate.Reason)
	}
	fmt.Fprintf(c.out, "released_claims=%d state=%s\n", released, *statePath)
	return nil
}

func janitorState(st *store.JSONStore) ([]store.Worker, []store.Claim, error) {
	workers, err := st.ListWorkers()
	if err != nil {
		return nil, nil, err
	}
	claimsList, err := st.ListClaims()
	if err != nil {
		return nil, nil, err
	}
	return workers, claimsList, nil
}

func janitorStaleWorkers(workers []store.Worker, now time.Time, older time.Duration) []store.Worker {
	var stale []store.Worker
	for _, worker := range workers {
		if isTerminalWorker(worker) || worker.UpdatedAt.IsZero() {
			continue
		}
		if now.Sub(worker.UpdatedAt.UTC()) > older {
			stale = append(stale, worker)
		}
	}
	return stale
}

func janitorClaimCandidates(claimsList []store.Claim, workers []store.Worker, now time.Time, older time.Duration) []janitorCandidate {
	byID := map[string]store.Worker{}
	for _, worker := range workers {
		byID[worker.ID] = worker
	}
	var candidates []janitorCandidate
	for _, claim := range claimsList {
		if claim.Status != store.ClaimActive {
			continue
		}
		reason := ""
		if !claims.IsOpen(claim, now) {
			reason = "expired"
		} else if strings.TrimSpace(claim.WorkerID) == "" {
			reason = "missing-worker"
		} else if worker, ok := byID[claim.WorkerID]; !ok {
			reason = "unknown-worker"
		} else if isTerminalWorker(worker) {
			reason = "terminal-worker"
		} else if !worker.UpdatedAt.IsZero() && now.Sub(worker.UpdatedAt.UTC()) > older {
			reason = "stale-worker"
		}
		if reason == "" {
			continue
		}
		candidates = append(candidates, janitorCandidate{Claim: claim, Reason: reason})
	}
	return candidates
}
