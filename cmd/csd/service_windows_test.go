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

func TestWindowsServiceArgsPreferPersistedCommandLine(t *testing.T) {
	processArgs := []string{"serve", "--state", `C:\Users\ThomasBray\AppData\Roaming\codex-swarm\state.json`}
	startArgs := []string{"--state", `C:\ProgramData\codex-swarm\state.json`}

	got := windowsServiceArgs(processArgs, startArgs)
	if len(got) != len(processArgs) {
		t.Fatalf("windowsServiceArgs() = %q, want %q", got, processArgs)
	}
	for i := range processArgs {
		if got[i] != processArgs[i] {
			t.Fatalf("windowsServiceArgs()[%d] = %q, want %q", i, got[i], processArgs[i])
		}
	}
}

func TestWindowsServiceArgsFallBackToStartArguments(t *testing.T) {
	startArgs := []string{"--state", `C:\ProgramData\codex-swarm\state.json`}
	got := windowsServiceArgs(nil, startArgs)
	if len(got) != len(startArgs) {
		t.Fatalf("windowsServiceArgs() = %q, want %q", got, startArgs)
	}
	for i := range startArgs {
		if got[i] != startArgs[i] {
			t.Fatalf("windowsServiceArgs()[%d] = %q, want %q", i, got[i], startArgs[i])
		}
	}
}
