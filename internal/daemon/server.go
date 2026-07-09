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
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
	"github.com/MTG-Thomas/codex-swarm/internal/version"
)

var Version = version.Version

type Status = protocol.Status
type WorkerStatus = protocol.WorkerStatus
type WorkersResponse = protocol.WorkersResponse
type ClaimsResponse = protocol.ClaimsResponse
type DispatchRequest = protocol.DispatchRequest
type DispatchResponse = protocol.DispatchResponse
type LegacyStatus = protocol.LegacyStatus
type ClaimConflict = protocol.ClaimConflict

type readStore interface {
	ListWorkers() ([]store.Worker, error)
	ListClaims() ([]store.Claim, error)
	SaveWorkers(...store.Worker) error
}

type eventStore interface {
	ListEvents() ([]store.Event, error)
}

// Server exposes read-only daemon HTTP endpoints over swarm state.
type Server struct {
	store         readStore
	statePath     string
	issueProvider readiness.IssueMetadataProvider
}

// NewServer builds a read-only daemon server over the provided store.
func NewServer(statePath string, st readStore) *Server {
	provider, err := gh.NewIssueMetadataProviderFromEnv()
	if err != nil {
		provider = gh.ErrorIssueMetadataProvider{Err: err}
	}
	return NewServerWithIssueProvider(statePath, st, provider)
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
	mux.HandleFunc("/v1/events", s.handleEvents)
	mux.HandleFunc("/v1/dispatch", s.handleDispatch)
	mux.HandleFunc("/v1/status", s.handleLegacyStatus)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	workers, err := s.store.ListWorkers()
	if err != nil {
		writeRouteError(w, r, http.StatusInternalServerError, "store_read_failed", err.Error())
		return
	}
	claimList, err := s.store.ListClaims()
	if err != nil {
		writeRouteError(w, r, http.StatusInternalServerError, "store_read_failed", err.Error())
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
		writeMethodNotAllowed(w, r)
		return
	}
	workers, err := s.store.ListWorkers()
	if err != nil {
		writeRouteError(w, r, http.StatusInternalServerError, "store_read_failed", err.Error())
		return
	}
	writeJSON(w, WorkersResponse{Workers: summarizeWorkers(workers)})
}

func (s *Server) handleClaims(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	claimList, err := s.store.ListClaims()
	if err != nil {
		writeRouteError(w, r, http.StatusInternalServerError, "store_read_failed", err.Error())
		return
	}
	writeJSON(w, ClaimsResponse{
		Claims:    claimList,
		Conflicts: findClaimConflicts(claimList, time.Now().UTC()),
	})
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	report, err := readiness.Build(r.Context(), readiness.BuildInput{
		Issue:    r.URL.Query().Get("issue"),
		Repo:     r.URL.Query().Get("repo"),
		Store:    s.store,
		Provider: s.issueProvider,
	})
	if err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "readiness_failed", err.Error())
		return
	}
	writeJSON(w, report)
}

func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "dispatch requires loopback daemon access")
		return
	}
	var req DispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "parse dispatch request: "+err.Error())
		return
	}
	if strings.TrimSpace(req.RequestID) == "" {
		writeRouteError(w, r, http.StatusBadRequest, "request_id_required", "request_id is required")
		return
	}
	report, err := readiness.Build(r.Context(), readiness.BuildInput{
		Issue:         req.Issue,
		Repo:          req.Repo,
		Store:         s.store,
		Provider:      s.issueProvider,
		ExplicitGates: req.Gates,
	})
	if err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "readiness_failed", err.Error())
		return
	}
	plan, err := dispatch.Plan(dispatch.Input{Readiness: report, Prompt: req.Prompt, Gates: req.Gates})
	if err != nil {
		writeRouteError(w, r, http.StatusConflict, "dispatch_plan_failed", err.Error())
		return
	}
	result, err := dispatch.Execute(s.store, plan, req.RequestID, "mock", time.Now().UTC())
	if err != nil {
		writeRouteError(w, r, statusForDispatchError(err), codeForDispatchError(err), err.Error())
		return
	}
	writeJSON(w, DispatchResponse{
		RequestID:   result.RequestID,
		Implementer: result.Implementer,
		Validator:   result.Validator,
		Replayed:    result.Replayed,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	workerID := strings.TrimSpace(r.URL.Query().Get("worker"))
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	events, err := s.eventEnvelopes(workerID)
	if err != nil {
		writeRouteError(w, r, http.StatusInternalServerError, "store_read_failed", err.Error())
		return
	}
	if format == "ndjson" {
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		for _, event := range events {
			_ = enc.Encode(event)
		}
		return
	}
	writeJSON(w, protocol.EventsResponse{Events: events})
}

func (s *Server) eventEnvelopes(workerID string) ([]protocol.EventEnvelope, error) {
	workers, err := s.store.ListWorkers()
	if err != nil {
		return nil, err
	}
	workerIssues := map[string]string{}
	for _, worker := range workers {
		workerIssues[worker.ID] = worker.Issue
	}
	var events []store.Event
	if st, ok := s.store.(eventStore); ok {
		events, err = st.ListEvents()
		if err != nil {
			return nil, err
		}
	}
	if len(events) == 0 {
		for _, worker := range workers {
			if workerID != "" && worker.ID != workerID {
				continue
			}
			for _, event := range worker.Events {
				if event.WorkerID == "" {
					event.WorkerID = worker.ID
				}
				if event.Issue == "" {
					event.Issue = worker.Issue
				}
				events = append(events, event)
			}
		}
	}
	envelopes := make([]protocol.EventEnvelope, 0, len(events))
	for _, event := range events {
		id := event.WorkerID
		if id == "" {
			id = event.From
		}
		if workerID != "" && id != workerID && event.From != workerID && event.To != workerID {
			continue
		}
		issue := event.Issue
		if issue == "" {
			issue = workerIssues[id]
		}
		envelopes = append(envelopes, protocol.EventEnvelope{
			Schema:    "codex-swarm:event:v1",
			Kind:      "worker.event",
			At:        event.At,
			WorkerID:  id,
			Type:      event.Type,
			Message:   event.Message,
			From:      event.From,
			To:        event.To,
			Issue:     issue,
			RequestID: event.RequestID,
		})
	}
	return envelopes, nil
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
		writeMethodNotAllowed(w, r)
		return
	}
	workers, err := s.store.ListWorkers()
	if err != nil {
		writeRouteError(w, r, http.StatusInternalServerError, "store_read_failed", err.Error())
		return
	}
	writeJSON(w, LegacyStatus{
		Daemon:    "running",
		StatePath: s.statePath,
		Workers:   workers,
	})
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeRouteError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeRouteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		protocol.WriteError(w, status, code, message)
		return
	}
	http.Error(w, message, status)
}

func statusForDispatchError(err error) int {
	if isReplayMismatch(err) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func codeForDispatchError(err error) string {
	if isReplayMismatch(err) {
		return "request_replay_mismatch"
	}
	return "dispatch_execute_failed"
}

func isReplayMismatch(err error) bool {
	return strings.Contains(err.Error(), "does not match original mutation fingerprint")
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
