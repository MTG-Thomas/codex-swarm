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

const stateLockTimeout = 15 * time.Second

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
		return s.upsert("worker", worker.ID, worker)
	})
}

// SaveWorkers inserts or replaces worker records under one state lock.
func (s *JSONStore) SaveWorkers(workers ...Worker) error {
	return s.withStateLock(func() error {
		for _, worker := range workers {
			normalizeWorkerLifecycleForSave(&worker)
			if err := s.upsert("worker", worker.ID, worker); err != nil {
				return err
			}
		}
		return nil
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
			return s.upsert("worker", updated.ID, updated)
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
		for id, worker := range updated {
			if err := s.upsert("worker", id, worker); err != nil {
				return err
			}
		}
		return nil
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
		for id, worker := range targets {
			if err := s.upsert("worker", id, *worker); err != nil {
				return err
			}
		}
		if err := s.upsert("events", "singleton", state.Events); err != nil {
			return err
		}
		return s.upsert("completed_mutations", "singleton", state.CompletedMutations)
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
		worker, found, err := getRecord[Worker](s.tx, "worker", id)
		if err != nil {
			return err
		}
		if !found {
			return ErrWorkerNotFound
		}
		normalizeWorkerLifecycleForRead(&worker)
		got = worker
		return nil
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
		var err error
		workers, err = listRecords[Worker](s.tx, "worker")
		if err != nil {
			return err
		}
		for i := range workers {
			normalizeWorkerLifecycleForRead(&workers[i])
		}
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
			return s.upsert("trace_lane", updated.Agent, updated)
		}
		lane := TraceLane{Agent: agent}
		if err := mutate(&lane); err != nil {
			return err
		}
		state.TraceLanes = append(state.TraceLanes, lane)
		updated = lane
		return s.upsert("trace_lane", updated.Agent, updated)
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
		return s.upsert("gate_evidence", evidence.ID, evidence)
	})
}

// ListGateEvidence returns quality gate evidence sorted by newest first.
func (s *JSONStore) ListGateEvidence() ([]GateEvidence, error) {
	var evidence []GateEvidence
	err := s.withStateLock(func() error {
		var err error
		evidence, err = listRecords[GateEvidence](s.tx, "gate_evidence")
		if err != nil {
			return err
		}
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
		return s.upsert("schedule", schedule.ID, schedule)
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
		return s.upsert("claim", claim.ID, claim)
	})
}

// SaveClaimValidated validates workers and existing claims, then saves a claim
// under the same state lock and returns the previous claim snapshot.
func (s *JSONStore) SaveClaimValidated(claim Claim, validate func([]Worker, []Claim) error) ([]Claim, error) {
	return s.SaveClaimsValidated([]Claim{claim}, validate)
}

// SaveClaimsValidated validates and inserts a claim set in one transaction.
func (s *JSONStore) SaveClaimsValidated(claims []Claim, validate func([]Worker, []Claim) error) ([]Claim, error) {
	var previous []Claim
	err := s.withStateLock(func() error {
		workers, err := listRecords[Worker](s.tx, "worker")
		if err != nil {
			return err
		}
		for i := range workers {
			normalizeWorkerLifecycleForRead(&workers[i])
		}
		existing, err := listRecords[Claim](s.tx, "claim")
		if err != nil {
			return err
		}
		if validate != nil {
			if err := validate(workers, existing); err != nil {
				return err
			}
		}
		previous = append([]Claim(nil), existing...)
		for _, claim := range claims {
			if err := s.upsert("claim", claim.ID, claim); err != nil {
				return err
			}
		}
		return nil
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

		for _, claim := range claims {
			for _, current := range state.Claims {
				if current.ID == claim.ID && (force || !current.UpdatedAt.After(claim.UpdatedAt)) {
					if err := s.upsert("claim", current.ID, current); err != nil {
						return err
					}
					break
				}
			}
		}
		return nil
	})
	return imported, skipped, conflicted, err
}

// GetClaim returns one claim by ID.
func (s *JSONStore) GetClaim(id string) (Claim, error) {
	var got Claim
	err := s.withStateLock(func() error {
		claim, found, err := getRecord[Claim](s.tx, "claim", id)
		if err != nil {
			return err
		}
		if !found {
			return ErrClaimNotFound
		}
		got = claim
		return nil
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
		var err error
		claims, err = listRecords[Claim](s.tx, "claim")
		if err != nil {
			return err
		}
		sort.Slice(claims, func(i, j int) bool {
			return claims[i].UpdatedAt.After(claims[j].UpdatedAt)
		})
		return nil
	})
	return claims, err
}

