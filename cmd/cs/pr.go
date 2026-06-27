package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) pr(args []string) error {
	if len(args) == 0 {
		return errors.New("pr requires <attach|status>")
	}
	switch args[0] {
	case "attach":
		return c.prAttach(args[1:])
	case "status":
		return c.prStatus(args[1:])
	default:
		return fmt.Errorf("unknown pr command %q", args[0])
	}
}

func (c cli) prAttach(args []string) error {
	fs := c.flagSet("pr attach")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	workerID := fs.String("worker", "", "worker to attach the pull request to")
	url := fs.String("url", "", "pull request URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*workerID) == "" {
		return errors.New("pr attach requires --worker")
	}
	if strings.TrimSpace(*url) == "" {
		return errors.New("pr attach requires --url")
	}
	now := c.now().UTC()
	worker, err := store.NewJSONStore(*statePath).UpdateWorker(strings.TrimSpace(*workerID), func(worker *store.Worker) error {
		upsertPullRequest(worker, store.PullRequestState{
			URL:       strings.TrimSpace(*url),
			UpdatedAt: now,
		})
		worker.Events = append(worker.Events, store.Event{
			At:       now,
			Type:     "pr.attach",
			Message:  strings.TrimSpace(*url),
			Issue:    worker.Issue,
			WorkerID: worker.ID,
		})
		worker.UpdatedAt = now
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "attached pr worker=%s url=%s\n", worker.ID, strings.TrimSpace(*url))
	return nil
}

func (c cli) prStatus(args []string) error {
	fs := c.flagSet("pr status")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	timeout := fs.Duration("timeout", 30*time.Second, "GitHub status timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("pr status requires <worker>")
	}
	workerID := strings.TrimSpace(fs.Arg(0))
	st := store.NewJSONStore(*statePath)
	worker, err := st.GetWorker(workerID)
	if err != nil {
		return err
	}
	if len(worker.PullRequests) == 0 {
		return fmt.Errorf("worker %s has no attached pull requests; attach one with: cs pr attach --worker %s --url <pr-url>", worker.ID, worker.ID)
	}
	url := worker.PullRequests[0].URL
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	status, err := (gh.CLIPRStatusProvider{}).PullRequestStatus(ctx, url)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	state := pullRequestStateFromStatus(status, now)
	worker, err = st.UpdateWorker(workerID, func(worker *store.Worker) error {
		upsertPullRequest(worker, state)
		worker.Events = append(worker.Events, store.Event{
			At:       now,
			Type:     "pr.status",
			Message:  fmt.Sprintf("url=%s checks=%s review=%s next=%s", state.URL, state.CheckSummary, emptyDash(state.ReviewDecision), state.NextAction),
			Issue:    worker.Issue,
			WorkerID: worker.ID,
		})
		worker.UpdatedAt = now
		return nil
	})
	if err != nil {
		return err
	}
	printPullRequestState(c.out, worker.ID, state)
	return nil
}

func pullRequestStateFromStatus(status gh.PullRequestStatus, now time.Time) store.PullRequestState {
	return store.PullRequestState{
		URL:              status.URL,
		State:            status.State,
		BaseBranch:       status.BaseBranch,
		HeadBranch:       status.HeadBranch,
		ReviewDecision:   status.ReviewDecision,
		CheckSummary:     status.CheckSummary(),
		CodeRabbitStatus: status.CodeRabbitStatus,
		NextAction:       status.NextAction(),
		UpdatedAt:        now,
	}
}

func upsertPullRequest(worker *store.Worker, pr store.PullRequestState) {
	pr.URL = strings.TrimSpace(pr.URL)
	for i := range worker.PullRequests {
		if worker.PullRequests[i].URL == pr.URL {
			worker.PullRequests[i] = pr
			return
		}
	}
	worker.PullRequests = append(worker.PullRequests, pr)
}

func printPullRequestState(out interface {
	Write([]byte) (int, error)
}, workerID string, pr store.PullRequestState) {
	fmt.Fprintf(out, "pr worker=%s url=%s state=%s base=%s head=%s\n", workerID, pr.URL, emptyDash(pr.State), emptyDash(pr.BaseBranch), emptyDash(pr.HeadBranch))
	fmt.Fprintf(out, "checks=%s review=%s coderabbit=%s next=%s\n", emptyDash(pr.CheckSummary), emptyDash(pr.ReviewDecision), emptyDash(pr.CodeRabbitStatus), emptyDash(pr.NextAction))
}
