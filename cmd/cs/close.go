package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) closeWorker(args []string) error {
	fs := c.flagSet("close")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	daemonURL := fs.String("daemon", "", "daemon base URL for completion delivery")
	statusValue := fs.String("status", "done", "terminal status: done or failed")
	note := fs.String("note", "", "operator closeout summary")
	refreshPR := fs.Bool("refresh-pr", true, "refresh attached pull requests before closeout")
	timeout := fs.Duration("timeout", 30*time.Second, "pull request refresh timeout")
	requestIDFlag := fs.String("request-id", "", "idempotency key")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("close requires <worker>")
	}
	workerID := strings.TrimSpace(fs.Arg(0))
	status := store.WorkerStatus(strings.ToLower(strings.TrimSpace(*statusValue)))
	if status != store.WorkerDone && status != store.WorkerFailed {
		return errors.New("close --status must be done or failed")
	}
	st := store.NewJSONStore(*statePath)
	worker, err := st.GetWorker(workerID)
	if err != nil {
		if errors.Is(err, store.ErrWorkerNotFound) {
			return fmt.Errorf("worker %q not found", workerID)
		}
		return err
	}
	pullRequests := worker.PullRequests
	if *refreshPR && len(pullRequests) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		pullRequests = make([]store.PullRequestState, 0, len(worker.PullRequests))
		for _, attached := range worker.PullRequests {
			statusReadback, err := (gh.CLIPRStatusProvider{}).PullRequestStatus(ctx, attached.URL)
			if err != nil {
				return fmt.Errorf("refresh worker %s pull request %s: %w", worker.ID, attached.URL, err)
			}
			pullRequests = append(pullRequests, pullRequestStateFromStatus(statusReadback, c.now().UTC()))
		}
	}
	now := c.now().UTC()
	requestID, err := c.requestID(*requestIDFlag, now)
	if err != nil {
		return err
	}
	report := string(status)
	if strings.TrimSpace(*note) != "" {
		report += ": " + strings.TrimSpace(*note)
	}
	fingerprint := closeFingerprint(workerID, status, report, pullRequests)
	result, err := st.CloseWorker(store.CloseWorkerRequest{
		RequestID: requestID, Fingerprint: fingerprint, WorkerID: workerID, Status: status,
		Report: report, PullRequests: pullRequests, At: now,
	})
	if err != nil {
		return err
	}
	completion, err := c.forwardCompletion(*statePath, *daemonURL, requestID+"-completion", workerID, result.Worker.Report)
	if err != nil {
		return fmt.Errorf("worker %s closed but completion forwarding failed: %w", workerID, err)
	}
	response := protocol.CloseResponse{
		Worker: result.Worker, ReleasedClaims: result.ReleasedClaims, Replayed: result.Replayed, Completion: completion,
	}
	if *jsonOutput {
		return json.NewEncoder(c.out).Encode(response)
	}
	fmt.Fprintf(c.out, "closed worker=%s status=%s released_claims=%d prs=%d replayed=%t\n", result.Worker.ID, displayWorkerStatus(result.Worker), len(result.ReleasedClaims), len(result.Worker.PullRequests), result.Replayed)
	c.printCompletionResponse(workerID, completion)
	return nil
}

func closeFingerprint(workerID string, status store.WorkerStatus, report string, pullRequests []store.PullRequestState) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s\x00%s\x00%s", workerID, status, report)
	for _, pullRequest := range pullRequests {
		fmt.Fprintf(&builder, "\x00%s\x00%s\x00%s", pullRequest.URL, pullRequest.State, pullRequest.NextAction)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}
