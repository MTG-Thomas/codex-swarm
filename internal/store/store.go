package store

import (
	"errors"
	"time"
)

var ErrWorkerNotFound = errors.New("worker not found")

type WorkerStatus string

const (
	WorkerPending WorkerStatus = "pending"
	WorkerRunning WorkerStatus = "running"
	WorkerIdle    WorkerStatus = "idle"
	WorkerDone    WorkerStatus = "done"
	WorkerFailed  WorkerStatus = "failed"
)

type Worker struct {
	ID          string       `json:"id"`
	ParentID    string       `json:"parent_id,omitempty"`
	Role        string       `json:"role,omitempty"`
	Issue       string       `json:"issue,omitempty"`
	ProjectRoot string       `json:"project_root"`
	Worktree    string       `json:"worktree"`
	Branch      string       `json:"branch"`
	ThreadID    string       `json:"thread_id"`
	TurnID      string       `json:"turn_id,omitempty"`
	Engine      string       `json:"engine"`
	Status      WorkerStatus `json:"status"`
	Prompt      string       `json:"prompt"`
	LastMessage string       `json:"last_message,omitempty"`
	Report      string       `json:"report,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Events      []Event      `json:"events,omitempty"`
}

type Event struct {
	At      time.Time `json:"at"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
}

type Schedule struct {
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Prompt    string    `json:"prompt"`
	Cron      string    `json:"cron"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store interface {
	SaveWorker(worker Worker) error
	GetWorker(id string) (Worker, error)
	ListWorkers() ([]Worker, error)
}
