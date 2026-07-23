package config

import (
	"os"
	"path/filepath"
)

const (
	StateDatabaseFilename = "state.db"
	LegacyStateFilename   = "state.json"
)

type Config struct {
	Home string
}

func DefaultHome(userHome string) string {
	return filepath.Join(userHome, ".codex-swarm")
}

// DefaultStatePath returns the machine-global ledger path for the current user.
// Existing state.json ledgers remain selected until an operator migrates them;
// new installations and already-migrated installations use state.db.
func DefaultStatePath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return StatePathIn(filepath.Join(dir, "codex-swarm"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return StatePathIn(DefaultHome(home))
	}
	return StatePathIn(".codex-swarm")
}

// StatePathIn resolves the active ledger in dir without mutating either file.
// The SQLite-named database wins when both paths exist.
func StatePathIn(dir string) string {
	current := filepath.Join(dir, StateDatabaseFilename)
	if pathExists(current) {
		return current
	}
	legacy := filepath.Join(dir, LegacyStateFilename)
	if pathExists(legacy) {
		return legacy
	}
	return current
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}
