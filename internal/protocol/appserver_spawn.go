package protocol

// AppserverSpawnRequest asks the daemon to own one already-persisted worker's
// first Codex app-server turn. The request is intentionally narrow: workspace,
// transport, and worker identity come from the durable worker record.
type AppserverSpawnRequest struct {
	RequestID string `json:"request_id"`
	WorkerID  string `json:"worker_id"`
	Prompt    string `json:"prompt"`
}

// AppserverSpawnResponse is returned after thread/start and turn/start have
// been persisted, while the daemon continues to own the first turn.
type AppserverSpawnResponse struct {
	RequestID    string `json:"request_id"`
	WorkerID     string `json:"worker_id"`
	HostID       string `json:"host_id"`
	ThreadID     string `json:"thread_id"`
	TurnID       string `json:"turn_id"`
	Worktree     string `json:"worktree"`
	Status       string `json:"status"`
	RuntimeOwner string `json:"runtime_owner"`
	Replayed     bool   `json:"replayed"`
}
