package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/dispatch"
	gh "github.com/MTG-Thomas/codex-swarm/internal/github"
	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
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

type DispatchRequest struct {
	RequestID string   `json:"request_id"`
	Issue     string   `json:"issue"`
	Repo      string   `json:"repo"`
	Prompt    string   `json:"prompt"`
	Gates     []string `json:"gates,omitempty"`
}

type DispatchResponse struct {
	RequestID   string `json:"request_id"`
	Implementer string `json:"implementer"`
	Validator   string `json:"validator"`
	Replayed    bool   `json:"replayed"`
}

// LegacyStatus is the compatibility response for older daemon clients.
type LegacyStatus struct {
	Daemon    string         `json:"daemon"`
	StatePath string         `json:"state_path"`
	Workers   []store.Worker `json:"workers"`
}

// ClaimConflict describes one pair of overlapping open claims.
type ClaimConflict struct {
	ClaimID    string `json:"claim_id"`
	ConflictID string `json:"conflict_id"`
	Repo       string `json:"repo"`
	Scope      string `json:"scope"`
}

type readStore interface {
	ListWorkers() ([]store.Worker, error)
	ListClaims() ([]store.Claim, error)
	SaveWorkers(...store.Worker) error
}

// Server exposes read-only daemon HTTP endpoints over swarm state.
type Server struct {
	store         readStore
	statePath     string
	issueProvider readiness.IssueMetadataProvider
}

// NewServer builds a read-only daemon server over the provided store.
func NewServer(statePath string, st readStore) *Server {
	return NewServerWithIssueProvider(statePath, st, gh.CLIssueMetadataProvider{})
}

func NewServerWithIssueProvider(statePath string, st readStore, provider readiness.IssueMetadataProvider) *Server {
	return &Server{
		store:         st,
		statePath:     statePath,
		issueProvider: provider,
	}
}

// Handler returns the daemon HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/workers", s.handleWorkers)
	mux.HandleFunc("/claims", s.handleClaims)
	mux.HandleFunc("/readiness", s.handleReadiness)
	mux.HandleFunc("/dispatch", s.handleDispatch)
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

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	report, err := readiness.Build(r.Context(), readiness.BuildInput{
		Issue:    r.URL.Query().Get("issue"),
		Repo:     r.URL.Query().Get("repo"),
		Store:    s.store,
		Provider: s.issueProvider,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, report)
}

func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, "dispatch requires loopback daemon access", http.StatusForbidden)
		return
	}
	var req DispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "parse dispatch request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RequestID) == "" {
		http.Error(w, "request_id is required", http.StatusBadRequest)
		return
	}
	report, err := readiness.Build(r.Context(), readiness.BuildInput{
		Issue:    req.Issue,
		Repo:     req.Repo,
		Store:    s.store,
		Provider: s.issueProvider,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	plan, err := dispatch.Plan(dispatch.Input{Readiness: report, Prompt: req.Prompt, Gates: req.Gates})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	result, err := dispatch.Execute(s.store, plan, req.RequestID, "mock", time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, DispatchResponse{
		RequestID:   result.RequestID,
		Implementer: result.Implementer,
		Validator:   result.Validator,
		Replayed:    result.Replayed,
	})
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

// Client reads status from a running codex-swarm daemon.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// Status returns compact daemon state, falling back only for legacy daemons.
func (c Client) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := c.get(ctx, "/status", &status); err != nil {
		if !isLegacyFallbackStatus(err) {
			return Status{}, err
		}
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

// Workers returns compact worker summaries from the daemon.
func (c Client) Workers(ctx context.Context) (WorkersResponse, error) {
	var workers WorkersResponse
	if err := c.get(ctx, "/workers", &workers); err != nil {
		if !isLegacyFallbackStatus(err) {
			return WorkersResponse{}, err
		}
		var legacy LegacyStatus
		if legacyErr := c.get(ctx, "/v1/status", &legacy); legacyErr != nil {
			return WorkersResponse{}, err
		}
		return WorkersResponse{Workers: summarizeWorkers(legacy.Workers)}, nil
	}
	return workers, nil
}

type statusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e statusError) Error() string {
	message := "daemon returned " + e.Status
	if body := strings.TrimSpace(e.Body); body != "" {
		message += ": " + body
	}
	return message
}

func isLegacyFallbackStatus(err error) bool {
	var status statusError
	return errors.As(err, &status) && (status.StatusCode == http.StatusNotFound || status.StatusCode == http.StatusNotImplemented)
}

// Claims returns claims and conflict summaries from the daemon.
func (c Client) Claims(ctx context.Context) (ClaimsResponse, error) {
	var claimList ClaimsResponse
	if err := c.get(ctx, "/claims", &claimList); err != nil {
		return ClaimsResponse{}, err
	}
	return claimList, nil
}

// Readiness returns a read-only issue readiness report from the daemon.
func (c Client) Readiness(ctx context.Context, issue, repo string) (readiness.Report, error) {
	var report readiness.Report
	query := url.Values{}
	query.Set("issue", issue)
	query.Set("repo", repo)
	if err := c.get(ctx, "/readiness?"+query.Encode(), &report); err != nil {
		return readiness.Report{}, err
	}
	return report, nil
}

func (c Client) Dispatch(ctx context.Context, request DispatchRequest) (DispatchResponse, error) {
	var response DispatchResponse
	if err := c.post(ctx, "/dispatch", request, &response); err != nil {
		return DispatchResponse{}, err
	}
	return response, nil
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
		return statusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: responseBody(resp)}
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (c Client) post(ctx context.Context, path string, body, target any) error {
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		return fmt.Errorf("daemon base URL is required")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
		return statusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: responseBody(resp)}
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func responseBody(resp *http.Response) string {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	return string(data)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func summarizeWorkers(workers []store.Worker) []WorkerStatus {
	summaries := make([]WorkerStatus, 0, len(workers))
	for _, worker := range workers {
		summaries = append(summaries, WorkerStatus{
			ID:               worker.ID,
			Status:           displayWorkerStatus(worker),
			Role:             worker.Role,
			Issue:            worker.Issue,
			ValidationOf:     worker.ValidationOf,
			ValidationStatus: worker.ValidationStatus,
			Worktree:         worker.Worktree,
			ThreadID:         worker.ThreadID,
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
