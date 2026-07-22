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
	"strconv"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/coordination"
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
type LegacyWorker = protocol.LegacyWorker
type ClaimConflict = protocol.ClaimConflict

type readStore interface {
	ListWorkers() ([]store.Worker, error)
	ListClaims() ([]store.Claim, error)
	SaveWorkers(...store.Worker) error
}

type eventStore interface {
	ListEvents() ([]store.Event, error)
}

type metricsStore interface {
	CoordinationMetrics(time.Time) (store.CoordinationMetrics, error)
}

type coordinationStore interface {
	GetWorker(string) (store.Worker, error)
	ListWorkers() ([]store.Worker, error)
	CreateMessage(store.Message, []string) (store.Message, []store.Delivery, bool, error)
	UpdateDelivery(string, store.DeliveryState, string, time.Time) error
	RecordFileTouch(store.FileTouch) ([]store.TouchConflict, error)
	ListMessages(string) ([]store.DeliveredMessage, error)
}

type codexTaskStore interface {
	IngestCodexTasks(store.CodexTaskIngestRequest) (store.CodexTaskIngestResult, error)
	ListCodexTasks(store.CodexTaskListFilter) (store.CodexTaskPage, error)
	CodexTaskStats(*time.Time) (store.CodexTaskStats, error)
}

// Server exposes read-only daemon HTTP endpoints over swarm state.
type Server struct {
	store         readStore
	statePath     string
	issueProvider readiness.IssueMetadataProvider
	steerer       coordination.TurnSteerer
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

// SetTurnSteerer installs an optional daemon-owned runtime. By default active
// workers poll the durable queue over their existing app-server connection.
func (s *Server) SetTurnSteerer(steerer coordination.TurnSteerer) {
	s.steerer = steerer
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
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/touches", s.handleTouches)
	mux.HandleFunc("/v1/completions", s.handleCompletions)
	mux.HandleFunc("/v1/codex-tasks", s.handleCodexTasks)
	mux.HandleFunc("/v1/codex-tasks/ingest", s.handleCodexTaskIngest)
	mux.HandleFunc("/v1/codex-tasks/status", s.handleCodexTaskStatus)
	mux.HandleFunc("/v1/dispatch", s.handleDispatch)
	mux.HandleFunc("/v1/status", s.handleLegacyStatus)
	return mux
}

func (s *Server) codexTasks() (codexTaskStore, error) {
	st, ok := s.store.(codexTaskStore)
	if !ok {
		return nil, errors.New("store does not support the Codex task index")
	}
	return st, nil
}

func (s *Server) handleCodexTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	st, err := s.codexTasks()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "task_index_unavailable", err.Error())
		return
	}
	filter, err := codexTaskFilterFromQuery(r.URL.Query())
	if err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	page, err := st.ListCodexTasks(filter)
	if err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "task_list_failed", err.Error())
		return
	}
	writeJSON(w, protocol.CodexTaskListResponse(page))
}

func (s *Server) handleCodexTaskIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "Codex task ingestion requires loopback daemon access")
		return
	}
	st, err := s.codexTasks()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "task_index_unavailable", err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var request protocol.CodexTaskIngestRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "parse Codex task snapshot: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "Codex task snapshot must contain one JSON document")
		return
	}
	result, err := st.IngestCodexTasks(request)
	if err != nil {
		status, code := http.StatusBadRequest, "task_ingest_failed"
		if errors.Is(err, store.ErrCodexTaskReplayMismatch) {
			status, code = http.StatusConflict, "request_replay_mismatch"
		}
		writeRouteError(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, protocol.CodexTaskIngestResponse(result))
}

