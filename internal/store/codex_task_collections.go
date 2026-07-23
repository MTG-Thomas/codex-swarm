package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// CodexTaskCollectionSource identifies host snapshots collected from the
	// Codex task-list API. Callers cannot substitute a different source.
	CodexTaskCollectionSource = "codex.list_threads"

	maxCodexTaskCollectionCursorLength = 4096
	maxOpenCodexTaskCollections        = 1000
	maxCodexTaskCollectionPages        = 1000
	maxCodexTaskCollectionReplays      = 1000
	staleCodexTaskCollectionAge        = 7 * 24 * time.Hour
)

var (
	ErrCodexTaskCollectionNotFound       = errors.New("codex task collection not found")
	ErrCodexTaskCollectionReplayMismatch = errors.New("codex task collection replay mismatch")
	ErrCodexTaskCollectionFinalized      = errors.New("codex task collection already finalized")
)

// CodexTaskHostObservation is the metadata-only task shape accepted from a
// Codex host. It deliberately excludes prompts, responses, transcripts, and
// coordinator-authored classifications.
type CodexTaskHostObservation struct {
	ThreadID    string    `json:"thread_id"`
	Title       string    `json:"title,omitempty"`
	CWD         string    `json:"cwd,omitempty"`
	Project     string    `json:"project,omitempty"`
	Status      string    `json:"status,omitempty"`
	Unread      bool      `json:"unread"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	WaitCursor  *string   `json:"wait_cursor,omitempty"`
	Tombstoned  bool      `json:"tombstoned,omitempty"`
	Coordinator *bool     `json:"coordinator,omitempty"`
}

// CodexTaskCollectionPageRequest stages one page in a host observation. Page
// numbers are one-based and cursors must form a contiguous chain.
type CodexTaskCollectionPageRequest struct {
	HostID        string                     `json:"host_id"`
	ObservationID string                     `json:"observation_id"`
	Page          int                        `json:"page"`
	Cursor        string                     `json:"cursor,omitempty"`
	NextCursor    string                     `json:"next_cursor,omitempty"`
	ObservedAt    time.Time                  `json:"observed_at"`
	Tasks         []CodexTaskHostObservation `json:"tasks"`
}

type CodexTaskCollectionPageResult struct {
	HostID        string    `json:"host_id"`
	ObservationID string    `json:"observation_id"`
	Page          int       `json:"page"`
	Tasks         int       `json:"tasks"`
	ObservedAt    time.Time `json:"observed_at"`
	Replayed      bool      `json:"replayed"`
}

type CodexTaskCollectionFinishRequest struct {
	HostID        string `json:"host_id"`
	ObservationID string `json:"observation_id"`
	Coverage      string `json:"coverage"`
}

type CodexTaskCollectionFinishResult struct {
	ObservationID string                `json:"observation_id"`
	Pages         int                   `json:"pages"`
	Ingest        CodexTaskIngestResult `json:"ingest"`
	Replayed      bool                  `json:"replayed"`
}

// CodexTaskCollectionStatus is compact restart-safe readback for one host
// observation. It exposes no staged task payload.
type CodexTaskCollectionStatus struct {
	HostID        string     `json:"host_id"`
	ObservationID string     `json:"observation_id"`
	Source        string     `json:"source"`
	ObservedAt    time.Time  `json:"observed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	Pages         int        `json:"pages"`
	Tasks         int        `json:"tasks"`
	NextPage      int        `json:"next_page,omitempty"`
	NextCursor    string     `json:"next_cursor,omitempty"`
	Coverage      string     `json:"coverage,omitempty"`
	FinalizedAt   *time.Time `json:"finalized_at,omitempty"`
}

type codexTaskCollectionPageFingerprint struct {
	HostID        string                     `json:"host_id"`
	ObservationID string                     `json:"observation_id"`
	Page          int                        `json:"page"`
	Cursor        string                     `json:"cursor,omitempty"`
	NextCursor    string                     `json:"next_cursor,omitempty"`
	Tasks         []CodexTaskHostObservation `json:"tasks"`
}

