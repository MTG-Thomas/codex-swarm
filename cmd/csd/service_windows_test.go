//go:build windows

package main

import (
	"path/filepath"
	"testing"
)

func TestWindowsServiceDefaultStatePathUsesProgramData(t *testing.T) {
	t.Setenv("ProgramData", `C:\ProgramData`)
	got := defaultServiceStatePath()
	want := filepath.Join(`C:\ProgramData`, "codex-swarm", "state.json")
	if got != want {
		t.Fatalf("defaultServiceStatePath() = %q, want %q", got, want)
	}
}

func TestWindowsServiceServeOptionsDefaultToProgramData(t *testing.T) {
	t.Setenv("ProgramData", `C:\ProgramData`)
	addr, state, err := serveOptionsWithDefaultState(nil, defaultServiceStatePath())
	if err != nil {
		t.Fatalf("serveOptionsWithDefaultState() error = %v", err)
	}
	if addr != "127.0.0.1:8787" {
		t.Fatalf("addr = %q, want default daemon address", addr)
	}
	want := filepath.Join(`C:\ProgramData`, "codex-swarm", "state.json")
	if state != want {
		t.Fatalf("state = %q, want %q", state, want)
	}
}
