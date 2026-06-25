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
	Workers []Worker `json:"workers"`
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