func (s *Server) handleCodexTaskStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	st, err := s.codexTasks()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "task_index_unavailable", err.Error())
		return
	}
	var staleBefore *time.Time
	if value := strings.TrimSpace(r.URL.Query().Get("stale_before")); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			writeRouteError(w, r, http.StatusBadRequest, "invalid_filter", "stale_before must be RFC3339")
			return
		}
		parsed = parsed.UTC()
		staleBefore = &parsed
	}
	stats, err := st.CodexTaskStats(staleBefore)
	if err != nil {
		writeRouteError(w, r, http.StatusInternalServerError, "task_status_failed", err.Error())
		return
	}
	writeJSON(w, protocol.CodexTaskStatusResponse(stats))
}

func codexTaskFilterFromQuery(query url.Values) (store.CodexTaskListFilter, error) {
	filter := store.CodexTaskListFilter{
		HostID: strings.TrimSpace(query.Get("host")), Project: strings.TrimSpace(query.Get("project")),
		Status: strings.TrimSpace(query.Get("status")), Source: strings.TrimSpace(query.Get("source")),
		Tier: strings.ToUpper(strings.TrimSpace(query.Get("tier"))), Cursor: strings.TrimSpace(query.Get("cursor")),
	}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return filter, errors.New("limit must be an integer")
		}
		filter.Limit = limit
	}
	if value := strings.TrimSpace(query.Get("unread")); value != "" {
		unread, err := strconv.ParseBool(value)
		if err != nil {
			return filter, errors.New("unread must be true or false")
		}
		filter.Unread = &unread
	}
	if value := strings.TrimSpace(query.Get("include_tombstoned")); value != "" {
		include, err := strconv.ParseBool(value)
		if err != nil {
			return filter, errors.New("include_tombstoned must be true or false")
		}
		filter.IncludeTombstoned = include
	}
	if value := strings.TrimSpace(query.Get("stale_before")); value != "" {
		at, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return filter, errors.New("stale_before must be RFC3339")
		}
		at = at.UTC()
		filter.StaleBefore = &at
	}
	return filter, nil
}

func (s *Server) coordinationService() (coordination.Service, coordinationStore, error) {
	st, ok := s.store.(coordinationStore)
	if !ok {
		return coordination.Service{}, nil, errors.New("store does not support durable coordination")
	}
	return coordination.Service{Store: st, Steerer: s.steerer}, st, nil
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	service, st, err := s.coordinationService()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "coordination_unavailable", err.Error())
		return
	}
	if r.Method == http.MethodGet {
		workerID := strings.TrimSpace(r.URL.Query().Get("worker"))
		if workerID == "" {
			writeRouteError(w, r, http.StatusBadRequest, "worker_required", "worker query parameter is required")
			return
		}
		messages, err := st.ListMessages(workerID)
		if err != nil {
			writeRouteError(w, r, http.StatusInternalServerError, "store_read_failed", err.Error())
			return
		}
		writeJSON(w, protocol.InboxResponse{Messages: messages})
		return
	}
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "message delivery requires loopback daemon access")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req protocol.MessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "parse message request: "+err.Error())
		return
	}
	result, err := service.Send(r.Context(), coordination.SendRequest{
		RequestID: req.RequestID, Kind: req.Kind, From: req.From, To: req.To, Body: req.Body,
	})
	if err != nil {
		writeRouteError(w, r, coordinationStatus(err), coordinationCode(err), err.Error())
		return
	}
	writeJSON(w, s.messageResponse(result))
}

func (s *Server) handleTouches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "file touch delivery requires loopback daemon access")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	service, _, err := s.coordinationService()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "coordination_unavailable", err.Error())
		return
	}
	var req protocol.TouchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "parse touch request: "+err.Error())
		return
	}
	result, err := service.Touch(r.Context(), coordination.TouchRequest{
		RequestID: req.RequestID, WorkerID: req.WorkerID, Repo: req.Repo, Path: req.Path, Operation: req.Operation,
		LineStart: req.LineStart, LineEnd: req.LineEnd, Intent: req.Intent,
	})
	if err != nil {
		writeRouteError(w, r, coordinationStatus(err), coordinationCode(err), err.Error())
		return
	}
	warnings := make([]protocol.MessageResponse, 0, len(result.Warnings))
	for _, warning := range result.Warnings {
		warnings = append(warnings, s.messageResponse(warning))
	}
	writeJSON(w, protocol.TouchResponse{Touch: result.Touch, Conflicts: result.Conflicts, Warnings: warnings})
}

