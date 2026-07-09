package dispatch

import (
	"fmt"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type WorkerStore interface {
	ListWorkers() ([]store.Worker, error)
	SaveWorkers(...store.Worker) error
}

type Result struct {
	RequestID   string `json:"request_id"`
	Implementer string `json:"implementer"`
	Validator   string `json:"validator"`
	Replayed    bool   `json:"replayed"`
}

func Execute(st WorkerStore, plan PlanResult, requestID, engine string, now time.Time) (Result, error) {
	if st == nil {
		return Result{}, fmt.Errorf("worker store is required")
	}
	if requestID == "" {
		requestID = plan.RequestID
	}
	workers, err := st.ListWorkers()
	if err != nil {
		return Result{}, err
	}
	if implementer, validator, ok := Replay(workers, requestID); ok {
		if implementer.Prompt != plan.Prompt || validator.Issue != plan.Issue || validator.ProjectRoot != plan.Repo {
			return Result{}, fmt.Errorf("request %q for dispatch does not match original mutation fingerprint", requestID)
		}
		return Result{RequestID: requestID, Implementer: implementer.ID, Validator: validator.ID, Replayed: true}, nil
	}
	implementer, validator, err := NewWorkerPair(plan.Issue, plan.Repo, engine, plan.Prompt, plan.Gates, now)
	if err != nil {
		return Result{}, err
	}
	event := store.Event{
		At:        now,
		Type:      "issue.dispatch",
		Message:   fmt.Sprintf("issue=%s request=%s", plan.Issue, requestID),
		Issue:     plan.Issue,
		RequestID: requestID,
	}
	implementer.Events = append(implementer.Events, event)
	validator.Events = append(validator.Events, event)
	if err := st.SaveWorkers(implementer, validator); err != nil {
		return Result{}, err
	}
	return Result{RequestID: requestID, Implementer: implementer.ID, Validator: validator.ID}, nil
}

func Replay(workers []store.Worker, requestID string) (store.Worker, store.Worker, bool) {
	var implementer, validator store.Worker
	for _, worker := range workers {
		if !hasDispatchRequest(worker, requestID) {
			continue
		}
		switch worker.Role {
		case "implementer":
			implementer = worker
		case "validator":
			validator = worker
		}
	}
	return implementer, validator, implementer.ID != "" && validator.ID != ""
}

func hasDispatchRequest(worker store.Worker, requestID string) bool {
	for _, event := range worker.Events {
		if event.Type == "issue.dispatch" && event.RequestID == requestID {
			return true
		}
	}
	return false
}
