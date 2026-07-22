package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	CodexTaskCoverageWindow   = "window"
	CodexTaskCoverageComplete = "complete"
	maxCodexTasksPerSnapshot  = 1000
	maxCodexTaskPageSize      = 500
	maxCodexTaskIngestReplays = 5000
	codexTaskDBTimeLayout     = "2006-01-02T15:04:05.000000000Z"
)

// CodexTask is a durable discovery record for one Codex task. Codex remains
// authoritative for live lifecycle state; this record only retains the latest
// host-supplied observation and discovery metadata.
type CodexTask struct {
	HostID                       string     `json:"host_id"`
	ThreadID                     string     `json:"thread_id"`
	Title                        string     `json:"title,omitempty"`
	Description                  string     `json:"description,omitempty"`
	CWD                          string     `json:"cwd,omitempty"`
	Project                      string     `json:"project,omitempty"`
	Status                       string     `json:"status,omitempty"`
	Unread                       bool       `json:"unread"`
	Coordinator                  bool       `json:"coordinator,omitempty"`
	Tier                         string     `json:"tier,omitempty"`
	LastMeaningfulOutcome        string     `json:"last_meaningful_outcome,omitempty"`
	UnresolvedLoop               string     `json:"unresolved_loop,omitempty"`
	SmallestNextAction           string     `json:"smallest_next_action,omitempty"`
	OperatorDecision             string     `json:"operator_decision,omitempty"`
	LastClassifiedAt             *time.Time `json:"last_classified_at,omitempty"`
	LastClassificationSnapshotID string     `json:"last_classification_snapshot_id,omitempty"`
	CreatedAt                    time.Time  `json:"created_at"`
	UpdatedAt                    time.Time  `json:"updated_at"`
	FirstSeenAt                  time.Time  `json:"first_seen_at"`
	LastSeenAt                   time.Time  `json:"last_seen_at"`
	StateObservedAt              time.Time  `json:"state_observed_at"`
	StateSnapshotID              string     `json:"state_snapshot_id"`
	DiscoverySource              string     `json:"discovery_source"`
	WaitCursor                   string     `json:"wait_cursor,omitempty"`
	LastSnapshotID               string     `json:"last_snapshot_id"`
	MissingSince                 *time.Time `json:"missing_since,omitempty"`
	TombstonedAt                 *time.Time `json:"tombstoned_at,omitempty"`
}

// CodexTaskObservation is the intentionally metadata-only task shape accepted
// from a Codex host. It excludes prompts, final messages, and transcript bodies.
type CodexTaskObservation struct {
	ThreadID       string                   `json:"thread_id"`
	Title          string                   `json:"title,omitempty"`
	Description    string                   `json:"description,omitempty"`
	CWD            string                   `json:"cwd,omitempty"`
	Project        string                   `json:"project,omitempty"`
	Status         string                   `json:"status,omitempty"`
	Unread         bool                     `json:"unread"`
	CreatedAt      time.Time                `json:"created_at,omitempty"`
	UpdatedAt      time.Time                `json:"updated_at,omitempty"`
	WaitCursor     *string                  `json:"wait_cursor,omitempty"`
	Tombstoned     bool                     `json:"tombstoned,omitempty"`
	Coordinator    *bool                    `json:"coordinator,omitempty"`
	Classification *CodexTaskClassification `json:"classification,omitempty"`
}

// CodexTaskClassification is a compact coordinator-authored summary. It must
// not contain prompt, response, transcript, or secret material.
type CodexTaskClassification struct {
	Tier                  string    `json:"tier,omitempty"`
	LastMeaningfulOutcome string    `json:"last_meaningful_outcome,omitempty"`
	UnresolvedLoop        string    `json:"unresolved_loop,omitempty"`
	SmallestNextAction    string    `json:"smallest_next_action,omitempty"`
	OperatorDecision      string    `json:"operator_decision,omitempty"`
	ClassifiedAt          time.Time `json:"classified_at,omitempty"`
}

// CodexTaskIngestRequest is one idempotent host snapshot ingestion.
type CodexTaskIngestRequest struct {
	RequestID  string                 `json:"request_id"`
	HostID     string                 `json:"host_id"`
	Source     string                 `json:"source"`
	ObservedAt time.Time              `json:"observed_at"`
	Coverage   string                 `json:"coverage,omitempty"`
	Tasks      []CodexTaskObservation `json:"tasks"`
}