func (s *Server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "completion forwarding requires loopback daemon access")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	service, _, err := s.coordinationService()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "coordination_unavailable", err.Error())
		return
	}
	var req protocol.CompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "parse completion request: "+err.Error())
		return
	}
	result, forwarded, err := service.ForwardCompletion(r.Context(), req.RequestID, req.WorkerID, req.Report)
	if err != nil {
		writeRouteError(w, r, coordinationStatus(err), coordinationCode(err), err.Error())
		return
	}
	response := protocol.CompletionResponse{Forwarded: forwarded}
	if forwarded {
		message := s.messageResponse(result)
		response.Message = &message
	}
	writeJSON(w, response)
}

func (s *Server) messageResponse(result coordination.SendResult) protocol.MessageResponse {
	native := append([]store.NativeSteeringRequest(nil), result.NativeSteering...)
	for i := range native {
		native[i].StatePath = s.statePath
	}
	return protocol.MessageResponse{Message: result.Message, Deliveries: result.Deliveries, NativeSteering: native, Replayed: result.Replayed}
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
	status := Status{
		Daemon:        "running",
		Version:       Version,
		StatePath:     s.statePath,
		WorkerCount:   len(workers),
		ClaimCount:    len(claimList),
		ConflictCount: len(findClaimConflicts(claimList, time.Now().UTC())),
	}
	if metricsSource, ok := s.store.(metricsStore); ok {
		metrics, err := metricsSource.CoordinationMetrics(time.Now().UTC())
		if err != nil {
			writeRouteError(w, r, http.StatusInternalServerError, "store_metrics_failed", err.Error())
			return
		}
		status.Backend = metrics.Backend
		status.ActiveWorkerCount = metrics.ActiveWorkers
		status.LiveMessageWorkers = metrics.LiveMessageWorkers
		status.ResumeWorkers = metrics.ResumeWorkers
		status.ManagedWorktreeWorkers = metrics.ManagedWorktreeWorkers
		status.AutomaticCompletionWorkers = metrics.AutomaticCompletionWorkers
		status.ExternalTrackerWorkers = metrics.ExternalTrackerWorkers
		status.SteerableWorkers = metrics.SteerableWorkers
		status.ActiveClaimCount = metrics.ActiveClaims
		status.MessageCount = metrics.MessageCount
		status.QueuedMessages = metrics.QueuedMessages
		status.SteeredMessages = metrics.SteeredMessages
		status.DeliveredMessages = metrics.DeliveredMessages
		status.RecentTouches = metrics.RecentTouches
		status.ConflictMessages = metrics.ConflictMessages
	}
	if taskSource, ok := s.store.(codexTaskStore); ok {
		stats, err := taskSource.CodexTaskStats(nil)
		if err != nil {
			writeRouteError(w, r, http.StatusInternalServerError, "task_status_failed", err.Error())
			return
		}
		status.CodexTaskCount = stats.Total
	}
	writeJSON(w, status)
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
		Claims:    protocolClaims(claimList),
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
		Workers:   protocolLegacyWorkers(workers),
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

func coordinationStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrWorkerNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrMessageReplayMismatch):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func coordinationCode(err error) string {
	switch {
	case errors.Is(err, store.ErrWorkerNotFound):
		return "worker_not_found"
	case errors.Is(err, store.ErrMessageReplayMismatch):
		return "request_replay_mismatch"
	default:
		return "coordination_failed"
	}
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
		return WorkersResponse{Workers: summarizeLegacyWorkers(legacy.Workers)}, nil
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

// Message routes a DM or subtree broadcast through the daemon courier.
func (c Client) Message(ctx context.Context, request protocol.MessageRequest) (protocol.MessageResponse, error) {
	var response protocol.MessageResponse
	if err := c.post(ctx, "/v1/messages", request, &response); err != nil {
		return protocol.MessageResponse{}, err
	}
	return response, nil
}

// Inbox returns all durable deliveries for one worker.
func (c Client) Inbox(ctx context.Context, workerID string) (protocol.InboxResponse, error) {
	var response protocol.InboxResponse
	query := url.Values{}
	query.Set("worker", workerID)
	if err := c.get(ctx, "/v1/messages?"+query.Encode(), &response); err != nil {
		return protocol.InboxResponse{}, err
	}
	return response, nil
}

// Touch records a file touch and returns warning-only conflicts.
func (c Client) Touch(ctx context.Context, request protocol.TouchRequest) (protocol.TouchResponse, error) {
	var response protocol.TouchResponse
	if err := c.post(ctx, "/v1/touches", request, &response); err != nil {
		return protocol.TouchResponse{}, err
	}
	return response, nil
}

// Completion forwards a child terminal report to its parent.
func (c Client) Completion(ctx context.Context, request protocol.CompletionRequest) (protocol.CompletionResponse, error) {
	var response protocol.CompletionResponse
	if err := c.post(ctx, "/v1/completions", request, &response); err != nil {
		return protocol.CompletionResponse{}, err
	}
	return response, nil
}

// CodexTasks reads one stable page from the durable Codex task discovery index.
func (c Client) CodexTasks(ctx context.Context, filter store.CodexTaskListFilter) (protocol.CodexTaskListResponse, error) {
	var response protocol.CodexTaskListResponse
	query := url.Values{}
	if filter.HostID != "" {
		query.Set("host", filter.HostID)
	}
	if filter.Project != "" {
		query.Set("project", filter.Project)
	}
	if filter.Status != "" {
		query.Set("status", filter.Status)
	}
	if filter.Source != "" {
		query.Set("source", filter.Source)
	}
	if filter.Tier != "" {
		query.Set("tier", filter.Tier)
	}
	if filter.Cursor != "" {
		query.Set("cursor", filter.Cursor)
	}
	if filter.Limit != 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Unread != nil {
		query.Set("unread", strconv.FormatBool(*filter.Unread))
	}
	if filter.IncludeTombstoned {
		query.Set("include_tombstoned", "true")
	}
	if filter.StaleBefore != nil {
		query.Set("stale_before", filter.StaleBefore.UTC().Format(time.RFC3339Nano))
	}
	path := "/v1/codex-tasks"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := c.get(ctx, path, &response); err != nil {
		return protocol.CodexTaskListResponse{}, err
	}
	return response, nil
}

// IngestCodexTasks submits one explicit metadata-only host snapshot.
func (c Client) IngestCodexTasks(ctx context.Context, request protocol.CodexTaskIngestRequest) (protocol.CodexTaskIngestResponse, error) {
	var response protocol.CodexTaskIngestResponse
	if err := c.post(ctx, "/v1/codex-tasks/ingest", request, &response); err != nil {
		return protocol.CodexTaskIngestResponse{}, err
	}
	return response, nil
}

// CodexTaskStatus summarizes the durable discovery index.
func (c Client) CodexTaskStatus(ctx context.Context, staleBefore *time.Time) (protocol.CodexTaskStatusResponse, error) {
	var response protocol.CodexTaskStatusResponse
	path := "/v1/codex-tasks/status"
	if staleBefore != nil {
		query := url.Values{"stale_before": []string{staleBefore.UTC().Format(time.RFC3339Nano)}}
		path += "?" + query.Encode()
	}
	if err := c.get(ctx, path, &response); err != nil {
		return protocol.CodexTaskStatusResponse{}, err
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
			Worktree:         truthfulWorkerWorktree(worker),
			Repo:             worker.ProjectRoot,
			Engine:           worker.Engine,
			Capabilities:     store.CapabilitiesForWorker(worker).Strings(),
			HostID:           worker.HostID,
			ThreadID:         worker.ThreadID,
			TurnID:           worker.TurnID,
			RuntimeOwner:     string(worker.RuntimeOwner),
			Prompt:           worker.Prompt,
			UpdatedAt:        worker.UpdatedAt,
		})
	}
	return summaries
}

