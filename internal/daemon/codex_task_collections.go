package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (s *Server) handleCodexTaskCollectionPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "Codex task collection requires loopback daemon access")
		return
	}
	st, err := s.codexTasks()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "task_index_unavailable", err.Error())
		return
	}
	var request protocol.CodexTaskCollectionPageRequest
	if !decodeCodexTaskCollectionRequest(w, r, &request) {
		return
	}
	result, err := st.AddCodexTaskCollectionPage(request)
	if err != nil {
		status, code := codexTaskCollectionError(err)
		writeRouteError(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, protocol.CodexTaskCollectionPageResponse(result))
}

func (s *Server) handleCodexTaskCollectionFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "Codex task collection requires loopback daemon access")
		return
	}
	st, err := s.codexTasks()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "task_index_unavailable", err.Error())
		return
	}
	var request protocol.CodexTaskCollectionFinishRequest
	if !decodeCodexTaskCollectionRequest(w, r, &request) {
		return
	}
	result, err := st.FinishCodexTaskCollection(request)
	if err != nil {
		status, code := codexTaskCollectionError(err)
		writeRouteError(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, protocol.CodexTaskCollectionFinishResponse(result))
}

func (s *Server) handleCodexTaskCollectionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, r)
		return
	}
	if !isLoopbackRemote(r.RemoteAddr) {
		writeRouteError(w, r, http.StatusForbidden, "loopback_required", "Codex task collection requires loopback daemon access")
		return
	}
	st, err := s.codexTasks()
	if err != nil {
		writeRouteError(w, r, http.StatusNotImplemented, "task_index_unavailable", err.Error())
		return
	}
	status, err := st.GetCodexTaskCollectionStatus(r.URL.Query().Get("host"), r.URL.Query().Get("observation"))
	if err != nil {
		code, label := codexTaskCollectionError(err)
		writeRouteError(w, r, code, label, err.Error())
		return
	}
	writeJSON(w, protocol.CodexTaskCollectionStatusResponse(status))
}

func decodeCodexTaskCollectionRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "parse Codex task collection request: "+err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeRouteError(w, r, http.StatusBadRequest, "invalid_json", "Codex task collection request must contain one JSON document")
		return false
	}
	return true
}

func codexTaskCollectionError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrCodexTaskCollectionReplayMismatch):
		return http.StatusConflict, "collection_replay_mismatch"
	case errors.Is(err, store.ErrCodexTaskReplayMismatch):
		return http.StatusConflict, "request_replay_mismatch"
	case errors.Is(err, store.ErrCodexTaskCollectionFinalized):
		return http.StatusConflict, "collection_finalized"
	case errors.Is(err, store.ErrCodexTaskCollectionNotFound):
		return http.StatusNotFound, "collection_not_found"
	default:
		return http.StatusBadRequest, "collection_invalid"
	}
}

// AddCodexTaskCollectionPage stages one metadata-only host discovery page.
func (c Client) AddCodexTaskCollectionPage(ctx context.Context, request protocol.CodexTaskCollectionPageRequest) (protocol.CodexTaskCollectionPageResponse, error) {
	var response protocol.CodexTaskCollectionPageResponse
	if err := c.post(ctx, "/v1/codex-tasks/collections/pages", request, &response); err != nil {
		return protocol.CodexTaskCollectionPageResponse{}, fmt.Errorf("add Codex task collection page: %w", err)
	}
	return response, nil
}

// FinishCodexTaskCollection validates and atomically ingests all staged pages.
func (c Client) FinishCodexTaskCollection(ctx context.Context, request protocol.CodexTaskCollectionFinishRequest) (protocol.CodexTaskCollectionFinishResponse, error) {
	var response protocol.CodexTaskCollectionFinishResponse
	if err := c.post(ctx, "/v1/codex-tasks/collections/finish", request, &response); err != nil {
		return protocol.CodexTaskCollectionFinishResponse{}, fmt.Errorf("finish Codex task collection: %w", err)
	}
	return response, nil
}

// CodexTaskCollectionStatus reads compact restart-safe collector progress.
func (c Client) CodexTaskCollectionStatus(ctx context.Context, hostID, observationID string) (protocol.CodexTaskCollectionStatusResponse, error) {
	var response protocol.CodexTaskCollectionStatusResponse
	query := url.Values{"host": []string{hostID}, "observation": []string{observationID}}
	if err := c.get(ctx, "/v1/codex-tasks/collections/status?"+query.Encode(), &response); err != nil {
		return protocol.CodexTaskCollectionStatusResponse{}, fmt.Errorf("read Codex task collection status: %w", err)
	}
	return response, nil
}
