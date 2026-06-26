package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/lifecycle"
)

const stateLockTimeout = 5 * time.Second

type JSONStore struct {
	path string
	mu   sync.Mutex
}

type stateFile struct {
	Workers   []Worker   `json:"workers"`
	Schedules []Schedule `json:"schedules,omitempty"`
	Claims    []Claim    `json:"claims,omitempty"`
	Agents    []Agent    `json:"agents,omitempty"`
}

func NewJSONStore(path string) *JSONStore {
	return &JSONStore{path: path}
}

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

func (s *JSONStore) withStateLock(fn func() error) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	lock, err := acquireStateLock(s.path)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	return fn()
}

func (s *JSONStore) read() (stateFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return stateFile{}, nil
		}
		return stateFile{}, fmt.Errorf("read state %s: %w", s.path, err)
	}
	if len(data) == 0 {
		return stateFile{}, nil
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return stateFile{}, fmt.Errorf("parse state %s: %w", s.path, err)
	}
	for i := range state.Workers {
		normalizeWorkerLifecycleForRead(&state.Workers[i])
	}
	return state, nil
}

func (s *JSONStore) write(state stateFile) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	tmp := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	if err := writeFileDurably(tmp, data); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := replaceStateFile(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace state: %w", err)
	}
	if err := syncParentDir(s.path); err != nil {
		return fmt.Errorf("sync state dir: %w", err)
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
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
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
			return fmt.Errorf("%v; close state lock %s: %w", err, l.path, closeErr)
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