// AddCodexTaskCollectionPage durably stages one page. Page one creates the
// collection and fixes the timestamp used by final ingestion. ObservedAt on
// later pages is accepted as collection-time caller metadata but cannot change
// that ordering timestamp and is excluded from replay identity.
func (s *JSONStore) AddCodexTaskCollectionPage(request CodexTaskCollectionPageRequest) (CodexTaskCollectionPageResult, error) {
	request = normalizeCodexTaskCollectionPage(request)
	if err := validateCodexTaskCollectionPage(request); err != nil {
		return CodexTaskCollectionPageResult{}, err
	}
	fingerprint, payload, err := codexTaskCollectionPagePayload(request)
	if err != nil {
		return CodexTaskCollectionPageResult{}, err
	}

	var result CodexTaskCollectionPageResult
	err = s.withStateLock(func() error {
		var observedValue string
		var pageCount, taskCount int
		var finalized sql.NullString
		err := s.tx.QueryRow(`SELECT observed_at,page_count,task_count,finalized_at FROM codex_task_collections WHERE host_id=? AND observation_id=?`, request.HostID, request.ObservationID).Scan(&observedValue, &pageCount, &taskCount, &finalized)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if request.Page != 1 {
				return fmt.Errorf("%w: host=%s observation=%s", ErrCodexTaskCollectionNotFound, request.HostID, request.ObservationID)
			}
			receivedAt := time.Now().UTC()
			if err := pruneStaleCodexTaskCollections(s.tx, receivedAt); err != nil {
				return err
			}
			var open int
			if err := s.tx.QueryRow(`SELECT COUNT(*) FROM codex_task_collections WHERE finalized_at IS NULL`).Scan(&open); err != nil {
				return fmt.Errorf("count open codex task collections: %w", err)
			}
			if open >= maxOpenCodexTaskCollections {
				return fmt.Errorf("open codex task collections reached the %d collection limit", maxOpenCodexTaskCollections)
			}
			observedValue = formatDBTime(request.ObservedAt)
			if _, err := s.tx.Exec(`INSERT INTO codex_task_collections(host_id,observation_id,observed_at,page_count,task_count,created_at) VALUES(?,?,?,?,?,?)`, request.HostID, request.ObservationID, observedValue, 0, 0, formatDBTime(receivedAt)); err != nil {
				return fmt.Errorf("create codex task collection: %w", err)
			}
		case err != nil:
			return fmt.Errorf("read codex task collection: %w", err)
		}

		observedAt, err := parseDBTime(observedValue)
		if err != nil {
			return fmt.Errorf("parse codex task collection time: %w", err)
		}
		result = CodexTaskCollectionPageResult{
			HostID: request.HostID, ObservationID: request.ObservationID,
			Page: request.Page, Tasks: len(request.Tasks), ObservedAt: observedAt,
		}

		var storedFingerprint string
		err = s.tx.QueryRow(`SELECT fingerprint FROM codex_task_collection_pages WHERE host_id=? AND observation_id=? AND page_number=?`, request.HostID, request.ObservationID, request.Page).Scan(&storedFingerprint)
		switch {
		case err == nil:
			if storedFingerprint != fingerprint {
				return fmt.Errorf("%w: host=%s observation=%s page=%d", ErrCodexTaskCollectionReplayMismatch, request.HostID, request.ObservationID, request.Page)
			}
			result.Replayed = true
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read codex task collection page replay: %w", err)
		}
		if finalized.Valid {
			return fmt.Errorf("%w: host=%s observation=%s", ErrCodexTaskCollectionFinalized, request.HostID, request.ObservationID)
		}

		if request.Page != pageCount+1 {
			return fmt.Errorf("codex task collection page %d is not contiguous; next page is %d", request.Page, pageCount+1)
		}
		if request.Page == 1 {
			if request.Cursor != "" {
				return errors.New("codex task collection page 1 cursor must be empty")
			}
		} else {
			var previousNext string
			if err := s.tx.QueryRow(`SELECT next_cursor FROM codex_task_collection_pages WHERE host_id=? AND observation_id=? AND page_number=?`, request.HostID, request.ObservationID, request.Page-1).Scan(&previousNext); err != nil {
				return fmt.Errorf("read previous codex task collection page: %w", err)
			}
			if previousNext == "" {
				return errors.New("codex task collection cannot continue after a terminal page")
			}
			if request.Cursor != previousNext {
				return fmt.Errorf("codex task collection page %d cursor does not match page %d next_cursor", request.Page, request.Page-1)
			}
		}
		if request.NextCursor != "" {
			var reused int
			if err := s.tx.QueryRow(`SELECT COUNT(*) FROM codex_task_collection_pages WHERE host_id=? AND observation_id=? AND (cursor=? OR next_cursor=?)`, request.HostID, request.ObservationID, request.NextCursor, request.NextCursor).Scan(&reused); err != nil {
				return fmt.Errorf("check codex task collection cursor cycle: %w", err)
			}
			if reused != 0 || request.NextCursor == request.Cursor {
				return errors.New("codex task collection next_cursor repeats an earlier cursor")
			}
		}
		if taskCount+len(request.Tasks) > maxCodexTasksPerSnapshot {
			return fmt.Errorf("codex task collection exceeds %d tasks", maxCodexTasksPerSnapshot)
		}
		if err := ensureNoCodexTaskCollectionDuplicates(s.tx, request); err != nil {
			return err
		}

		if _, err := s.tx.Exec(`INSERT INTO codex_task_collection_pages(host_id,observation_id,page_number,cursor,next_cursor,fingerprint,tasks,task_count,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, request.HostID, request.ObservationID, request.Page, request.Cursor, request.NextCursor, fingerprint, payload, len(request.Tasks), formatDBTime(time.Now().UTC())); err != nil {
			return fmt.Errorf("stage codex task collection page: %w", err)
		}
		_, err = s.tx.Exec(`UPDATE codex_task_collections SET page_count=?,task_count=? WHERE host_id=? AND observation_id=?`, pageCount+1, taskCount+len(request.Tasks), request.HostID, request.ObservationID)
		return err
	})
	if err != nil {
		return CodexTaskCollectionPageResult{}, err
	}
	return result, nil
}

// GetCodexTaskCollectionStatus returns enough durable state for an interrupted
// host collector to resume the next page without exposing staged task data.
func (s *JSONStore) GetCodexTaskCollectionStatus(hostID, observationID string) (CodexTaskCollectionStatus, error) {
	hostID = strings.TrimSpace(hostID)
	observationID = strings.TrimSpace(observationID)
	if hostID == "" || observationID == "" {
		return CodexTaskCollectionStatus{}, errors.New("host_id and observation_id are required")
	}
	if len(hostID) > 256 || len(observationID) > 256 {
		return CodexTaskCollectionStatus{}, errors.New("host_id and observation_id must not exceed 256 characters")
	}
	var status CodexTaskCollectionStatus
	err := s.withStateLock(func() error {
		var observedValue, createdValue string
		var coverage sql.NullString
		var finalized sql.NullString
		err := s.tx.QueryRow(`SELECT observed_at,created_at,page_count,task_count,coverage,finalized_at FROM codex_task_collections WHERE host_id=? AND observation_id=?`, hostID, observationID).Scan(&observedValue, &createdValue, &status.Pages, &status.Tasks, &coverage, &finalized)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: host=%s observation=%s", ErrCodexTaskCollectionNotFound, hostID, observationID)
		}
		if err != nil {
			return err
		}
		status.HostID = hostID
		status.ObservationID = observationID
		status.Source = CodexTaskCollectionSource
		status.ObservedAt, err = parseDBTime(observedValue)
		if err != nil {
			return err
		}
		status.CreatedAt, err = parseDBTime(createdValue)
		if err != nil {
			return err
		}
		if coverage.Valid {
			status.Coverage = coverage.String
		}
		if finalized.Valid {
			at, err := parseDBTime(finalized.String)
			if err != nil {
				return err
			}
			status.FinalizedAt = &at
		} else {
			status.NextPage = status.Pages + 1
		}
		if status.Pages > 0 {
			if err := s.tx.QueryRow(`SELECT next_cursor FROM codex_task_collection_pages WHERE host_id=? AND observation_id=? AND page_number=?`, hostID, observationID, status.Pages).Scan(&status.NextCursor); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return CodexTaskCollectionStatus{}, err
	}
	return status, nil
}

// FinishCodexTaskCollection validates and atomically publishes all staged
// pages. Finalization empties page payloads while retaining compact page and
// finish fingerprints for bounded idempotent replay.
func (s *JSONStore) FinishCodexTaskCollection(request CodexTaskCollectionFinishRequest) (CodexTaskCollectionFinishResult, error) {
	request.HostID = strings.TrimSpace(request.HostID)
	request.ObservationID = strings.TrimSpace(request.ObservationID)
	request.Coverage = strings.ToLower(strings.TrimSpace(request.Coverage))
	if request.HostID == "" || request.ObservationID == "" {
		return CodexTaskCollectionFinishResult{}, errors.New("host_id and observation_id are required")
	}
	if len(request.HostID) > 256 || len(request.ObservationID) > 256 {
		return CodexTaskCollectionFinishResult{}, errors.New("host_id and observation_id must not exceed 256 characters")
	}
	if request.Coverage != CodexTaskCoverageWindow && request.Coverage != CodexTaskCoverageComplete {
		return CodexTaskCollectionFinishResult{}, fmt.Errorf("coverage must be %q or %q", CodexTaskCoverageWindow, CodexTaskCoverageComplete)
	}
	finishFingerprint, err := fingerprintJSON(request)
	if err != nil {
		return CodexTaskCollectionFinishResult{}, err
	}

	var result CodexTaskCollectionFinishResult
	err = s.withStateLock(func() error {
		var observedValue string
		var pageCount, taskCount int
		var storedFinishFingerprint sql.NullString
		var storedResult []byte
		err := s.tx.QueryRow(`SELECT observed_at,page_count,task_count,finish_fingerprint,finish_result FROM codex_task_collections WHERE host_id=? AND observation_id=?`, request.HostID, request.ObservationID).Scan(&observedValue, &pageCount, &taskCount, &storedFinishFingerprint, &storedResult)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: host=%s observation=%s", ErrCodexTaskCollectionNotFound, request.HostID, request.ObservationID)
		}
		if err != nil {
			return fmt.Errorf("read codex task collection finalization: %w", err)
		}
		if storedFinishFingerprint.Valid {
			if storedFinishFingerprint.String != finishFingerprint {
				return fmt.Errorf("%w: host=%s observation=%s finish", ErrCodexTaskCollectionReplayMismatch, request.HostID, request.ObservationID)
			}
			if err := json.Unmarshal(storedResult, &result); err != nil {
				return fmt.Errorf("decode codex task collection replay: %w", err)
			}
			result.Replayed = true
			return nil
		}
		if pageCount < 1 {
			return errors.New("codex task collection has no pages")
		}

		observedAt, err := parseDBTime(observedValue)
		if err != nil {
			return fmt.Errorf("parse codex task collection time: %w", err)
		}
		pages, tasks, terminalCursor, err := readCodexTaskCollectionPages(s.tx, request.HostID, request.ObservationID, pageCount, taskCount)
		if err != nil {
			return err
		}
		if request.Coverage == CodexTaskCoverageComplete && terminalCursor != "" {
			return errors.New("complete codex task collection requires a terminal empty next_cursor")
		}
		ingest, err := s.ingestCodexTasksLocked(CodexTaskIngestRequest{
			RequestID: codexTaskCollectionIngestID(request.HostID, request.ObservationID),
			HostID:    request.HostID, Source: CodexTaskCollectionSource,
			ObservedAt: observedAt, Coverage: request.Coverage, Tasks: tasks,
		})
		if err != nil {
			return fmt.Errorf("finalize codex task collection ingest: %w", err)
		}
		result = CodexTaskCollectionFinishResult{ObservationID: request.ObservationID, Pages: pages, Ingest: ingest}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		finalizedAt := formatDBTime(time.Now().UTC())
		if _, err := s.tx.Exec(`UPDATE codex_task_collections SET coverage=?,finish_fingerprint=?,finish_result=?,finalized_at=? WHERE host_id=? AND observation_id=?`, request.Coverage, finishFingerprint, encoded, finalizedAt, request.HostID, request.ObservationID); err != nil {
			return fmt.Errorf("record codex task collection finalization: %w", err)
		}
		if _, err := s.tx.Exec(`UPDATE codex_task_collection_pages SET tasks=? WHERE host_id=? AND observation_id=?`, []byte(`[]`), request.HostID, request.ObservationID); err != nil {
			return fmt.Errorf("compact codex task collection pages: %w", err)
		}
		return pruneCodexTaskCollectionReplays(s.tx, maxCodexTaskCollectionReplays)
	})
	if err != nil {
		return CodexTaskCollectionFinishResult{}, err
	}
	return result, nil
}

func normalizeCodexTaskCollectionPage(request CodexTaskCollectionPageRequest) CodexTaskCollectionPageRequest {
	request.HostID = strings.TrimSpace(request.HostID)
	request.ObservationID = strings.TrimSpace(request.ObservationID)
	request.ObservedAt = request.ObservedAt.UTC()
	request.Tasks = append([]CodexTaskHostObservation(nil), request.Tasks...)
	for i := range request.Tasks {
		task := &request.Tasks[i]
		task.ThreadID = strings.TrimSpace(task.ThreadID)
		task.Title = strings.TrimSpace(task.Title)
		task.CWD = strings.TrimSpace(task.CWD)
		task.Project = strings.TrimSpace(task.Project)
		task.Status = strings.TrimSpace(task.Status)
		task.CreatedAt = task.CreatedAt.UTC()
		task.UpdatedAt = task.UpdatedAt.UTC()
		if task.WaitCursor != nil {
			cursor := *task.WaitCursor
			task.WaitCursor = &cursor
		}
	}
	sort.Slice(request.Tasks, func(i, j int) bool { return request.Tasks[i].ThreadID < request.Tasks[j].ThreadID })
	return request
}

func validateCodexTaskCollectionPage(request CodexTaskCollectionPageRequest) error {
	if request.HostID == "" || request.ObservationID == "" {
		return errors.New("host_id and observation_id are required")
	}
	if len(request.HostID) > 256 || len(request.ObservationID) > 256 {
		return errors.New("host_id and observation_id must not exceed 256 characters")
	}
	if request.Page < 1 {
		return errors.New("codex task collection page must be at least 1")
	}
	if request.Page > maxCodexTaskCollectionPages {
		return fmt.Errorf("codex task collection page must not exceed %d", maxCodexTaskCollectionPages)
	}
	if request.ObservedAt.IsZero() {
		return errors.New("observed_at is required")
	}
	if len(request.Cursor) > maxCodexTaskCollectionCursorLength || len(request.NextCursor) > maxCodexTaskCollectionCursorLength {
		return fmt.Errorf("codex task collection cursors must not exceed %d characters", maxCodexTaskCollectionCursorLength)
	}
	return validateCodexTaskRequest(CodexTaskIngestRequest{
		RequestID: request.ObservationID, HostID: request.HostID, Source: CodexTaskCollectionSource,
		ObservedAt: request.ObservedAt, Coverage: CodexTaskCoverageWindow,
		Tasks: codexTaskHostObservations(request.Tasks),
	})
}

func codexTaskHostObservations(tasks []CodexTaskHostObservation) []CodexTaskObservation {
	result := make([]CodexTaskObservation, len(tasks))
	for i, task := range tasks {
		result[i] = CodexTaskObservation{
			ThreadID: task.ThreadID, Title: task.Title,
			CWD: task.CWD, Project: task.Project, Status: task.Status, Unread: task.Unread,
			CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, WaitCursor: task.WaitCursor,
			Tombstoned: task.Tombstoned, Coordinator: task.Coordinator,
		}
	}
	return result
}

func codexTaskCollectionPagePayload(request CodexTaskCollectionPageRequest) (string, []byte, error) {
	identity := codexTaskCollectionPageFingerprint{
		HostID: request.HostID, ObservationID: request.ObservationID, Page: request.Page,
		Cursor: request.Cursor, NextCursor: request.NextCursor, Tasks: request.Tasks,
	}
	fingerprint, err := fingerprintJSON(identity)
	if err != nil {
		return "", nil, fmt.Errorf("fingerprint codex task collection page: %w", err)
	}
	payload, err := json.Marshal(request.Tasks)
	if err != nil {
		return "", nil, fmt.Errorf("encode codex task collection page: %w", err)
	}
	return fingerprint, payload, nil
}

func fingerprintJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func ensureNoCodexTaskCollectionDuplicates(q *sql.Tx, request CodexTaskCollectionPageRequest) error {
	seen := make(map[string]struct{}, len(request.Tasks))
	for _, task := range request.Tasks {
		seen[task.ThreadID] = struct{}{}
	}
	rows, err := q.Query(`SELECT tasks FROM codex_task_collection_pages WHERE host_id=? AND observation_id=?`, request.HostID, request.ObservationID)
	if err != nil {
		return fmt.Errorf("read staged codex task collection identities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var tasks []CodexTaskHostObservation
		if err := json.Unmarshal(payload, &tasks); err != nil {
			return fmt.Errorf("decode staged codex task collection identities: %w", err)
		}
		for _, task := range tasks {
			if _, duplicate := seen[task.ThreadID]; duplicate {
				return fmt.Errorf("duplicate thread_id %q across codex task collection pages", task.ThreadID)
			}
		}
	}
	return rows.Err()
}

func readCodexTaskCollectionPages(q *sql.Tx, hostID, observationID string, expectedPages, expectedTasks int) (int, []CodexTaskObservation, string, error) {
	rows, err := q.Query(`SELECT page_number,cursor,next_cursor,tasks FROM codex_task_collection_pages WHERE host_id=? AND observation_id=? ORDER BY page_number`, hostID, observationID)
	if err != nil {
		return 0, nil, "", err
	}
	defer rows.Close()
	var tasks []CodexTaskObservation
	pages := 0
	expectedCursor := ""
	for rows.Next() {
		var page int
		var cursor, nextCursor string
		var payload []byte
		if err := rows.Scan(&page, &cursor, &nextCursor, &payload); err != nil {
			return 0, nil, "", err
		}
		pages++
		if page != pages || cursor != expectedCursor {
			return 0, nil, "", errors.New("codex task collection cursor chain is not contiguous")
		}
		var hostTasks []CodexTaskHostObservation
		if err := json.Unmarshal(payload, &hostTasks); err != nil {
			return 0, nil, "", fmt.Errorf("decode codex task collection page %d: %w", page, err)
		}
		tasks = append(tasks, codexTaskHostObservations(hostTasks)...)
		expectedCursor = nextCursor
	}
	if err := rows.Err(); err != nil {
		return 0, nil, "", err
	}
	if pages != expectedPages || len(tasks) != expectedTasks {
		return 0, nil, "", errors.New("codex task collection staging counts do not match its manifest")
	}
	return pages, tasks, expectedCursor, nil
}

func codexTaskCollectionIngestID(hostID, observationID string) string {
	sum := sha256.Sum256([]byte(hostID + "\x00" + observationID))
	return "codex-collection-" + hex.EncodeToString(sum[:])
}

func pruneCodexTaskCollectionReplays(q *sql.Tx, retain int) error {
	_, err := q.Exec(`DELETE FROM codex_task_collections WHERE finalized_at IS NOT NULL AND rowid IN (SELECT rowid FROM codex_task_collections WHERE finalized_at IS NOT NULL ORDER BY finalized_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, retain)
	return err
}

func pruneStaleCodexTaskCollections(q *sql.Tx, now time.Time) error {
	cutoff := formatDBTime(now.Add(-staleCodexTaskCollectionAge))
	if _, err := q.Exec(`DELETE FROM codex_task_collections WHERE finalized_at IS NULL AND created_at < ?`, cutoff); err != nil {
		return fmt.Errorf("prune stale open codex task collections: %w", err)
	}
	return nil
}