// CoordinationMetrics reports durable coordination coverage without loading unrelated records.
func (s *JSONStore) CoordinationMetrics(now time.Time) (CoordinationMetrics, error) {
	metrics := CoordinationMetrics{Backend: "sqlite"}
	err := s.withStateLock(func() error {
		workers, err := listRecords[Worker](s.tx, "worker")
		if err != nil {
			return err
		}
		claimList, err := listRecords[Claim](s.tx, "claim")
		if err != nil {
			return err
		}
		metrics.WorkerCount = len(workers)
		for i := range workers {
			normalizeWorkerLifecycleForRead(&workers[i])
			worker := workers[i]
			if worker.Status != WorkerDone && worker.Status != WorkerFailed {
				metrics.ActiveWorkers++
			}
			capabilities := CapabilitiesForWorker(worker)
			if capabilities.Has(CapabilityLiveMessage) {
				metrics.LiveMessageWorkers++
				if worker.Status == WorkerRunning && strings.TrimSpace(worker.ThreadID) != "" && strings.TrimSpace(worker.TurnID) != "" {
					metrics.SteerableWorkers++
				}
			}
			if capabilities.Has(CapabilityResume) {
				metrics.ResumeWorkers++
			}
			if capabilities.Has(CapabilityManagedWorktree) {
				metrics.ManagedWorktreeWorkers++
			}
			if capabilities.Has(CapabilityAutomaticCompletion) {
				metrics.AutomaticCompletionWorkers++
			}
			if capabilities.Has(CapabilityExternalTracker) {
				metrics.ExternalTrackerWorkers++
			}
		}
		metrics.ClaimCount = len(claimList)
		for _, claim := range claimList {
			if (claim.Status == ClaimActive || claim.Status == ClaimBlocked) && (claim.ExpiresAt.IsZero() || claim.ExpiresAt.After(now)) {
				metrics.ActiveClaims++
			}
		}
		counts := []struct {
			target *int
			query  string
			args   []any
		}{
			{&metrics.MessageCount, `SELECT COUNT(*) FROM messages`, nil},
			{&metrics.QueuedMessages, `SELECT COUNT(*) FROM message_deliveries WHERE state=?`, []any{DeliveryQueued}},
			{&metrics.SteeredMessages, `SELECT COUNT(*) FROM message_deliveries WHERE state=?`, []any{DeliverySteered}},
			{&metrics.DeliveredMessages, `SELECT COUNT(*) FROM message_deliveries WHERE state=?`, []any{DeliveryDelivered}},
			{&metrics.RecentTouches, `SELECT COUNT(*) FROM file_touches WHERE created_at>=?`, []any{now.Add(-touchRetention).UTC().Format(time.RFC3339Nano)}},
			{&metrics.ConflictMessages, `SELECT COUNT(*) FROM messages WHERE kind=?`, []any{MessageConflict}},
		}
		for _, count := range counts {
			value, err := queryCount(s.tx, count.query, count.args...)
			if err != nil {
				return err
			}
			*count.target = value
		}
		return nil
	})
	return metrics, err
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

		for _, current := range state.Agents {
			if current.ID == agent.ID || agent.Current {
				if err := s.upsert("agent", current.ID, current); err != nil {
					return err
				}
			}
		}
		return nil
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
		return s.upsert("bifrost_changeset", changeset.ID, changeset)
	})
}

// UpdateBifrostChangeset mutates one Bifrost changeset atomically.
func (s *JSONStore) UpdateBifrostChangeset(id string, mutate func(*BifrostChangeset) error) (BifrostChangeset, error) {
	var updated BifrostChangeset
	err := s.withStateLock(func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		for i := range state.BifrostChangesets {
			if state.BifrostChangesets[i].ID != id {
				continue
			}
			if err := mutate(&state.BifrostChangesets[i]); err != nil {
				return err
			}
			state.BifrostChangesets[i].ID = id
			updated = state.BifrostChangesets[i]
			return s.upsert("bifrost_changeset", id, updated)
		}
		return fmt.Errorf("%w: %s", ErrBifrostChangesetNotFound, id)
	})
	return updated, err
}

