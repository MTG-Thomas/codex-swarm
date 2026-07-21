package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
	"github.com/MTG-Thomas/codex-swarm/internal/version"
)

type workerStatusView struct {
	Status  protocol.Status         `json:"status"`
	Shown   int                     `json:"shown"`
	Total   int                     `json:"total"`
	Since   string                  `json:"since,omitempty"`
	Workers []protocol.WorkerStatus `json:"workers"`
}

func (c cli) statusWorkers(args []string) error {
	fs := c.flagSet("status")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL, for example http://127.0.0.1:8787")
	issues := fs.Bool("issues", false, "print compact issue and worker operations dashboard")
	detail := fs.Bool("detail", false, "include lower-priority dashboard rows")
	all := fs.Bool("all", false, "show complete worker history")
	since := fs.Duration("since", 24*time.Hour, "include recently updated workers")
	repo := fs.String("repo", "", "filter by repository root")
	statusFilter := fs.String("status", "", "comma-separated display statuses")
	limit := fs.Int("limit", 50, "maximum worker rows, zero for unlimited")
	jsonOutput := fs.Bool("json", false, "print structured JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *issues {
		return c.statusIssues(*statePath, *detail)
	}
	if *since < 0 {
		return errors.New("status --since must not be negative")
	}
	if *limit < 0 {
		return errors.New("status --limit must not be negative")
	}

	status, workers, err := c.readWorkerStatus(*statePath, *daemonURL)
	if err != nil {
		return err
	}
	repoFilter := ""
	if strings.TrimSpace(*repo) != "" {
		repoFilter, err = filepath.Abs(*repo)
		if err != nil {
			return fmt.Errorf("resolve status repo: %w", err)
		}
	}
	statuses := parseStatusFilter(*statusFilter)
	cutoff := c.now().UTC().Add(-*since)
	filtered := make([]protocol.WorkerStatus, 0, len(workers))
	for _, worker := range workers {
		if repoFilter != "" && !sameStatusRepo(worker.Repo, repoFilter) {
			continue
		}
		if len(statuses) > 0 {
			if _, ok := statuses[strings.ToLower(worker.Status)]; !ok {
				continue
			}
		} else if !*all && worker.UpdatedAt.Before(cutoff) && worker.Status != "working" && worker.Status != "pending" {
			continue
		}
		filtered = append(filtered, worker)
	}
	if *limit > 0 && len(filtered) > *limit {
		filtered = filtered[:*limit]
	}
	view := workerStatusView{Status: status, Shown: len(filtered), Total: len(workers), Workers: filtered}
	if !*all {
		view.Since = since.String()
	}
	if *jsonOutput {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintln(c.out, status.String())
	fmt.Fprintf(c.out, "view shown=%d total=%d since=%s repo=%s status=%s limit=%d\n", view.Shown, view.Total, emptyDash(view.Since), emptyDash(repoFilter), emptyDash(*statusFilter), *limit)
	for _, worker := range filtered {
		fmt.Fprintf(c.out, "%s\t%s\t%s\t%s\t%s\n", worker.ID, worker.Status, emptyDash(worker.Engine), emptyDash(worker.ThreadID), short(worker.Prompt, 60))
	}
	return nil
}

func (c cli) readWorkerStatus(statePath, daemonURL string) (protocol.Status, []protocol.WorkerStatus, error) {
	if baseURL := configuredDaemonURL(daemonURL); baseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := daemon.Client{BaseURL: baseURL}
		status, err := client.Status(ctx)
		if err != nil {
			return protocol.Status{}, nil, fmt.Errorf("daemon status: %w", err)
		}
		response, err := client.Workers(ctx)
		if err != nil {
			return protocol.Status{}, nil, fmt.Errorf("daemon workers: %w", err)
		}
		return status, response.Workers, nil
	}
	st := store.NewJSONStore(statePath)
	workers, err := st.ListWorkers()
	if err != nil {
		return protocol.Status{}, nil, err
	}
	metrics, err := st.CoordinationMetrics(c.now().UTC())
	if err != nil {
		return protocol.Status{}, nil, err
	}
	claimList, err := st.ListClaims()
	if err != nil {
		return protocol.Status{}, nil, err
	}
	status := protocol.Status{
		Daemon: "direct", Version: version.Version, StatePath: statePath, Backend: metrics.Backend,
		WorkerCount: metrics.WorkerCount, ActiveWorkerCount: metrics.ActiveWorkers, LiveMessageWorkers: metrics.LiveMessageWorkers,
		ResumeWorkers: metrics.ResumeWorkers, ManagedWorktreeWorkers: metrics.ManagedWorktreeWorkers, AutomaticCompletionWorkers: metrics.AutomaticCompletionWorkers,
		ExternalTrackerWorkers: metrics.ExternalTrackerWorkers, SteerableWorkers: metrics.SteerableWorkers,
		ClaimCount: metrics.ClaimCount, ActiveClaimCount: metrics.ActiveClaims, ConflictCount: countClaimConflicts(claimList, c.now().UTC()),
		MessageCount: metrics.MessageCount, QueuedMessages: metrics.QueuedMessages, SteeredMessages: metrics.SteeredMessages,
		DeliveredMessages: metrics.DeliveredMessages, RecentTouches: metrics.RecentTouches, ConflictMessages: metrics.ConflictMessages,
	}
	return status, summarizeStatusWorkers(workers), nil
}

func summarizeStatusWorkers(workers []store.Worker) []protocol.WorkerStatus {
	result := make([]protocol.WorkerStatus, 0, len(workers))
	for _, worker := range workers {
		result = append(result, protocol.WorkerStatus{
			ID: worker.ID, Status: displayWorkerStatus(worker), Role: worker.Role, Issue: worker.Issue,
			ValidationOf: worker.ValidationOf, ValidationStatus: worker.ValidationStatus, Worktree: truthfulWorkerWorktree(worker),
			Repo: worker.ProjectRoot, Engine: worker.Engine, Capabilities: store.CapabilitiesForWorker(worker).Strings(), ThreadID: worker.ThreadID, Prompt: worker.Prompt, UpdatedAt: worker.UpdatedAt,
		})
	}
	return result
}

func truthfulWorkerWorktree(worker store.Worker) string {
	if store.CapabilitiesForWorker(worker).Has(store.CapabilityManagedWorktree) {
		return worker.Worktree
	}
	return ""
}

func parseStatusFilter(value string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}

func sameStatusRepo(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func countClaimConflicts(all []store.Claim, now time.Time) int {
	count := 0
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if claims.Conflicts(all[i], all[j], now) {
				count++
			}
		}
	}
	return count
}
