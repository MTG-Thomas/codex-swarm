package store

import "time"

type WorkerStatus string

const (
	WorkerPending WorkerStatus = "pending"
	WorkerRunning WorkerStatus = "running"
	WorkerIdle    WorkerStatus = "idle"
	WorkerDone    WorkerStatus = "done"
	WorkerFailed  WorkerStatus = "failed"
)

type Worker struct {
	ID          string
	ProjectRoot string
	Worktree    string
	Branch      string
	ThreadID    string
	Status      WorkerStatus
	UpdatedAt   time.Time
}

type Store interface {
	SaveWorker(worker Worker) error
	GetWorker(id string) (Worker, error)
	ListWorkers() ([]Worker, error)
}
