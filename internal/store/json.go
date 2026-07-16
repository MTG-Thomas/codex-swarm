package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
	_ "modernc.org/sqlite"
)

const stateLockTimeout = 5 * time.Second

type JSONStore struct {
	path string
	mu   sync.Mutex
	tx   *sql.Tx
}

type stateFile struct {
	Workers            []Worker            `json:"workers"`
	Schedules          []Schedule          `json:"schedules,omitempty"`
	Claims             []Claim             `json:"claims,omitempty"`
	Agents             []Agent             `json:"agents,omitempty"`
	Events             []Event             `json:"events,omitempty"`
	TraceLanes         []TraceLane         `json:"trace_lanes,omitempty"`
	GateEvidence       []GateEvidence      `json:"gate_evidence,omitempty"`
	CompletedMutations []CompletedMutation `json:"completed_mutations,omitempty"`
	BifrostChangesets  []BifrostChangeset  `json:"bifrost_changesets,omitempty"`
}

// NewJSONStore returns a SQLite-backed store rooted at path. The historical
// name is retained as a source-compatibility contract for existing callers.
func NewJSONStore(path string) *JSONStore {
	return &JSONStore{path: path}
}

// SaveWorker inserts or replaces one worker record.
func (s *JSONStore) SaveWorker(worker Worker) error {
	return s.withStateLock(func() error {
		normalizeWorkerLifecycleForSave(&worker)
		state, err := s.read()
		if err != nil {
			return err
		}

		found := false
		for i := range state.Workers {
			if state.Workers[i].ID == worker.ID {
				state.Workers[i] = worker
				found = true
				break
			}
		}
		if !found {
			state.Workers = append(state.Workers, worker)
		}

		return s.write(state)
	})
}

// SaveWorkers inserts or replaces worker records under one state lock.
func (s *JSONStore) SaveWorkers(workers ...Worker) error {
	return s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for _, worker := range workers {
			normalizeWorkerLifecycleForSave(&worker)
			found := false
			for i := range state.Workers {
				if state.Workers[i].ID == worker.ID {
					state.Workers[i] = worker
					found = true
					break
				}
			}
			if !found {
				state.Workers = append(state.Workers, worker)
			}
		}
		return s.write(state)
	})
}

// UpdateWorker mutates one worker while holding the state lock.
func (s *JSONStore) UpdateWorker(id string, mutate func(*Worker) error) (Worker, error) {
	var updated Worker
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for i := range state.Workers {
			if state.Workers[i].ID != id {
				continue
			}
			if err := mutate(&state.Workers[i]); err != nil {
				return err
			}
			normalizeWorkerLifecycleForSave(&state.Workers[i])
			updated = state.Workers[i]
			return s.write(state)
		}
		return fmt.Errorf("%w: %s", ErrWorkerNotFound, id)
	})
	if err != nil {
		return Worker{}, err
	}
	return updated, nil
}

