package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// CloseWorkerRequest is one idempotent local closeout transaction.
type CloseWorkerRequest struct {
	RequestID    string
	Fingerprint  string
	WorkerID     string
	Status       WorkerStatus
	Report       string
	PullRequests []PullRequestState
	At           time.Time
}

// CloseWorkerResult is the durable worker and claim state after closeout.
type CloseWorkerResult struct {
	Worker         Worker
	ReleasedClaims []Claim
	Replayed       bool
}

// CloseWorker atomically terminates a worker and releases all of its open claims.
func (s *JSONStore) CloseWorker(request CloseWorkerRequest) (CloseWorkerResult, error) {
	if strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.Fingerprint) == "" {
		return CloseWorkerResult{}, errors.New("close worker request id and fingerprint are required")
	}
	if request.Status != WorkerDone && request.Status != WorkerFailed {
		return CloseWorkerResult{}, fmt.Errorf("close worker status must be done or failed, got %q", request.Status)
	}
	request.At = request.At.UTC()
	var result CloseWorkerResult
	err := s.withStateLock(func() error {
		completed, found, err := getRecord[[]CompletedMutation](s.tx, "completed_mutations", "singleton")
		if err != nil {
			return err
		}
		if !found {
			completed = nil
		}
		for _, mutation := range completed {
			if mutation.RequestID != request.RequestID || mutation.Command != "worker.close" {
				continue
			}
			if mutation.Fingerprint != request.Fingerprint {
				return fmt.Errorf("request %q for worker.close does not match original mutation fingerprint", request.RequestID)
			}
			worker, found, err := getRecord[Worker](s.tx, "worker", request.WorkerID)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%w: %s", ErrWorkerNotFound, request.WorkerID)
			}
			normalizeWorkerLifecycleForRead(&worker)
			result.Worker = worker
			result.Replayed = true
			return nil
		}

		worker, found, err := getRecord[Worker](s.tx, "worker", request.WorkerID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: %s", ErrWorkerNotFound, request.WorkerID)
		}
		normalizeWorkerLifecycleForRead(&worker)
		worker.ApplyStatusAt(request.Status, request.At)
		worker.Report = strings.TrimSpace(request.Report)
		if request.PullRequests != nil {
			worker.PullRequests = append([]PullRequestState(nil), request.PullRequests...)
			for _, pullRequest := range request.PullRequests {
				worker.Events = append(worker.Events, Event{At: request.At, Type: "pr.status", Message: fmt.Sprintf("url=%s checks=%s review=%s next=%s", pullRequest.URL, pullRequest.CheckSummary, pullRequest.ReviewDecision, pullRequest.NextAction), WorkerID: worker.ID, Issue: worker.Issue, RequestID: request.RequestID})
			}
		}
		worker.Events = append(worker.Events, Event{At: request.At, Type: "closed", Message: worker.Report, WorkerID: worker.ID, Issue: worker.Issue, RequestID: request.RequestID})
		worker.UpdatedAt = request.At
		if err := s.upsert("worker", worker.ID, worker); err != nil {
			return err
		}

		claimList, err := listRecords[Claim](s.tx, "claim")
		if err != nil {
			return err
		}
		for _, claim := range claimList {
			if claim.WorkerID != worker.ID || (claim.Status != ClaimActive && claim.Status != ClaimBlocked) {
				continue
			}
			claim.Status = ClaimReleased
			claim.Blocker = ""
			claim.Next = ""
			claim.UpdatedAt = request.At
			if strings.TrimSpace(request.Report) != "" {
				claim.Note = strings.TrimSpace(request.Report)
			}
			if err := s.upsert("claim", claim.ID, claim); err != nil {
				return err
			}
			result.ReleasedClaims = append(result.ReleasedClaims, claim)
		}

		events, _, err := getRecord[[]Event](s.tx, "events", "singleton")
		if err != nil {
			return err
		}
		closeEvent := Event{At: request.At, Type: "worker.closed", Message: worker.Report, WorkerID: worker.ID, Issue: worker.Issue, RequestID: request.RequestID}
		events = appendBoundedEvents(events, []Event{closeEvent}, SwarmEventCap)
		if err := s.upsert("events", "singleton", events); err != nil {
			return err
		}
		completed = appendBoundedCompletedMutations(completed, CompletedMutation{
			RequestID: request.RequestID, Command: "worker.close", Fingerprint: request.Fingerprint,
			Output: worker.ID, CreatedAt: request.At,
		}, CompletedMutationCacheCap)
		if err := s.upsert("completed_mutations", "singleton", completed); err != nil {
			return err
		}
		result.Worker = worker
		return nil
	})
	return result, err
}