type CodexTaskIngestResult struct {
	RequestID     string    `json:"request_id"`
	HostID        string    `json:"host_id"`
	Source        string    `json:"source"`
	ObservedAt    time.Time `json:"observed_at"`
	Coverage      string    `json:"coverage"`
	Observed      int       `json:"observed"`
	Inserted      int       `json:"inserted"`
	Updated       int       `json:"updated"`
	StaleSkipped  int       `json:"stale_skipped"`
	MissingMarked int       `json:"missing_marked"`
	Replayed      bool      `json:"replayed"`
}

type CodexTaskListFilter struct {
	HostID            string
	Project           string
	Status            string
	Source            string
	Tier              string
	Unread            *bool
	IncludeTombstoned bool
	StaleBefore       *time.Time
	Limit             int
	Cursor            string
}

type CodexTaskPage struct {
	Tasks      []CodexTask `json:"tasks"`
	Total      int         `json:"total"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

type CodexTaskStats struct {
	Total      int            `json:"total"`
	Unread     int            `json:"unread"`
	Missing    int            `json:"missing"`
	Tombstoned int            `json:"tombstoned"`
	Stale      int            `json:"stale"`
	ByStatus   map[string]int `json:"by_status"`
	ByHost     map[string]int `json:"by_host"`
	ByTier     map[string]int `json:"by_tier"`
	OldestSeen *time.Time     `json:"oldest_seen,omitempty"`
	NewestSeen *time.Time     `json:"newest_seen,omitempty"`
}

type codexTaskCursor struct {
	LastSeenAt time.Time `json:"last_seen_at"`
	HostID     string    `json:"host_id"`
	ThreadID   string    `json:"thread_id"`
}

func (s *JSONStore) IngestCodexTasks(request CodexTaskIngestRequest) (CodexTaskIngestResult, error) {
	var result CodexTaskIngestResult
	err := s.withStateLock(func() error {
		var err error
		result, err = s.ingestCodexTasksLocked(request)
		return err
	})
	if err != nil {
		return CodexTaskIngestResult{}, err
	}
	return result, nil
}

// ingestCodexTasksLocked applies one snapshot using the caller's active SQLite
// transaction. Keeping this boundary private lets multi-page collection
// finalization publish its task snapshot atomically with its replay record.
func (s *JSONStore) ingestCodexTasksLocked(request CodexTaskIngestRequest) (CodexTaskIngestResult, error) {
	if s.tx == nil {
		return CodexTaskIngestResult{}, errors.New("ingest codex tasks outside SQLite transaction")
	}
	request = normalizeCodexTaskRequest(request)
	if err := validateCodexTaskRequest(request); err != nil {
		return CodexTaskIngestResult{}, err
	}
	fingerprint, err := codexTaskFingerprint(request)
	if err != nil {
		return CodexTaskIngestResult{}, err
	}
	result := CodexTaskIngestResult{
		RequestID: request.RequestID, HostID: request.HostID, Source: request.Source,
		ObservedAt: request.ObservedAt, Coverage: request.Coverage, Observed: len(request.Tasks),
	}
	var storedFingerprint string
	var storedResult []byte
	err = s.tx.QueryRow(`SELECT fingerprint,result FROM codex_task_ingests WHERE request_id=?`, request.RequestID).Scan(&storedFingerprint, &storedResult)
	switch {
	case err == nil:
		if storedFingerprint != fingerprint {
			return CodexTaskIngestResult{}, fmt.Errorf("%w: %s", ErrCodexTaskReplayMismatch, request.RequestID)
		}
		if err := json.Unmarshal(storedResult, &result); err != nil {
			return CodexTaskIngestResult{}, fmt.Errorf("decode codex task ingest replay %q: %w", request.RequestID, err)
		}
		result.Replayed = true
		return result, nil
	case !errors.Is(err, sql.ErrNoRows):
		return CodexTaskIngestResult{}, fmt.Errorf("read codex task ingest replay %q: %w", request.RequestID, err)
	}

	seen := make(map[string]struct{}, len(request.Tasks))
	for _, observation := range request.Tasks {
		seen[observation.ThreadID] = struct{}{}
		existing, found, err := getCodexTask(s.tx, request.HostID, observation.ThreadID)
		if err != nil {
			return CodexTaskIngestResult{}, err
		}
		if found && !snapshotWins(request.ObservedAt, request.RequestID, existing.StateObservedAt, existing.StateSnapshotID) {
			result.StaleSkipped++
			if mergeCodexTaskClassification(&existing, request, observation.Classification) {
				if err := upsertCodexTask(s.tx, existing); err != nil {
					return CodexTaskIngestResult{}, err
				}
				result.Updated++
			}
			continue
		}
		task := mergeCodexTask(existing, found, request, observation)
		if err := upsertCodexTask(s.tx, task); err != nil {
			return CodexTaskIngestResult{}, err
		}
		if found {
			result.Updated++
		} else {
			result.Inserted++
		}
	}
	if request.Coverage == CodexTaskCoverageComplete {
		rows, err := s.tx.Query(`SELECT thread_id,state_observed_at,state_snapshot_id,missing_since FROM codex_tasks WHERE host_id=? AND discovery_source=? AND tombstoned_at IS NULL`, request.HostID, request.Source)
		if err != nil {
			return CodexTaskIngestResult{}, fmt.Errorf("list absent codex tasks: %w", err)
		}
		type absentTask struct {
			threadID       string
			alreadyMissing bool
		}
		var absent []absentTask
		for rows.Next() {
			var threadID, stateAtValue, stateSnapshotID string
			var missing sql.NullString
			if err := rows.Scan(&threadID, &stateAtValue, &stateSnapshotID, &missing); err != nil {
				_ = rows.Close()
				return CodexTaskIngestResult{}, err
			}
			stateAt, err := parseDBTime(stateAtValue)
			if err != nil {
				_ = rows.Close()
				return CodexTaskIngestResult{}, err
			}
			if _, ok := seen[threadID]; !ok && snapshotWins(request.ObservedAt, request.RequestID, stateAt, stateSnapshotID) {
				absent = append(absent, absentTask{threadID: threadID, alreadyMissing: missing.Valid})
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return CodexTaskIngestResult{}, err
		}
		for _, task := range absent {
			_, err := s.tx.Exec(`UPDATE codex_tasks SET missing_since=COALESCE(missing_since,?),state_observed_at=?,state_snapshot_id=? WHERE host_id=? AND thread_id=?`, formatDBTime(request.ObservedAt), formatDBTime(request.ObservedAt), request.RequestID, request.HostID, task.threadID)
			if err != nil {
				return CodexTaskIngestResult{}, err
			}
			if !task.alreadyMissing {
				result.MissingMarked++
			}
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return CodexTaskIngestResult{}, err
	}
	_, err = s.tx.Exec(`INSERT INTO codex_task_ingests(request_id,fingerprint,result,created_at) VALUES(?,?,?,?)`, request.RequestID, fingerprint, encoded, formatDBTime(request.ObservedAt))
	if err != nil {
		return CodexTaskIngestResult{}, err
	}
	if err := pruneCodexTaskIngestReplays(s.tx, maxCodexTaskIngestReplays); err != nil {
		return CodexTaskIngestResult{}, err
	}
	return result, nil
}

func (s *JSONStore) ListCodexTasks(filter CodexTaskListFilter) (CodexTaskPage, error) {
	filter.Tier = strings.ToUpper(strings.TrimSpace(filter.Tier))
	if filter.Tier != "" && filter.Tier != "P0" && filter.Tier != "P1" && filter.Tier != "P2" && filter.Tier != "P3" {
		return CodexTaskPage{}, errors.New("codex task tier filter must be P0, P1, P2, or P3")
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 0 || filter.Limit > maxCodexTaskPageSize {
		return CodexTaskPage{}, fmt.Errorf("codex task limit must be between 1 and %d", maxCodexTaskPageSize)
	}
	var cursor codexTaskCursor
	if strings.TrimSpace(filter.Cursor) != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.LastSeenAt.IsZero() || cursor.HostID == "" || cursor.ThreadID == "" {
			return CodexTaskPage{}, errors.New("invalid codex task cursor")
		}
	}
	page := CodexTaskPage{}
	err := s.withStateLock(func() error {
		clauses := []string{"1=1"}
		args := []any{}
		add := func(clause string, value any) { clauses = append(clauses, clause); args = append(args, value) }
		if filter.HostID != "" {
			add("host_id=?", filter.HostID)
		}
		if filter.Project != "" {
			add("project=?", filter.Project)
		}
		if filter.Status != "" {
			add("status=?", filter.Status)
		}
		if filter.Source != "" {
			add("discovery_source=?", filter.Source)
		}
		if filter.Tier != "" {
			add("tier=?", filter.Tier)
		}
		if filter.Unread != nil {
			add("unread=?", boolInt(*filter.Unread))
		}
		if !filter.IncludeTombstoned {
			clauses = append(clauses, "tombstoned_at IS NULL")
		}
		if filter.StaleBefore != nil {
			add("last_seen_at < ?", formatDBTime(*filter.StaleBefore))
		}
		baseClauses := append([]string(nil), clauses...)
		baseArgs := append([]any(nil), args...)
		if err := s.tx.QueryRow(`SELECT COUNT(*) FROM codex_tasks WHERE `+strings.Join(baseClauses, " AND "), baseArgs...).Scan(&page.Total); err != nil {
			return err
		}
		if !cursor.LastSeenAt.IsZero() {
			clauses = append(clauses, "(last_seen_at < ? OR (last_seen_at = ? AND (host_id > ? OR (host_id = ? AND thread_id > ?))))")
			at := formatDBTime(cursor.LastSeenAt)
			args = append(args, at, at, cursor.HostID, cursor.HostID, cursor.ThreadID)
		}
		args = append(args, filter.Limit+1)
		query := `SELECT host_id,thread_id,title,description,cwd,project,status,unread,coordinator,tier,last_meaningful_outcome,unresolved_loop,smallest_next_action,operator_decision,last_classified_at,last_classification_snapshot_id,created_at,updated_at,first_seen_at,last_seen_at,state_observed_at,state_snapshot_id,discovery_source,wait_cursor,last_snapshot_id,missing_since,tombstoned_at FROM codex_tasks WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY last_seen_at DESC,host_id,thread_id LIMIT ?`
		rows, err := s.tx.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			task, err := scanCodexTask(rows)
			if err != nil {
				return err
			}
			page.Tasks = append(page.Tasks, task)
		}
		return rows.Err()
	})
	if err != nil {
		return CodexTaskPage{}, err
	}
	if len(page.Tasks) > filter.Limit {
		page.Tasks = page.Tasks[:filter.Limit]
		last := page.Tasks[len(page.Tasks)-1]
		data, _ := json.Marshal(codexTaskCursor{LastSeenAt: last.LastSeenAt, HostID: last.HostID, ThreadID: last.ThreadID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func (s *JSONStore) CodexTaskStats(staleBefore *time.Time) (CodexTaskStats, error) {
	stats := CodexTaskStats{ByStatus: map[string]int{}, ByHost: map[string]int{}, ByTier: map[string]int{}}
	err := s.withStateLock(func() error {
		rows, err := s.tx.Query(`SELECT host_id,status,tier,unread,missing_since,tombstoned_at,last_seen_at FROM codex_tasks`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var host, status, tier, lastSeen string
			var unread int
			var missing, tombstone sql.NullString
			if err := rows.Scan(&host, &status, &tier, &unread, &missing, &tombstone, &lastSeen); err != nil {
				return err
			}
			at, err := parseDBTime(lastSeen)
			if err != nil {
				return err
			}
			stats.Total++
			if unread != 0 {
				stats.Unread++
			}
			if missing.Valid {
				stats.Missing++
			}
			if tombstone.Valid {
				stats.Tombstoned++
			}
			if staleBefore != nil && at.Before(*staleBefore) {
				stats.Stale++
			}
			stats.ByHost[host]++
			stats.ByStatus[status]++
			if tier != "" {
				stats.ByTier[tier]++
			}
			if stats.OldestSeen == nil || at.Before(*stats.OldestSeen) {
				copy := at
				stats.OldestSeen = &copy
			}
			if stats.NewestSeen == nil || at.After(*stats.NewestSeen) {
				copy := at
				stats.NewestSeen = &copy
			}
		}
		return rows.Err()
	})
	return stats, err
}

func normalizeCodexTaskRequest(request CodexTaskIngestRequest) CodexTaskIngestRequest {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.HostID = strings.TrimSpace(request.HostID)
	request.Source = strings.TrimSpace(request.Source)
	request.Coverage = strings.ToLower(strings.TrimSpace(request.Coverage))
	if request.Coverage == "" {
		request.Coverage = CodexTaskCoverageWindow
	}
	request.ObservedAt = request.ObservedAt.UTC()
	request.Tasks = append([]CodexTaskObservation(nil), request.Tasks...)
	for i := range request.Tasks {
		t := &request.Tasks[i]
		t.ThreadID = strings.TrimSpace(t.ThreadID)
		t.Title = strings.TrimSpace(t.Title)
		t.Description = strings.TrimSpace(t.Description)
		t.CWD = strings.TrimSpace(t.CWD)
		t.Project = strings.TrimSpace(t.Project)
		t.Status = strings.TrimSpace(t.Status)
		if t.WaitCursor != nil {
			cursor := *t.WaitCursor
			t.WaitCursor = &cursor
		}
		if t.Classification != nil {
			classification := *t.Classification
			classification.Tier = strings.ToUpper(strings.TrimSpace(classification.Tier))
			classification.LastMeaningfulOutcome = strings.TrimSpace(classification.LastMeaningfulOutcome)
			classification.UnresolvedLoop = strings.TrimSpace(classification.UnresolvedLoop)
			classification.SmallestNextAction = strings.TrimSpace(classification.SmallestNextAction)
			classification.OperatorDecision = strings.TrimSpace(classification.OperatorDecision)
			classification.ClassifiedAt = classification.ClassifiedAt.UTC()
			t.Classification = &classification
		}
		t.CreatedAt = t.CreatedAt.UTC()
		t.UpdatedAt = t.UpdatedAt.UTC()
	}
	sort.Slice(request.Tasks, func(i, j int) bool { return request.Tasks[i].ThreadID < request.Tasks[j].ThreadID })
	return request
}

func validateCodexTaskRequest(request CodexTaskIngestRequest) error {
	if request.RequestID == "" {
		return errors.New("request_id is required")
	}
	if request.HostID == "" {
		return errors.New("host_id is required")
	}
	if request.Source == "" {
		return errors.New("source is required")
	}
	if request.ObservedAt.IsZero() {
		return errors.New("observed_at is required")
	}
	if request.Coverage != CodexTaskCoverageWindow && request.Coverage != CodexTaskCoverageComplete {
		return fmt.Errorf("coverage must be %q or %q", CodexTaskCoverageWindow, CodexTaskCoverageComplete)
	}
	if len(request.Tasks) > maxCodexTasksPerSnapshot {
		return fmt.Errorf("snapshot exceeds %d tasks", maxCodexTasksPerSnapshot)
	}
	if len(request.RequestID) > 256 || len(request.HostID) > 256 || len(request.Source) > 256 {
		return errors.New("request_id, host_id, and source must not exceed 256 characters")
	}
	seen := map[string]struct{}{}
	for _, task := range request.Tasks {
		if task.ThreadID == "" {
			return errors.New("task thread_id is required")
		}
		cursorLength := 0
		if task.WaitCursor != nil {
			cursorLength = len(*task.WaitCursor)
		}
		if len(task.ThreadID) > 512 || len(task.Title) > 1000 || len(task.Description) > 2000 || len(task.CWD) > 4096 || len(task.Project) > 1000 || len(task.Status) > 128 || cursorLength > 4096 {
			return fmt.Errorf("codex task %q contains an oversized metadata field", task.ThreadID)
		}
		if classification := task.Classification; classification != nil {
			if classification.Tier != "" && classification.Tier != "P0" && classification.Tier != "P1" && classification.Tier != "P2" && classification.Tier != "P3" {
				return fmt.Errorf("codex task %q tier must be P0, P1, P2, or P3", task.ThreadID)
			}
			if len(classification.LastMeaningfulOutcome) > 2000 || len(classification.UnresolvedLoop) > 2000 || len(classification.SmallestNextAction) > 2000 || len(classification.OperatorDecision) > 2000 {
				return fmt.Errorf("codex task %q contains an oversized classification field", task.ThreadID)
			}
		}
		if _, ok := seen[task.ThreadID]; ok {
			return fmt.Errorf("duplicate thread_id %q in snapshot", task.ThreadID)
		}
		seen[task.ThreadID] = struct{}{}
	}
	return nil
}

func codexTaskFingerprint(request CodexTaskIngestRequest) (string, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode codex task snapshot fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func mergeCodexTask(existing CodexTask, found bool, request CodexTaskIngestRequest, observation CodexTaskObservation) CodexTask {
	task := existing
	if !found {
		task = CodexTask{HostID: request.HostID, ThreadID: observation.ThreadID, FirstSeenAt: request.ObservedAt}
	}
	setIfPresent(&task.Title, observation.Title)
	setIfPresent(&task.Description, observation.Description)
	setIfPresent(&task.CWD, observation.CWD)
	setIfPresent(&task.Project, observation.Project)
	setIfPresent(&task.Status, observation.Status)
	if !observation.CreatedAt.IsZero() {
		task.CreatedAt = observation.CreatedAt
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = request.ObservedAt
	}
	if !observation.UpdatedAt.IsZero() {
		task.UpdatedAt = observation.UpdatedAt
	} else {
		task.UpdatedAt = request.ObservedAt
	}
	task.Unread = observation.Unread
	if observation.Coordinator != nil {
		task.Coordinator = *observation.Coordinator
	}
	mergeCodexTaskClassification(&task, request, observation.Classification)
	task.LastSeenAt = request.ObservedAt
	task.StateObservedAt = request.ObservedAt
	task.StateSnapshotID = request.RequestID
	task.DiscoverySource = request.Source
	task.LastSnapshotID = request.RequestID
	task.MissingSince = nil
	if observation.WaitCursor != nil {
		task.WaitCursor = *observation.WaitCursor
	}
	if observation.Tombstoned {
		at := request.ObservedAt
		task.TombstonedAt = &at
	} else {
		task.TombstonedAt = nil
	}
	return task
}

func mergeCodexTaskClassification(task *CodexTask, request CodexTaskIngestRequest, classification *CodexTaskClassification) bool {
	if classification == nil {
		return false
	}
	at := classification.ClassifiedAt
	if at.IsZero() {
		at = request.ObservedAt
	}
	lastClassifiedAt := time.Time{}
	if task.LastClassifiedAt != nil {
		lastClassifiedAt = *task.LastClassifiedAt
	}
	if !snapshotWins(at, request.RequestID, lastClassifiedAt, task.LastClassificationSnapshotID) {
		return false
	}
	task.Tier = classification.Tier
	task.LastMeaningfulOutcome = classification.LastMeaningfulOutcome
	task.UnresolvedLoop = classification.UnresolvedLoop
	task.SmallestNextAction = classification.SmallestNextAction
	task.OperatorDecision = classification.OperatorDecision
	task.LastClassifiedAt = &at
	task.LastClassificationSnapshotID = request.RequestID
	return true
}

func setIfPresent(target *string, value string) {
	if value != "" {
		*target = value
	}
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func snapshotWins(observedAt time.Time, requestID string, stateAt time.Time, stateRequestID string) bool {
	return observedAt.After(stateAt) || (observedAt.Equal(stateAt) && requestID > stateRequestID)
}
func pruneCodexTaskIngestReplays(q *sql.Tx, retain int) error {
	_, err := q.Exec(`DELETE FROM codex_task_ingests WHERE rowid IN (SELECT rowid FROM codex_task_ingests ORDER BY rowid DESC LIMIT -1 OFFSET ?)`, retain)
	return err
}
func formatDBTime(value time.Time) string { return value.UTC().Format(codexTaskDBTimeLayout) }
func parseDBTime(value string) (time.Time, error) {
	parsed, err := time.Parse(codexTaskDBTimeLayout, value)
	if err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

type scanner interface{ Scan(...any) error }

func scanCodexTask(row scanner) (CodexTask, error) {
	var task CodexTask
	var unread, coordinator int
	var created, updated, firstSeen, lastSeen, stateObserved string
	var classified, missing, tombstone sql.NullString
	err := row.Scan(&task.HostID, &task.ThreadID, &task.Title, &task.Description, &task.CWD, &task.Project, &task.Status, &unread, &coordinator, &task.Tier, &task.LastMeaningfulOutcome, &task.UnresolvedLoop, &task.SmallestNextAction, &task.OperatorDecision, &classified, &task.LastClassificationSnapshotID, &created, &updated, &firstSeen, &lastSeen, &stateObserved, &task.StateSnapshotID, &task.DiscoverySource, &task.WaitCursor, &task.LastSnapshotID, &missing, &tombstone)
	if err != nil {
		return CodexTask{}, err
	}
	task.Unread = unread != 0
	task.Coordinator = coordinator != 0
	parsedTimes := []struct {
		value  string
		target *time.Time
	}{{created, &task.CreatedAt}, {updated, &task.UpdatedAt}, {firstSeen, &task.FirstSeenAt}, {lastSeen, &task.LastSeenAt}, {stateObserved, &task.StateObservedAt}}
	for _, item := range parsedTimes {
		parsed, err := parseDBTime(item.value)
		if err != nil {
			return CodexTask{}, err
		}
		*item.target = parsed
	}
	if missing.Valid {
		at, err := parseDBTime(missing.String)
		if err != nil {
			return CodexTask{}, err
		}
		task.MissingSince = &at
	}
	if classified.Valid {
		at, err := parseDBTime(classified.String)
		if err != nil {
			return CodexTask{}, err
		}
		task.LastClassifiedAt = &at
	}
	if tombstone.Valid {
		at, err := parseDBTime(tombstone.String)
		if err != nil {
			return CodexTask{}, err
		}
		task.TombstonedAt = &at
	}
	return task, nil
}

func getCodexTask(q *sql.Tx, hostID, threadID string) (CodexTask, bool, error) {
	row := q.QueryRow(`SELECT host_id,thread_id,title,description,cwd,project,status,unread,coordinator,tier,last_meaningful_outcome,unresolved_loop,smallest_next_action,operator_decision,last_classified_at,last_classification_snapshot_id,created_at,updated_at,first_seen_at,last_seen_at,state_observed_at,state_snapshot_id,discovery_source,wait_cursor,last_snapshot_id,missing_since,tombstoned_at FROM codex_tasks WHERE host_id=? AND thread_id=?`, hostID, threadID)
	task, err := scanCodexTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexTask{}, false, nil
	}
	return task, err == nil, err
}

func upsertCodexTask(q *sql.Tx, task CodexTask) error {
	var classified, missing, tombstone any
	if task.LastClassifiedAt != nil {
		classified = formatDBTime(*task.LastClassifiedAt)
	}
	if task.MissingSince != nil {
		missing = formatDBTime(*task.MissingSince)
	}
	if task.TombstonedAt != nil {
		tombstone = formatDBTime(*task.TombstonedAt)
	}
	_, err := q.Exec(`INSERT INTO codex_tasks(host_id,thread_id,title,description,cwd,project,status,unread,coordinator,tier,last_meaningful_outcome,unresolved_loop,smallest_next_action,operator_decision,last_classified_at,last_classification_snapshot_id,created_at,updated_at,first_seen_at,last_seen_at,state_observed_at,state_snapshot_id,discovery_source,wait_cursor,last_snapshot_id,missing_since,tombstoned_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(host_id,thread_id) DO UPDATE SET title=excluded.title,description=excluded.description,cwd=excluded.cwd,project=excluded.project,status=excluded.status,unread=excluded.unread,coordinator=excluded.coordinator,tier=excluded.tier,last_meaningful_outcome=excluded.last_meaningful_outcome,unresolved_loop=excluded.unresolved_loop,smallest_next_action=excluded.smallest_next_action,operator_decision=excluded.operator_decision,last_classified_at=excluded.last_classified_at,last_classification_snapshot_id=excluded.last_classification_snapshot_id,created_at=excluded.created_at,updated_at=excluded.updated_at,first_seen_at=excluded.first_seen_at,last_seen_at=excluded.last_seen_at,state_observed_at=excluded.state_observed_at,state_snapshot_id=excluded.state_snapshot_id,discovery_source=excluded.discovery_source,wait_cursor=excluded.wait_cursor,last_snapshot_id=excluded.last_snapshot_id,missing_since=excluded.missing_since,tombstoned_at=excluded.tombstoned_at`, task.HostID, task.ThreadID, task.Title, task.Description, task.CWD, task.Project, task.Status, boolInt(task.Unread), boolInt(task.Coordinator), task.Tier, task.LastMeaningfulOutcome, task.UnresolvedLoop, task.SmallestNextAction, task.OperatorDecision, classified, task.LastClassificationSnapshotID, formatDBTime(task.CreatedAt), formatDBTime(task.UpdatedAt), formatDBTime(task.FirstSeenAt), formatDBTime(task.LastSeenAt), formatDBTime(task.StateObservedAt), task.StateSnapshotID, task.DiscoverySource, task.WaitCursor, task.LastSnapshotID, missing, tombstone)
	return err
}