// UpdateWorkers mutates multiple workers while holding one state lock.
func (s *JSONStore) UpdateWorkers(ids []string, mutate func(map[string]*Worker) error) (map[string]Worker, error) {
	updated := map[string]Worker{}
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}

		targets := map[string]*Worker{}
		for _, id := range ids {
			if _, ok := targets[id]; ok {
				continue
			}
			found := false
			for i := range state.Workers {
				if state.Workers[i].ID == id {
					targets[id] = &state.Workers[i]
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: %s", ErrWorkerNotFound, id)
			}
		}

		if err := mutate(targets); err != nil {
			return err
		}
		for id, worker := range targets {
			normalizeWorkerLifecycleForSave(worker)
			updated[id] = *worker
		}
		return s.write(state)
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// UpdateWorkersWithRequest applies an idempotent multi-worker mutation.
func (s *JSONStore) UpdateWorkersWithRequest(requestID, command, fingerprint string, ids []string, mutate func(map[string]*Worker) (WorkerMutationResult, error)) (WorkerMutationResult, bool, error) {
	var result WorkerMutationResult
	replayed := false
	if strings.TrimSpace(requestID) == "" {
		return WorkerMutationResult{}, false, errors.New("request id is required")
	}
	if strings.TrimSpace(fingerprint) == "" {
		return WorkerMutationResult{}, false, errors.New("mutation fingerprint is required")
	}
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}

		for _, completed := range state.CompletedMutations {
			if completed.RequestID == requestID && completed.Command == command {
				if completed.Fingerprint == "" {
					return fmt.Errorf("request %q for %s cannot be replayed without a stored fingerprint", requestID, command)
				}
				if completed.Fingerprint != fingerprint {
					return fmt.Errorf("request %q for %s does not match original mutation fingerprint", requestID, command)
				}
				result.Output = completed.Output
				replayed = true
				return nil
			}
		}

		targets := map[string]*Worker{}
		for _, id := range ids {
			if _, ok := targets[id]; ok {
				continue
			}
			found := false
			for i := range state.Workers {
				if state.Workers[i].ID == id {
					targets[id] = &state.Workers[i]
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: %s", ErrWorkerNotFound, id)
			}
		}

		mutated, err := mutate(targets)
		if err != nil {
			return err
		}
		if strings.TrimSpace(mutated.Fingerprint) == "" {
			return errors.New("mutation result fingerprint is required")
		}
		for _, worker := range targets {
			normalizeWorkerLifecycleForSave(worker)
		}
		state.Events = appendBoundedEvents(state.Events, mutated.Events, SwarmEventCap)
		state.CompletedMutations = appendBoundedCompletedMutations(state.CompletedMutations, CompletedMutation{
			RequestID:   requestID,
			Command:     command,
			Fingerprint: mutated.Fingerprint,
			Output:      mutated.Output,
			CreatedAt:   completedMutationTime(mutated.Events),
		}, CompletedMutationCacheCap)
		result = mutated
		return s.write(state)
	})
	if err != nil {
		return WorkerMutationResult{}, false, err
	}
	return result, replayed, nil
}

// GetWorker returns one worker by ID.
func (s *JSONStore) GetWorker(id string) (Worker, error) {
	var got Worker
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for _, worker := range state.Workers {
			if worker.ID == id {
				got = worker
				return nil
			}
		}
		return ErrWorkerNotFound
	})
	if err != nil {
		return Worker{}, err
	}
	return got, nil
}

// ListWorkers returns workers sorted by most recent update.
func (s *JSONStore) ListWorkers() ([]Worker, error) {
	var workers []Worker
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		workers = append([]Worker(nil), state.Workers...)
		sort.Slice(workers, func(i, j int) bool {
			return workers[i].UpdatedAt.After(workers[j].UpdatedAt)
		})
		return nil
	})
	return workers, err
}

// ListEvents returns the durable swarm event log.
func (s *JSONStore) ListEvents() ([]Event, error) {
	var events []Event
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		events = append([]Event(nil), state.Events...)
		return nil
	})
	return events, err
}

// UpdateTraceLane mutates a per-agent trace lane while holding the state lock.
func (s *JSONStore) UpdateTraceLane(agent string, mutate func(*TraceLane) error) (TraceLane, error) {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return TraceLane{}, errors.New("trace agent is required")
	}
	var updated TraceLane
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for i := range state.TraceLanes {
			if state.TraceLanes[i].Agent != agent {
				continue
			}
			if err := mutate(&state.TraceLanes[i]); err != nil {
				return err
			}
			updated = state.TraceLanes[i]
			return s.write(state)
		}
		lane := TraceLane{Agent: agent}
		if err := mutate(&lane); err != nil {
			return err
		}
		state.TraceLanes = append(state.TraceLanes, lane)
		updated = lane
		return s.write(state)
	})
	if err != nil {
		return TraceLane{}, err
	}
	return updated, nil
}

// GetTraceLane returns one trace lane by agent name.
func (s *JSONStore) GetTraceLane(agent string) (TraceLane, error) {
	var got TraceLane
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for _, lane := range state.TraceLanes {
			if lane.Agent == agent {
				got = lane
				return nil
			}
		}
		return ErrTraceNotFound
	})
	if err != nil {
		return TraceLane{}, err
	}
	return got, nil
}

// ListTraceLanes returns trace lanes sorted by most recent update.
func (s *JSONStore) ListTraceLanes() ([]TraceLane, error) {
	var lanes []TraceLane
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		lanes = append([]TraceLane(nil), state.TraceLanes...)
		sort.Slice(lanes, func(i, j int) bool {
			return lanes[i].UpdatedAt.After(lanes[j].UpdatedAt)
		})
		return nil
	})
	return lanes, err
}

