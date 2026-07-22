package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/attention"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type attentionView struct {
	StatePath  string           `json:"state_path"`
	StaleAfter string           `json:"stale_after"`
	Shown      int              `json:"shown"`
	Total      int              `json:"total"`
	Counts     map[string]int   `json:"counts"`
	Items      []attention.Item `json:"items"`
}

func (c cli) attention(args []string) error {
	fs := c.flagSet("attention")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", "", "filter by repository root")
	workerID := fs.String("worker", "", "filter by worker expected to act")
	issue := fs.String("issue", "", "filter by GitHub issue reference")
	kindsValue := fs.String("kind", "", "comma-separated attention kinds")
	staleAfter := fs.Duration("stale-after", 24*time.Hour, "age after which a non-terminal worker needs attention")
	limit := fs.Int("limit", 100, "maximum rows, zero for unlimited")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("attention does not accept positional arguments")
	}
	if *staleAfter <= 0 {
		return errors.New("attention --stale-after must be positive")
	}
	if *limit < 0 {
		return errors.New("attention --limit must not be negative")
	}
	kinds, err := parseAttentionKinds(*kindsValue)
	if err != nil {
		return err
	}
	repoFilter := ""
	if strings.TrimSpace(*repo) != "" {
		repoFilter, err = filepath.Abs(*repo)
		if err != nil {
			return fmt.Errorf("resolve attention repo: %w", err)
		}
	}

	st := store.NewJSONStore(*statePath)
	workers, err := st.ListWorkers()
	if err != nil {
		return fmt.Errorf("read attention workers: %w", err)
	}
	claims, err := st.ListClaims()
	if err != nil {
		return fmt.Errorf("read attention claims: %w", err)
	}
	messages, err := st.ListAllQueuedMessages()
	if err != nil {
		return fmt.Errorf("read attention messages: %w", err)
	}
	gates, err := st.ListGateEvidence()
	if err != nil {
		return fmt.Errorf("read attention gates: %w", err)
	}

	derived := attention.Derive(attention.Input{
		Workers: workers, Claims: claims, Messages: messages, GateEvidence: gates,
		Now: c.now().UTC(), StaleAfter: *staleAfter,
	})
	filtered := make([]attention.Item, 0, len(derived))
	for _, item := range derived {
		if repoFilter != "" && !sameStatusRepo(item.Repo, repoFilter) {
			continue
		}
		if value := strings.TrimSpace(*workerID); value != "" && item.WorkerID != value {
			continue
		}
		if value := strings.TrimSpace(*issue); value != "" && !strings.EqualFold(item.Issue, value) {
			continue
		}
		if len(kinds) > 0 {
			if _, ok := kinds[item.Kind]; !ok {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	counts := attentionCounts(filtered)
	view := attentionView{
		StatePath: *statePath, StaleAfter: staleAfter.String(), Total: len(filtered), Counts: counts,
	}
	if *limit > 0 && len(filtered) > *limit {
		view.Items = filtered[:*limit]
	} else {
		view.Items = filtered
	}
	view.Shown = len(view.Items)
	if *jsonOutput {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return fmt.Errorf("encode attention view: %w", err)
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}

	fmt.Fprintf(c.out, "attention shown=%d total=%d queued_message=%d blocked_claim=%d stale_worker=%d validator_rejected=%d failed_gate=%d pr_next_action=%d stale_after=%s state=%s\n",
		view.Shown, view.Total, counts[attention.KindQueuedMessage], counts[attention.KindBlockedClaim], counts[attention.KindStaleWorker],
		counts[attention.KindValidatorRejected], counts[attention.KindFailedGate], counts[attention.KindPullRequest], view.StaleAfter, view.StatePath)
	for _, item := range view.Items {
		fmt.Fprintf(c.out, "kind=%s\tid=%s\tworker=%s\tissue=%s\trepo=%s\tupdated=%s\tnext=%q\tdetail=%q\n",
			item.Kind, item.ID, emptyDash(item.WorkerID), emptyDash(item.Issue), emptyDash(item.Repo),
			item.UpdatedAt.UTC().Format(time.RFC3339), item.NextAction, item.Detail)
	}
	return nil
}

func parseAttentionKinds(value string) (map[string]struct{}, error) {
	known := map[string]struct{}{
		attention.KindQueuedMessage: {}, attention.KindBlockedClaim: {}, attention.KindStaleWorker: {},
		attention.KindValidatorRejected: {}, attention.KindFailedGate: {}, attention.KindPullRequest: {},
	}
	result := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := known[item]; !ok {
			return nil, fmt.Errorf("unknown attention kind %q", item)
		}
		result[item] = struct{}{}
	}
	return result, nil
}

func attentionCounts(items []attention.Item) map[string]int {
	counts := map[string]int{
		attention.KindQueuedMessage: 0, attention.KindBlockedClaim: 0, attention.KindStaleWorker: 0,
		attention.KindValidatorRejected: 0, attention.KindFailedGate: 0, attention.KindPullRequest: 0,
	}
	for _, item := range items {
		counts[item.Kind]++
	}
	return counts
}
