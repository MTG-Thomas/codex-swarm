package protocol

import (
	"fmt"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

// Status is the compact operator-facing state returned by the daemon.
type Status struct {
	Daemon        string `json:"daemon"`
	Version       string `json:"version"`
	StatePath     string `json:"state_path"`
	WorkerCount   int    `json:"worker_count"`
	ClaimCount    int    `json:"claim_count"`
	ConflictCount int    `json:"conflict_count"`
}

// String renders a compact human-readable daemon status line.
func (s Status) String() string {
	return fmt.Sprintf("daemon=%s version=%s workers=%d claims=%d conflicts=%d state=%s", s.Daemon, s.Version, s.WorkerCount, s.ClaimCount, s.ConflictCount, s.StatePath)
}

// WorkerStatus is the compact daemon representation of one worker.
type WorkerStatus struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	Role             string `json:"role,omitempty"`
	Issue            string `json:"issue,omitempty"`
	ValidationOf     string `json:"validation_of,omitempty"`
	ValidationStatus string `json:"validation_status,omitempty"`
	Worktree         string `json:"worktree,omitempty"`
	ThreadID         string `json:"thread_id,omitempty"`
}

// WorkersResponse is returned by the daemon workers endpoint.
type WorkersResponse struct {
	Workers []WorkerStatus `json:"workers"`
}

// ClaimsResponse is returned by the daemon claims endpoint.
type ClaimsResponse struct {
	Claims    []store.Claim   `json:"claims"`
	Conflicts []ClaimConflict `json:"conflicts"`
}

// ClaimConflict describes one pair of overlapping open claims.
type ClaimConflict struct {
	ClaimID    string `json:"claim_id"`
	ConflictID string `json:"conflict_id"`
	Repo       string `json:"repo"`
	Scope      string `json:"scope"`
}

// LegacyStatus is the compatibility response for older daemon clients.
type LegacyStatus struct {
	Daemon    string         `json:"daemon"`
	StatePath string         `json:"state_path"`
	Workers   []store.Worker `json:"workers"`
}