// SaveGateEvidence inserts or replaces one quality gate evidence record.
func (s *JSONStore) SaveGateEvidence(evidence GateEvidence) error {
	return s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}

		found := false
		for i := range state.GateEvidence {
			if state.GateEvidence[i].ID == evidence.ID {
				state.GateEvidence[i] = evidence
				found = true
				break
			}
		}
		if !found {
			state.GateEvidence = append(state.GateEvidence, evidence)
		}

		return s.write(state)
	})
}

// ListGateEvidence returns quality gate evidence sorted by newest first.
func (s *JSONStore) ListGateEvidence() ([]GateEvidence, error) {
	var evidence []GateEvidence
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		evidence = append([]GateEvidence(nil), state.GateEvidence...)
		sort.Slice(evidence, func(i, j int) bool {
			return evidence[i].CreatedAt.After(evidence[j].CreatedAt)
		})
		return nil
	})
	return evidence, err
}

// SaveSchedule inserts or replaces one schedule record.
func (s *JSONStore) SaveSchedule(schedule Schedule) error {
	return s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}

		found := false
		for i := range state.Schedules {
			if state.Schedules[i].ID == schedule.ID {
				state.Schedules[i] = schedule
				found = true
				break
			}
		}
		if !found {
			state.Schedules = append(state.Schedules, schedule)
		}

		return s.write(state)
	})
}

// ListSchedules returns schedules sorted by most recent update.
func (s *JSONStore) ListSchedules() ([]Schedule, error) {
	var schedules []Schedule
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		schedules = append([]Schedule(nil), state.Schedules...)
		sort.Slice(schedules, func(i, j int) bool {
			return schedules[i].UpdatedAt.After(schedules[j].UpdatedAt)
		})
		return nil
	})
	return schedules, err
}

// SaveClaim inserts or replaces one claim record.
func (s *JSONStore) SaveClaim(claim Claim) error {
	return s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}

		found := false
		for i := range state.Claims {
			if state.Claims[i].ID == claim.ID {
				state.Claims[i] = claim
				found = true
				break
			}
		}
		if !found {
			state.Claims = append(state.Claims, claim)
		}

		return s.write(state)
	})
}

// SaveClaimValidated validates workers and existing claims, then saves a claim
// under the same state lock and returns the previous claim snapshot.
func (s *JSONStore) SaveClaimValidated(claim Claim, validate func([]Worker, []Claim) error) ([]Claim, error) {
	var previous []Claim
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		if validate != nil {
			if err := validate(state.Workers, state.Claims); err != nil {
				return err
			}
		}
		previous = append([]Claim(nil), state.Claims...)

		found := false
		for i := range state.Claims {
			if state.Claims[i].ID == claim.ID {
				state.Claims[i] = claim
				found = true
				break
			}
		}
		if !found {
			state.Claims = append(state.Claims, claim)
		}

		return s.write(state)
	})
	if err != nil {
		return nil, err
	}
	return previous, nil
}

// ImportClaims imports issue-backed claims with optional conflict override.
func (s *JSONStore) ImportClaims(claims []Claim, force bool) (imported int, skipped int, conflicted int, err error) {
	err = s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}

		for _, claim := range claims {
			found := false
			for i := range state.Claims {
				if state.Claims[i].ID != claim.ID {
					continue
				}
				found = true
				if !force && state.Claims[i].UpdatedAt.After(claim.UpdatedAt) {
					skipped++
					conflicted++
					break
				}
				state.Claims[i] = claim
				imported++
				break
			}
			if !found {
				state.Claims = append(state.Claims, claim)
				imported++
			}
		}

		return s.write(state)
	})
	return imported, skipped, conflicted, err
}

// GetClaim returns one claim by ID.
func (s *JSONStore) GetClaim(id string) (Claim, error) {
	var got Claim
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for _, claim := range state.Claims {
			if claim.ID == id {
				got = claim
				return nil
			}
		}
		return ErrClaimNotFound
	})
	if err != nil {
		return Claim{}, err
	}
	return got, nil
}

// ListClaims returns claims sorted by most recent update.
func (s *JSONStore) ListClaims() ([]Claim, error) {
	var claims []Claim
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		claims = append([]Claim(nil), state.Claims...)
		sort.Slice(claims, func(i, j int) bool {
			return claims[i].UpdatedAt.After(claims[j].UpdatedAt)
		})
		return nil
	})
	return claims, err
}

