package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

// Status is the compact operator-facing state returned by the daemon.
type Status struct {
	Daemon    string         `json:"daemon"`
	StatePath string         `json:"state_path"`
	Workers   []store.Worker `json:"workers"`
}

func (s Status) String() string {
	return fmt.Sprintf("daemon=%s workers=%d state=%s", s.Daemon, len(s.Workers), s.StatePath)
}

type Server struct {
	store     store.Store
	statePath string
}

func NewServer(statePath string, st store.Store) *Server {
	return &Server{
		store:     st,
		statePath: statePath,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/status", s.handleStatus)
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
	writeJSON(w, Status{
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
	if err := c.get(ctx, "/v1/status", &status); err != nil {
		return Status{}, err
	}
	return status, nil
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
