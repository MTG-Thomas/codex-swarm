//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateLockRejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	const contents = "do not replace"
	if err := os.WriteFile(victim, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "state.db")
	if err := os.Symlink(victim, statePath+".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireStateLock(statePath); err == nil || !strings.Contains(err.Error(), "open state lock") {
		t.Fatalf("acquireStateLock() error = %v, want safe open failure", err)
	}
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents {
		t.Fatalf("victim contents = %q, want %q", got, contents)
	}
}

func TestStateLockRejectsHardLinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	const contents = "do not replace"
	if err := os.WriteFile(victim, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "state.db")
	if err := os.Link(victim, statePath+".lock"); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireStateLock(statePath); err == nil || !strings.Contains(err.Error(), "hard links") {
		t.Fatalf("acquireStateLock() error = %v, want hard-link rejection", err)
	}
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents {
		t.Fatalf("victim contents = %q, want %q", got, contents)
	}
}