// SaveAgent inserts or replaces one agent identity record.
func (s *JSONStore) SaveAgent(agent Agent) error {
	return s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}

		found := false
		for i := range state.Agents {
			if state.Agents[i].ID == agent.ID {
				state.Agents[i] = agent
				found = true
				break
			}
		}
		if !found {
			state.Agents = append(state.Agents, agent)
		}
		if agent.Current {
			for i := range state.Agents {
				if state.Agents[i].ID != agent.ID {
					state.Agents[i].Current = false
				}
			}
		}

		return s.write(state)
	})
}

// GetAgent returns one registered agent by ID.
func (s *JSONStore) GetAgent(id string) (Agent, error) {
	var got Agent
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for _, agent := range state.Agents {
			if agent.ID == id {
				got = agent
				return nil
			}
		}
		return ErrAgentNotFound
	})
	if err != nil {
		return Agent{}, err
	}
	return got, nil
}

// CurrentAgent returns the agent identity marked current.
func (s *JSONStore) CurrentAgent() (Agent, error) {
	var got Agent
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for _, agent := range state.Agents {
			if agent.Current {
				got = agent
				return nil
			}
		}
		return ErrAgentNotFound
	})
	if err != nil {
		return Agent{}, err
	}
	return got, nil
}

// ListAgents returns agents sorted by most recent update.
func (s *JSONStore) ListAgents() ([]Agent, error) {
	var agents []Agent
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		agents = append([]Agent(nil), state.Agents...)
		sort.Slice(agents, func(i, j int) bool {
			return agents[i].UpdatedAt.After(agents[j].UpdatedAt)
		})
		return nil
	})
	return agents, err
}

// SaveBifrostChangeset inserts or replaces one Bifrost changeset record.
func (s *JSONStore) SaveBifrostChangeset(changeset BifrostChangeset) error {
	return s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for i := range state.BifrostChangesets {
			if state.BifrostChangesets[i].ID == changeset.ID {
				state.BifrostChangesets[i] = changeset
				return s.write(state)
			}
		}
		state.BifrostChangesets = append(state.BifrostChangesets, changeset)
		return s.write(state)
	})
}

// GetBifrostChangeset returns one Bifrost changeset by ID.
func (s *JSONStore) GetBifrostChangeset(id string) (BifrostChangeset, error) {
	var got BifrostChangeset
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for _, changeset := range state.BifrostChangesets {
			if changeset.ID == id {
				got = changeset
				return nil
			}
		}
		return fmt.Errorf("%w: %s", ErrBifrostChangesetNotFound, id)
	})
	return got, err
}

// ListBifrostChangesets returns changesets sorted by most recent update.
func (s *JSONStore) ListBifrostChangesets() ([]BifrostChangeset, error) {
	var changesets []BifrostChangeset
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		changesets = append([]BifrostChangeset(nil), state.BifrostChangesets...)
		sort.Slice(changesets, func(i, j int) bool {
			return changesets[i].UpdatedAt.After(changesets[j].UpdatedAt)
		})
		return nil
	})
	return changesets, err
}

func (s *JSONStore) withStateLock(fn func() error) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lock, err := acquireStateLock(s.path)
	if err != nil {
		return err
	}
	if err := s.ensureSQLite(); err != nil {
		_ = lock.release()
		return err
	}
	if err := lock.release(); err != nil {
		return err
	}
	db, err := openSQLite(s.path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin SQLite state transaction %s: %w", s.path, err)
	}
	s.tx = tx
	defer func() { s.tx = nil }()
	if err := fn(); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite state transaction %s: %w", s.path, err)
	}
	return nil
}

func (s *JSONStore) read() (stateFile, error) {
	if s.tx == nil {
		return stateFile{}, errors.New("read state outside SQLite transaction")
	}
	state, err := readState(s.tx)
	if err != nil {
		return stateFile{}, fmt.Errorf("read SQLite state %s: %w", s.path, err)
	}
	for i := range state.Workers {
		normalizeWorkerLifecycleForRead(&state.Workers[i])
	}
	return state, nil
}

func (s *JSONStore) write(state stateFile) error {
	if s.tx == nil {
		return errors.New("write state outside SQLite transaction")
	}
	if err := writeState(s.tx, state); err != nil {
		return fmt.Errorf("write SQLite state %s: %w", s.path, err)
	}
	return nil
}

