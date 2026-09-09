//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsServiceDefaultStatePathUsesProgramData(t *testing.T) {
	programData := t.TempDir()
	t.Setenv("ProgramData", programData)
	got := defaultServiceStatePath()
	want := filepath.Join(programData, "codex-swarm", "state.db")
	if got != want {
		t.Fatalf("defaultServiceStatePath() = %q, want %q", got, want)
	}
}

func TestWindowsServiceServeOptionsDefaultToProgramData(t *testing.T) {
	programData := t.TempDir()
	t.Setenv("ProgramData", programData)
	addr, state, err := serveOptionsWithDefaultState(nil, defaultServiceStatePath())
	if err != nil {
		t.Fatalf("serveOptionsWithDefaultState() error = %v", err)
	}
	if addr != "127.0.0.1:8787" {
		t.Fatalf("addr = %q, want default daemon address", addr)
	}
	want := filepath.Join(programData, "codex-swarm", "state.db")
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

func TestWindowsUserInstallPersistsConfigWithoutStarting(t *testing.T) {
	t.Setenv("CODEX_SWARM_RELAY_CONFIG", filepath.Join(t.TempDir(), "private.json"))
	t.Setenv("CODEX_SWARM_STATE", filepath.Join(t.TempDir(), "existing.db"))
	original := runWindowsTask
	defer func() { runWindowsTask = original }()
	calls := 0
	runWindowsTask = func(args ...string) ([]byte, error) {
		calls++
		if len(args) != 5 || args[0] != "/Create" || args[3] != "/XML" {
			t.Fatalf("unexpected invocation %q", args)
		}
		data, err := os.ReadFile(args[4])
		if err != nil {
			t.Fatal(err)
		}
		text := decodeTaskFile(t, data)
		if !strings.Contains(text, "--relay-config") || !strings.Contains(text, "existing.db") || strings.Contains(text, "LocalSystem") {
			t.Fatalf("incorrect task definition")
		}
		return nil, nil
	}
	if err := installService([]string{"--user"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