func summarizeLegacyWorkers(workers []protocol.LegacyWorker) []WorkerStatus {
	summaries := make([]WorkerStatus, 0, len(workers))
	for _, worker := range workers {
		summaries = append(summaries, WorkerStatus{
			ID:               worker.ID,
			Status:           worker.Status,
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

func truthfulWorkerWorktree(worker store.Worker) string {
	if store.CapabilitiesForWorker(worker).Has(store.CapabilityManagedWorktree) {
		return worker.Worktree
	}
	return ""
}

func protocolClaims(claims []store.Claim) []protocol.Claim {
	result := make([]protocol.Claim, 0, len(claims))
	for _, claim := range claims {
		result = append(result, protocol.Claim{
			ID: claim.ID, WorkerID: claim.WorkerID, Repo: claim.Repo, ScopeKind: string(claim.ScopeKind), Scope: claim.Scope,
			Issue: claim.Issue, Status: string(claim.Status), Note: claim.Note,
			ExternalWorker: claim.ExternalWorker, WorkerSource: claim.WorkerSource,
			Blocker: claim.Blocker, Next: claim.Next, ExpiresAt: claim.ExpiresAt,
			CreatedAt: claim.CreatedAt, UpdatedAt: claim.UpdatedAt,
		})
	}
	return result
}

func protocolLegacyWorkers(workers []store.Worker) []protocol.LegacyWorker {
	result := make([]protocol.LegacyWorker, 0, len(workers))
	for _, worker := range workers {
		legacy := protocol.LegacyWorker{
			ID: worker.ID, ParentID: worker.ParentID, Role: worker.Role, Issue: worker.Issue,
			ValidationOf: worker.ValidationOf, ValidationStatus: worker.ValidationStatus,
			ProjectRoot: worker.ProjectRoot, Worktree: worker.Worktree, Branch: worker.Branch,
			ThreadID: worker.ThreadID, TurnID: worker.TurnID, Engine: worker.Engine,
			Status: string(worker.Status), Prompt: worker.Prompt, LastMessage: worker.LastMessage,
			Report: worker.Report, CreatedAt: worker.CreatedAt, UpdatedAt: worker.UpdatedAt,
		}
		if worker.Lifecycle != nil {
			legacy.Lifecycle = &protocol.LegacyLifecycle{
				Version: worker.Lifecycle.Version,
				Session: protocol.LegacySessionLifecycle{
					State: string(worker.Lifecycle.Session.State), Reason: string(worker.Lifecycle.Session.Reason),
					CompletedAt: worker.Lifecycle.Session.CompletedAt, TerminatedAt: worker.Lifecycle.Session.TerminatedAt,
				},
				Runtime: protocol.LegacyRuntimeLifecycle{
					State: string(worker.Lifecycle.Runtime.State), Reason: string(worker.Lifecycle.Runtime.Reason),
				},
			}
		}
		for _, pr := range worker.PullRequests {
			legacy.PullRequests = append(legacy.PullRequests, protocol.LegacyPullRequest{
				URL: pr.URL, State: pr.State, BaseBranch: pr.BaseBranch, HeadBranch: pr.HeadBranch,
				ReviewDecision: pr.ReviewDecision, CheckSummary: pr.CheckSummary,
				CodeRabbitStatus: pr.CodeRabbitStatus, NextAction: pr.NextAction, UpdatedAt: pr.UpdatedAt,
			})
		}
		for _, event := range worker.Events {
			legacy.Events = append(legacy.Events, protocol.LegacyEvent{
				At: event.At, Type: event.Type, Message: event.Message, From: event.From,
				To: event.To, Issue: event.Issue, WorkerID: event.WorkerID, RequestID: event.RequestID,
			})
		}
		result = append(result, legacy)
	}
	return result
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
