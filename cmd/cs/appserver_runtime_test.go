package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
)

func TestConfigureDetachedRuntimeStarts(t *testing.T) {
	if os.Getenv("CODEX_SWARM_DETACHED_HELPER") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestConfigureDetachedRuntimeStarts$")
	cmd.Env = append(os.Environ(), "CODEX_SWARM_DETACHED_HELPER=1")
	configureDetachedRuntime(cmd)
	verifyDetachedRuntimeConfiguration(t, cmd)
	if err := cmd.Start(); err != nil {
		if detachedRuntimeStartRestricted(err) {
			t.Skipf("runner job forbids CREATE_BREAKAWAY_FROM_JOB: %v", err)
		}
		t.Fatalf("start detached runtime helper: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait detached runtime helper: %v", err)
	}
}

func TestCallerRuntimeKeepsPromptOffCommandLine(t *testing.T) {
	secretPrompt := "operator prompt must stay on stdin"
	request := protocol.AppserverSpawnRequest{RequestID: "appserver-spawn-worker", WorkerID: "worker", Prompt: secretPrompt}
	cmd := newCSDRuntimeCommand("csd", `C:\state\state.json`)
	for _, arg := range cmd.Args {
		if arg == request.Prompt {
			t.Fatalf("prompt leaked into runtime argument %q", arg)
		}
	}
}

func TestCallerRuntimeDoesNotInheritParentStderr(t *testing.T) {
	cmd := newCSDRuntimeCommand("csd", `C:\state\state.json`)
	if cmd.Stderr != nil {
		t.Fatal("detached runtime must not inherit the caller's stderr pipe")
	}
}

func TestSameStatePath(t *testing.T) {
	root := t.TempDir()
	left := filepath.Join(root, "state.json")
	right := filepath.Join(root, ".", "state.json")
	if !sameStatePath(left, right) {
		t.Fatalf("sameStatePath(%q, %q) = false", left, right)
	}
	other := filepath.Join(t.TempDir(), "state.json")
	if sameStatePath(left, other) {
		t.Fatalf("sameStatePath(%q, %q) = true", left, other)
	}
	if runtime.GOOS == "windows" && !sameStatePath(filepath.ToSlash(left), left) {
		t.Fatalf("sameStatePath should tolerate Windows separator/case normalization")
	}
}