const sqliteHeader = "SQLite format 3\x00"

func (s *JSONStore) ensureSQLite() error {
	data, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect state %s: %w", s.path, err)
	}
	if err == nil && bytes.HasPrefix(data, []byte(sqliteHeader)) {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) || len(data) == 0 {
		db, openErr := openSQLite(s.path)
		if openErr != nil {
			return openErr
		}
		return db.Close()
	}

	var legacy stateFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("parse legacy JSON state %s before migration: %w", s.path, err)
	}
	backup := s.path + ".legacy.json"
	if existing, readErr := os.ReadFile(backup); readErr == nil {
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("legacy backup %s already exists with different contents", backup)
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		if err := writeFileDurably(backup, data); err != nil {
			return fmt.Errorf("back up legacy state to %s: %w", backup, err)
		}
		if err := syncParentDir(backup); err != nil {
			return fmt.Errorf("sync legacy backup directory: %w", err)
		}
	} else {
		return fmt.Errorf("inspect legacy backup %s: %w", backup, readErr)
	}

	tmp := fmt.Sprintf("%s.migrate.%d", s.path, os.Getpid())
	_ = os.Remove(tmp)
	db, err := openSQLite(tmp)
	if err != nil {
		return fmt.Errorf("create migration database: %w", err)
	}
	tx, err := db.Begin()
	if err == nil {
		err = writeState(tx, legacy)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("import legacy state into SQLite: %w", errors.Join(err, closeErr))
	}
	if err := replaceStateFile(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("activate migrated SQLite state: %w", err)
	}
	if err := syncParentDir(s.path); err != nil {
		return fmt.Errorf("sync migrated state directory: %w", err)
	}
	return nil
}

