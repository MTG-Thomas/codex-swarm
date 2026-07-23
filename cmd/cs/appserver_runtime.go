package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
)

// csdAppserverRuntime starts a listener-free csd child under the caller's OS
// identity. This is the safe Windows-service fallback: LocalSystem keeps the
// coordination daemon, while the interactive user owns Codex credentials and
// the app-server process.
type csdAppserverRuntime struct {
	binary string
}

func (r csdAppserverRuntime) SpawnAppserver(ctx context.Context, statePath string, request protocol.AppserverSpawnRequest) (protocol.AppserverSpawnResponse, error) {
	if err := ctx.Err(); err != nil {
		return protocol.AppserverSpawnResponse{}, err
	}
	binary := r.binary
	if binary == "" {
		var err error
		binary, err = findCSDRuntimeBinary()
		if err != nil {
			return protocol.AppserverSpawnResponse{}, err
		}
	}
	cmd := newCSDRuntimeCommand(binary, statePath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return protocol.AppserverSpawnResponse{}, fmt.Errorf("open csd runtime stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return protocol.AppserverSpawnResponse{}, fmt.Errorf("open csd runtime stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return protocol.AppserverSpawnResponse{}, fmt.Errorf("start caller-owned csd runtime %s: %w", binary, err)
	}
	if err := json.NewEncoder(stdin).Encode(request); err != nil {
		_ = stdin.Close()
		return protocol.AppserverSpawnResponse{}, fmt.Errorf("send app-server request to caller-owned csd runtime pid=%d: %w", cmd.Process.Pid, err)
	}
	if err := stdin.Close(); err != nil {
		return protocol.AppserverSpawnResponse{}, fmt.Errorf("close caller-owned csd runtime stdin pid=%d: %w", cmd.Process.Pid, err)
	}
	type startResult struct {
		response protocol.AppserverSpawnResponse
		err      error
	}
	started := make(chan startResult, 1)
	go func() {
		var response protocol.AppserverSpawnResponse
		err := json.NewDecoder(stdout).Decode(&response)
		started <- startResult{response: response, err: err}
		// Wait owns pipe cleanup only after the durable identity read has
		// finished. The buffered result lets the CLI return while this goroutine
		// remains as the process reaper for the long-running turn.
		_ = cmd.Wait()
	}()
	select {
	case result := <-started:
		if result.err != nil {
			return protocol.AppserverSpawnResponse{}, fmt.Errorf("read durable identity from caller-owned csd runtime pid=%d: %w", cmd.Process.Pid, result.err)
		}
		return result.response, nil
	case <-ctx.Done():
		// The child deliberately outlives the request. Durable store readback is
		// authoritative before an operator decides whether a retry is safe.
		return protocol.AppserverSpawnResponse{}, ctx.Err()
	}
}

func newCSDRuntimeCommand(binary, statePath string) *exec.Cmd {
	cmd := exec.Command(binary, "appserver-runtime", "--state", statePath)
	// Do not attach the long-running runtime to the CLI's stderr. On Windows,
	// PowerShell and other callers wait for every inherited pipe handle to
	// close, which otherwise keeps `cs spawn` blocked until the first turn ends.
	// Runtime failures are persisted on the worker for durable readback.
	configureDetachedRuntime(cmd)
	return cmd
}

func findCSDRuntimeBinary() (string, error) {
	executable, err := os.Executable()
	if err == nil {
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		candidate := filepath.Join(filepath.Dir(executable), "csd"+ext)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	path, err := exec.LookPath("csd")
	if err != nil {
		return "", fmt.Errorf("find csd for caller-owned app-server runtime: %w", err)
	}
	return path, nil
}

func sameStatePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
