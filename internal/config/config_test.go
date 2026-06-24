package config

import (
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