func openSQLite(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	// Immediate transactions acquire the write reservation before callers read
	// their mutation snapshot, preventing stale read/modify/write races.
	db, err := sql.Open("sqlite", path+"?_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open SQLite state %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = FULL`,
		`CREATE TABLE IF NOT EXISTS records (
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(kind, id)
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize SQLite state %s: %w", path, err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure SQLite state %s: %w", path, err)
	}
	return db, nil
}

type sqlExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

func readState(q sqlExecutor) (stateFile, error) {
	rows, err := q.Query(`SELECT kind, payload FROM records`)
	if err != nil {
		return stateFile{}, err
	}
	defer rows.Close()
	var state stateFile
	for rows.Next() {
		var kind string
		var payload []byte
		if err := rows.Scan(&kind, &payload); err != nil {
			return stateFile{}, err
		}
		if err := appendRecord(&state, kind, payload); err != nil {
			return stateFile{}, err
		}
	}
	return state, rows.Err()
}

func appendRecord(state *stateFile, kind string, payload []byte) error {
	switch kind {
	case "worker":
		return unmarshalAppend(payload, &state.Workers)
	case "schedule":
		return unmarshalAppend(payload, &state.Schedules)
	case "claim":
		return unmarshalAppend(payload, &state.Claims)
	case "agent":
		return unmarshalAppend(payload, &state.Agents)
	case "trace_lane":
		return unmarshalAppend(payload, &state.TraceLanes)
	case "gate_evidence":
		return unmarshalAppend(payload, &state.GateEvidence)
	case "bifrost_changeset":
		return unmarshalAppend(payload, &state.BifrostChangesets)
	case "events":
		return json.Unmarshal(payload, &state.Events)
	case "completed_mutations":
		return json.Unmarshal(payload, &state.CompletedMutations)
	default:
		return nil
	}
}

func unmarshalAppend[T any](payload []byte, target *[]T) error {
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return err
	}
	*target = append(*target, value)
	return nil
}

func writeState(q sqlExecutor, state stateFile) error {
	records := make(map[string]map[string]any)
	add := func(kind, id string, value any) {
		if records[kind] == nil {
			records[kind] = make(map[string]any)
		}
		records[kind][id] = value
	}
	for _, value := range state.Workers {
		add("worker", value.ID, value)
	}
	for _, value := range state.Schedules {
		add("schedule", value.ID, value)
	}
	for _, value := range state.Claims {
		add("claim", value.ID, value)
	}
	for _, value := range state.Agents {
		add("agent", value.ID, value)
	}
	for _, value := range state.TraceLanes {
		add("trace_lane", value.Agent, value)
	}
	for _, value := range state.GateEvidence {
		add("gate_evidence", value.ID, value)
	}
	for _, value := range state.BifrostChangesets {
		add("bifrost_changeset", value.ID, value)
	}
	add("events", "singleton", state.Events)
	add("completed_mutations", "singleton", state.CompletedMutations)

	for kind, values := range records {
		args := []any{kind}
		placeholders := make([]string, 0, len(values))
		for id, value := range values {
			payload, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if _, err := q.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)
				ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload, updated_at=excluded.updated_at
				WHERE records.payload <> excluded.payload`, kind, id, payload, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		if _, err := q.Exec(`DELETE FROM records WHERE kind = ? AND id NOT IN (`+strings.Join(placeholders, ",")+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

func writeFileDurably(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, err := file.Write(data)
	if err != nil {
		_ = file.Close()
		return err
	}
	if written != len(data) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

type stateLock struct {
	path string
	file *os.File
}

func acquireStateLock(statePath string) (*stateLock, error) {
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	lockPath := statePath + ".lock"
	deadline := time.Now().Add(stateLockTimeout)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("open state lock %s: timed out after %s: %w", lockPath, stateLockTimeout, err)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		locked, err := tryLockStateFile(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("lock state %s: %w", lockPath, err)
		}
		if locked {
			if err := writeStateLockMetadata(file); err != nil {
				_ = unlockStateFile(file)
				_ = file.Close()
				return nil, fmt.Errorf("write state lock %s: %w", lockPath, err)
			}
			return &stateLock{path: lockPath, file: file}, nil
		}
		_ = file.Close()
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("acquire state lock %s: timed out after %s", lockPath, stateLockTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeStateLockMetadata(file *os.File) error {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	executable, err := os.Executable()
	if err != nil {
		executable = "unknown"
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "pid=%d\nhostname=%s\nexecutable=%s\nacquired_at=%s\n", os.Getpid(), hostname, executable, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return file.Sync()
}

func (l *stateLock) release() error {
	var err error
	if unlockErr := unlockStateFile(l.file); unlockErr != nil {
		err = fmt.Errorf("unlock state lock %s: %w", l.path, unlockErr)
	}
	if closeErr := l.file.Close(); closeErr != nil {
		if err != nil {
			return errors.Join(err, fmt.Errorf("close state lock %s: %w", l.path, closeErr))
		}
		return fmt.Errorf("close state lock %s: %w", l.path, closeErr)
	}
	return err
}

func normalizeWorkerLifecycleForRead(worker *Worker) {
	if worker.Lifecycle == nil {
		lc := lifecycleFromWorkerStatus(worker.Status)
		worker.Lifecycle = &lc
	} else if worker.Lifecycle.Version == 0 {
		worker.Lifecycle.Version = lifecycle.CurrentVersion
	}
	worker.Lifecycle.ClearTerminalMarkersForNonTerminal()
	worker.Status = workerStatusFromLifecycle(*worker.Lifecycle)
}

func normalizeWorkerLifecycleForSave(worker *Worker) {
	if worker.Lifecycle == nil {
		lc := lifecycleFromWorkerStatus(worker.Status)
		worker.Lifecycle = &lc
	} else if worker.Lifecycle.Version == 0 {
		worker.Lifecycle.Version = lifecycle.CurrentVersion
	}
	worker.Lifecycle.ClearTerminalMarkersForNonTerminal()
	worker.Status = workerStatusFromLifecycle(*worker.Lifecycle)
}

func appendBoundedEvents(existing []Event, added []Event, maxItems int) []Event {
	if len(added) == 0 {
		return existing
	}
	events := append(existing, added...)
	if maxItems <= 0 || len(events) <= maxItems {
		return events
	}
	return append([]Event(nil), events[len(events)-maxItems:]...)
}

func appendBoundedCompletedMutations(existing []CompletedMutation, added CompletedMutation, maxItems int) []CompletedMutation {
	mutations := append(existing, added)
	if maxItems <= 0 || len(mutations) <= maxItems {
		return mutations
	}
	return append([]CompletedMutation(nil), mutations[len(mutations)-maxItems:]...)
}

func completedMutationTime(events []Event) time.Time {
	for _, event := range events {
		if !event.At.IsZero() {
			return event.At
		}
	}
	return time.Now().UTC()
}
