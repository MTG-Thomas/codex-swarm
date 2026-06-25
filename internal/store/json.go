package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

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
	s.mu.Lock()
	defer s.mu.Unlock()

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
}

func (s *JSONStore) GetWorker(id string) (Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return Worker{}, err
	}
	for _, worker := range state.Workers {
		if worker.ID == id {
			return worker, nil
		}
	}
	return Worker{}, ErrWorkerNotFound
}

func (s *JSONStore) ListWorkers() ([]Worker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return nil, err
	}
	workers := append([]Worker(nil), state.Workers...)
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].UpdatedAt.After(workers[j].UpdatedAt)
	})
	return workers, nil
}

func (s *JSONStore) SaveSchedule(schedule Schedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
}

func (s *JSONStore) ListSchedules() ([]Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return nil, err
	}
	schedules := append([]Schedule(nil), state.Schedules...)
	sort.Slice(schedules, func(i, j int) bool {
		return schedules[i].UpdatedAt.After(schedules[j].UpdatedAt)
	})
	return schedules, nil
}

func (s *JSONStore) SaveClaim(claim Claim) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
}

func (s *JSONStore) GetClaim(id string) (Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return Claim{}, err
	}
	for _, claim := range state.Claims {
		if claim.ID == id {
			return claim, nil
		}
	}
	return Claim{}, ErrClaimNotFound
}

func (s *JSONStore) ListClaims() ([]Claim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return nil, err
	}
	claims := append([]Claim(nil), state.Claims...)
	sort.Slice(claims, func(i, j int) bool {
		return claims[i].UpdatedAt.After(claims[j].UpdatedAt)
	})
	return claims, nil
}

func (s *JSONStore) SaveAgent(agent Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
}

func (s *JSONStore) GetAgent(id string) (Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return Agent{}, err
	}
	for _, agent := range state.Agents {
		if agent.ID == id {
			return agent, nil
		}
	}
	return Agent{}, ErrAgentNotFound
}

func (s *JSONStore) CurrentAgent() (Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return Agent{}, err
	}
	for _, agent := range state.Agents {
		if agent.Current {
			return agent, nil
		}
	}
	return Agent{}, ErrAgentNotFound
}

func (s *JSONStore) ListAgents() ([]Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.read()
	if err != nil {
		return nil, err
	}
	agents := append([]Agent(nil), state.Agents...)
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].UpdatedAt.After(agents[j].UpdatedAt)
	})
	return agents, nil
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
	return state, nil
}

func (s *JSONStore) write(state stateFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	tmp := fmt.Sprintf("%s.%d.tmp", s.path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return fmt.Errorf("remove old state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}
