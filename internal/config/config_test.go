package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultHome(t *testing.T) {
	got := DefaultHome(filepath.Join("C:", "Users", "Thomas"))
	want := filepath.Join("C:", "Users", "Thomas", ".codex-swarm")
	if got != want {
		t.Fatalf("DefaultHome() = %q, want %q", got, want)
	}
}

func TestStatePathInUsesDatabaseNameForNewLedger(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, StateDatabaseFilename)
	if got := StatePathIn(dir); got != want {
		t.Fatalf("StatePathIn() = %q, want %q", got, want)
	}
}

func TestStatePathInKeepsExistingLegacyLedger(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, LegacyStateFilename)
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := StatePathIn(dir); got != legacy {
		t.Fatalf("StatePathIn() = %q, want legacy path %q", got, legacy)
	}
}

func TestStatePathInPrefersDatabaseWhenBothExist(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, LegacyStateFilename)
	current := filepath.Join(dir, StateDatabaseFilename)
	for _, path := range []string{legacy, current} {
		if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := StatePathIn(dir); got != current {
		t.Fatalf("StatePathIn() = %q, want current path %q", got, current)
	}
}
