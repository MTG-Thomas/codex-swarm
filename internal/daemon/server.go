package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const Version = "0.1.0"

// Status is the compact operator-facing state returned by the daemon.
type Status struct {
	Daemon        string `json:"daemon"`
	Version       string `json:"version"`
	StatePath     string `json:"state_path"`
	WorkerCount   int    `json:"worker_count"`
	ClaimCount    int    `json:"claim_count"`
	ConflictCount int    `json:"conflict_count"`
}

func (s Status) String() string {
	return fmt.Sprintf("daemon=%s version=%s workers=%d claims=%d conflicts=%d state=%s", s.Daemon, s.Version, s.WorkerCount, s.ClaimCount, s.ConflictCount, s.StatePath)
}

type WorkerStatus struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Issue    string `json:"issue,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
}

type WorkersResponse struct {
	Workers []WorkerStatus `json:"workers"`
}

type ClaimsResponse struct {
	Claims    []store.Claim   `json:"claims"`
	Conflicts []ClaimConflict `json:"conflicts"`
}

type LegacyStatus struct {
	Daemon    string         `json:"daemon"`
	StatePath string         `json:"state_path"`
	Workers   []store.Worker `json:"workers"`
}

type ClaimConflict struct {
	ClaimID    string `json:"claim_id"`
	ConflictID string `json:"conflict_id"`
	Repo       string `json:"repo"`
	Scope      string `json:"scope"`
}

type readStore interface {
	ListWorkers() ([]store.Worker, error)
	ListClaims() ([]store.Claim, error)
}

type Server struct {
	store     readStore
	statePath string
}

func NewServer(statePath string, st readStore) *Server {
	return &Server{
		store:     st,
		statePath: statePath,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/workers", s.handleWorkers)
	mux.HandleFunc("/claims", s.handleClaims)
	mux.HandleFunc("/v1/status", s.handleLegacyStatus)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workers, err := s.store.ListWorkers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	claimList, err := s.store.ListClaims()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, Status{
		Daemon:        "running",
		Version:       Version,
		StatePath:     s.statePath,
		WorkerCount:   len(workers),
		ClaimCount:    len(claimList),
		ConflictCount: len(findClaimConflicts(claimList, time.Now().UTC())),
	})
}

func (s *Server) handleWorkers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workers, err := s.store.ListWorkers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, WorkersResponse{Workers: summarizeWorkers(workers)})
}

func (s *Server) handleClaims(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	claimList, err := s.store.ListClaims()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, ClaimsResponse{
		Claims:    claimList,
		Conflicts: findClaimConflicts(claimList, time.Now().UTC()),
	})
}

func (s *Server) handleLegacyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	workers, err := s.store.ListWorkers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, LegacyStatus{
		Daemon:    "running",
		StatePath: s.statePath,
		Workers:   workers,
	})
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

func (c Client) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := c.get(ctx, "/status", &status); err != nil {
		var legacy LegacyStatus
		if legacyErr := c.get(ctx, "/v1/status", &legacy); legacyErr != nil {
			return Status{}, err
		}
		return Status{
			Daemon:      legacy.Daemon,
			Version:     "legacy",
			StatePath:   legacy.StatePath,
			WorkerCount: len(legacy.Workers),
		}, nil
	}
	return status, nil
}

func (c Client) Workers(ctx context.Context) (WorkersResponse, error) {
	var workers WorkersResponse
	if err := c.get(ctx, "/workers", &workers); err != nil {
		var legacy LegacyStatus
		if legacyErr := c.get(ctx, "/v1/status", &legacy); legacyErr != nil {
			return WorkersResponse{}, err
		}
		return WorkersResponse{Workers: summarizeWorkers(legacy.Workers)}, nil
	}
	return workers, nil
}

func (c Client) Claims(ctx context.Context) (ClaimsResponse, error) {
	var claimList ClaimsResponse
	if err := c.get(ctx, "/claims", &claimList); err != nil {
		return ClaimsResponse{}, err
	}
	return claimList, nil
}

func (c Client) get(ctx context.Context, path string, target any) error {
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		return fmt.Errorf("daemon base URL is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("daemon returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func summarizeWorkers(workers []store.Worker) []WorkerStatus {
	summaries := make([]WorkerStatus, 0, len(workers))
	for _, worker := range workers {
		summaries = append(summaries, WorkerStatus{
			ID:       worker.ID,
			Status:   displayWorkerStatus(worker),
			Issue:    worker.Issue,
			Worktree: worker.Worktree,
			ThreadID: worker.ThreadID,
		})
	}
	return summaries
}

func displayWorkerStatus(worker store.Worker) string {
	if worker.Lifecycle != nil {
		return string(worker.Lifecycle.DeriveStatus())
	}
	return string(worker.Status)
}

func findClaimConflicts(claimList []store.Claim, now time.Time) []ClaimConflict {
	conflicts := []ClaimConflict(nil)
	seen := map[string]struct{}{}
	for _, claim := range claimList {
		if !claims.IsOpen(claim, now) {
			continue
		}
		for _, conflict := range claims.FindConflicts(claimList, claim, now) {
			left, right := claim.ID, conflict.ID
			if right < left {
				left, right = right, left
			}
			key := left + "\x00" + right
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			conflicts = append(conflicts, ClaimConflict{
				ClaimID:    claim.ID,
				ConflictID: conflict.ID,
				Repo:       claim.Repo,
				Scope:      claim.Scope,
			})
		}
	}
	return conflicts
}