// DeleteBifrostChangeset removes one Bifrost changeset by ID.
func (s *JSONStore) DeleteBifrostChangeset(id string) error {
	return s.withStateLock(func() error {
		result, err := s.tx.Exec(`DELETE FROM records WHERE kind = ? AND id = ?`, "bifrost_changeset", id)
		if err != nil {
			return fmt.Errorf("delete SQLite bifrost_changeset %s: %w", id, err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("confirm deleted SQLite bifrost_changeset %s: %w", id, err)
		}
		if deleted == 0 {
			return fmt.Errorf("%w: %s", ErrBifrostChangesetNotFound, id)
		}
		return nil
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

	isSQLite, err := hasSQLiteHeader(s.path)
	if err != nil {
		return err
	}
	if !isSQLite {
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
	}
	db, err := openSQLite(s.path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
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

func hasSQLiteHeader(path string) (isSQLite bool, err error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect state %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	header := make([]byte, len(sqliteHeader))
	n, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, fmt.Errorf("inspect state header %s: %w", path, err)
	}
	return n == len(header) && bytes.Equal(header, []byte(sqliteHeader)), nil
}

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
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS records (
			kind TEXT NOT NULL,
			id TEXT NOT NULL,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(kind, id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			sender TEXT NOT NULL,
			body TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS message_deliveries (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			recipient_id TEXT NOT NULL,
			state TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(message_id, recipient_id)
		)`,
		`CREATE INDEX IF NOT EXISTS message_deliveries_recipient_state ON message_deliveries(recipient_id, state, created_at)`,
		`CREATE TABLE IF NOT EXISTS message_delivery_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			delivery_id TEXT NOT NULL REFERENCES message_deliveries(id) ON DELETE CASCADE,
			state TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS message_delivery_events_delivery_sequence ON message_delivery_events(delivery_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS file_touches (
			id TEXT PRIMARY KEY,
			worker_id TEXT NOT NULL,
			repo TEXT NOT NULL,
			repo_key TEXT NOT NULL,
			path TEXT NOT NULL,
			path_key TEXT NOT NULL,
			operation TEXT NOT NULL,
			line_start INTEGER NOT NULL DEFAULT 0,
			line_end INTEGER NOT NULL DEFAULT 0,
			intent TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS file_touches_conflict_lookup ON file_touches(repo_key, path_key, operation, created_at)`,
		`CREATE TABLE IF NOT EXISTS codex_tasks (
			host_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			unread INTEGER NOT NULL DEFAULT 0,
			coordinator INTEGER NOT NULL DEFAULT 0,
			tier TEXT NOT NULL DEFAULT '',
			last_meaningful_outcome TEXT NOT NULL DEFAULT '',
			unresolved_loop TEXT NOT NULL DEFAULT '',
			smallest_next_action TEXT NOT NULL DEFAULT '',
			operator_decision TEXT NOT NULL DEFAULT '',
			last_classified_at TEXT,
			last_classification_snapshot_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			state_observed_at TEXT NOT NULL,
			state_snapshot_id TEXT NOT NULL,
			discovery_source TEXT NOT NULL,
			wait_cursor TEXT NOT NULL DEFAULT '',
			last_snapshot_id TEXT NOT NULL,
			missing_since TEXT,
			tombstoned_at TEXT,
			PRIMARY KEY(host_id, thread_id)
		)`,
		`CREATE INDEX IF NOT EXISTS codex_tasks_seen_order ON codex_tasks(last_seen_at DESC, host_id, thread_id)`,
		`CREATE INDEX IF NOT EXISTS codex_tasks_filters ON codex_tasks(host_id, status, unread, discovery_source)`,
		`CREATE TABLE IF NOT EXISTS codex_task_ingests (
			request_id TEXT PRIMARY KEY,
			fingerprint TEXT NOT NULL,
			result BLOB NOT NULL,
			created_at TEXT NOT NULL
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

func getRecord[T any](q sqlExecutor, kind, id string) (T, bool, error) {
	var zero T
	rows, err := q.Query(`SELECT payload FROM records WHERE kind=? AND id=?`, kind, id)
	if err != nil {
		return zero, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return zero, false, rows.Err()
	}
	var payload []byte
	if err := rows.Scan(&payload); err != nil {
		return zero, false, err
	}
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return zero, false, fmt.Errorf("decode SQLite record kind=%q id=%q: %w", kind, id, err)
	}
	return value, true, rows.Err()
}

func listRecords[T any](q sqlExecutor, kind string) (values []T, err error) {
	rows, err := q.Query(`SELECT payload FROM records WHERE kind=?`, kind)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var value T
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, fmt.Errorf("decode SQLite record kind=%q: %w", kind, err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func queryCount(q sqlExecutor, query string, args ...any) (int, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, rows.Err()
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		return 0, err
	}
	return count, rows.Err()
}

func readState(q sqlExecutor) (state stateFile, err error) {
	rows, err := q.Query(`SELECT kind, id, payload FROM records`)
	if err != nil {
		return stateFile{}, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var kind string
		var id string
		var payload []byte
		if err := rows.Scan(&kind, &id, &payload); err != nil {
			return stateFile{}, fmt.Errorf("scan SQLite record kind=%q id=%q: %w", kind, id, err)
		}
		if err := appendRecord(&state, kind, payload); err != nil {
			return stateFile{}, fmt.Errorf("decode SQLite record kind=%q id=%q: %w", kind, id, err)
		}
	}
	return state, rows.Err()
}

func (s *JSONStore) upsert(kind, id string, value any) error {
	if s.tx == nil {
		return errors.New("write state outside SQLite transaction")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s %s: %w", kind, id, err)
	}
	if _, err := s.tx.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload, updated_at=excluded.updated_at
		WHERE records.payload <> excluded.payload`, kind, id, payload, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("upsert SQLite %s %s: %w", kind, id, err)
	}
	return nil
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
	if _, err := q.Exec(`DELETE FROM records`); err != nil {
		return err
	}
	records := make(map[string]map[string]any)
	for _, kind := range []string{"worker", "schedule", "claim", "agent", "trace_lane", "gate_evidence", "bifrost_changeset", "events", "completed_mutations"} {
		records[kind] = make(map[string]any)
	}
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
		for id, value := range values {
			payload, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("marshal replacement record kind=%q id=%q: %w", kind, id, err)
			}
			if _, err := q.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?)
				ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload, updated_at=excluded.updated_at
				WHERE records.payload <> excluded.payload`, kind, id, payload, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("replace SQLite record kind=%q id=%q: %w", kind, id, err)
			}
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
