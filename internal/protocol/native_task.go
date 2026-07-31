package protocol

import "github.com/MTG-Thomas/codex-swarm/internal/store"

// NativeTaskCreateRequest is the host action emitted after cs reserves a
// worker. The Codex host resolves ProjectRoot to its saved project ID, creates
// the task, then binds the returned identity.
type NativeTaskCreateRequest struct {
	WorkerID       string                      `json:"worker_id"`
	ParentWorkerID string                      `json:"parent_worker_id,omitempty"`
	ProjectRoot    string                      `json:"project_root"`
	Prompt         string                      `json:"prompt"`
	Environment    store.NativeTaskEnvironment `json:"environment"`
	StatePath      string                      `json:"state_path"`
	BindRequestID  string                      `json:"bind_request_id"`
}

// DispatchPrepareResponse is the machine-readable prepare contract.
type DispatchPrepareResponse struct {
	Worker           store.Worker            `json:"worker"`
	NativeTaskCreate NativeTaskCreateRequest `json:"native_task_create"`
	Replayed         bool                    `json:"replayed"`
}

// DispatchBindResponse is the machine-readable binding readback.
type DispatchBindResponse struct {
	Worker   store.Worker `json:"worker"`
	Replayed bool         `json:"replayed"`
}
