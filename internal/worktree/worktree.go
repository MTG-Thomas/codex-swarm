package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	slashpath "path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	malformedLockRetryInterval = 25 * time.Millisecond
	malformedLockStaleAge      = 30 * time.Second
	privateDirPerm             = 0o700
	privateFilePerm            = 0o600
)

// Spec describes a managed Git worktree request.
type Spec struct {
	RepoRoot string
	Branch   string
	Path     string
}

// Status is the porcelain status summary for a worktree.
type Status struct {
	Path  string
	Dirty bool
	Lines []string
}

// Result describes the outcome of creating or reusing a managed worktree.
type Result struct {
	Path     string
	Branch   string
	Reused   bool
	Warnings []string
}

// Git wraps the git executable used for managed worktree operations.
type Git struct {
	Binary string
}

// Create creates or reuses a managed worktree for the requested branch.
func (g Git) Create(ctx context.Context, spec Spec) (Result, error) {
	if spec.RepoRoot == "" {
		return Result{}, fmt.Errorf("repo root is required")
	}
	if spec.Branch == "" {
		return Result{}, fmt.Errorf("branch is required")
	}
	if spec.Path == "" {
		return Result{}, fmt.Errorf("worktree path is required")
	}
	repoRoot, err := filepath.Abs(spec.RepoRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve repo root: %w", err)
	}
	worktreePath, err := filepath.Abs(spec.Path)
	if err != nil {
		return Result{}, fmt.Errorf("resolve worktree path: %w", err)
	}
	spec = Spec{RepoRoot: repoRoot, Branch: spec.Branch, Path: worktreePath}

	lock, err := acquireLock(ctx, spec.RepoRoot, spec.Branch, spec.Path)
	if err != nil {
		return Result{}, err
	}
	defer lock.release()

	worktrees, err := g.worktrees(ctx, spec.RepoRoot)
	if err != nil {
		return Result{}, err
	}
	if conflict, ok := findBranchWorktree(worktrees, spec.Branch); ok && !pathsEqual(conflict.Path, spec.Path) {
		if pathsEqual(conflict.Path, spec.RepoRoot) {
			return Result{}, fmt.Errorf("branch %q is already checked out in the main repository at %s", spec.Branch, conflict.Path)
		}
		return Result{}, fmt.Errorf("branch %q is already checked out in external worktree at %s", spec.Branch, conflict.Path)
	}

	if _, err := os.Stat(spec.Path); err == nil {
		if conflict, ok := findBranchWorktree(worktrees, spec.Branch); ok && pathsEqual(conflict.Path, spec.Path) {
			status, err := g.Status(ctx, spec.Path)
			if err != nil {
				return Result{}, fmt.Errorf("inspect managed worktree status: %w", err)
			}
			result := Result{Path: spec.Path, Branch: spec.Branch, Reused: true}
			if status.Dirty {
				result.Warnings = append(result.Warnings, fmt.Sprintf("managed worktree %s has uncommitted changes; reusing without refresh", spec.Path))
			}
			return result, nil
		}
		return Result{}, fmt.Errorf("worktree path already exists but is not a managed checkout for branch %q: %s", spec.Branch, spec.Path)
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), privateDirPerm); err != nil {
		return Result{}, fmt.Errorf("create worktree parent: %w", err)
	}
	_, err = g.run(ctx, spec.RepoRoot, "worktree", "add", "-b", spec.Branch, spec.Path, "HEAD")
	if err != nil {
		return Result{}, err
	}
	return Result{Path: spec.Path, Branch: spec.Branch}, nil
}

// Status returns whether a worktree has uncommitted porcelain changes.
func (g Git) Status(ctx context.Context, path string) (Status, error) {
	if path == "" {
		return Status{}, fmt.Errorf("worktree path is required")
	}
	out, err := g.run(ctx, path, "status", "--porcelain")
	if err != nil {
		return Status{}, err
	}
	lines := compactLines(out)
	return Status{
		Path:  path,
		Dirty: len(lines) > 0,
		Lines: lines,
	}, nil
}

func (g Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	binary := g.Binary
	if binary == "" {
		binary = "git"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return stdout.String(), nil
}

func compactLines(value string) []string {
	raw := strings.Split(value, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

type worktreeLock struct {
	PID        int    `json:"pid"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
	AcquiredAt string `json:"acquired_at"`
}

type heldLock struct {
	path string
}

func acquireLock(ctx context.Context, repoRoot, branch, worktreePath string) (heldLock, error) {
	lockPath := lockFilePath(repoRoot, branch)
	if err := os.MkdirAll(filepath.Dir(lockPath), privateDirPerm); err != nil {
		return heldLock{}, fmt.Errorf("create worktree lock dir: %w", err)
	}
	lock := worktreeLock{
		PID:        os.Getpid(),
		Branch:     branch,
		Path:       worktreePath,
		AcquiredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	for {
		select {
		case <-ctx.Done():
			return heldLock{}, ctx.Err()
		default:
		}
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFilePerm)
		if err == nil {
			if err := json.NewEncoder(file).Encode(lock); err != nil {
				_ = file.Close()
				_ = os.Remove(lockPath)
				return heldLock{}, fmt.Errorf("write worktree lock: %w", err)
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(lockPath)
				return heldLock{}, fmt.Errorf("close worktree lock: %w", err)
			}
			return heldLock{path: lockPath}, nil
		}
		if !os.IsExist(err) {
			return heldLock{}, fmt.Errorf("create worktree lock: %w", err)
		}
		existing, readErr := readLockFile(lockPath)
		if readErr != nil {
			stale, statErr := unreadableLockStale(lockPath, time.Now())
			if statErr != nil {
				if os.IsNotExist(statErr) {
					continue
				}
				return heldLock{}, fmt.Errorf("inspect unreadable worktree lock %s: %w", lockPath, statErr)
			}
			if !stale {
				if err := sleepWithContext(ctx, malformedLockRetryInterval); err != nil {
					return heldLock{}, err
				}
				continue
			}
			if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
				return heldLock{}, fmt.Errorf("prune unreadable worktree lock %s: %w", lockPath, err)
			}
			continue
		}
		if processAlive(existing.PID) {
			return heldLock{}, fmt.Errorf("worktree branch %q is locked by live process pid %d at %s", branch, existing.PID, existing.Path)
		}
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			return heldLock{}, fmt.Errorf("prune stale worktree lock %s: %w", lockPath, err)
		}
	}
}

func (l heldLock) release() {
	if l.path != "" {
		_ = os.Remove(l.path)
	}
}

func lockFilePath(repoRoot, branch string) string {
	return filepath.Join(repoRoot, ".codex-swarm", "locks", safeLockName(branch)+".lock")
}

func safeLockName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		b.WriteString("branch")
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s-%x", b.String(), sum[:6])
}

func readLockFile(path string) (lock worktreeLock, err error) {
	file, err := os.Open(path)
	if err != nil {
		return worktreeLock{}, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err := json.NewDecoder(file).Decode(&lock); err != nil {
		return worktreeLock{}, err
	}
	return lock, nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func unreadableLockStale(path string, now time.Time) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return now.Sub(info.ModTime()) >= malformedLockStaleAge, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if pid == os.Getpid() {
		return true
	}
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
		if err != nil {
			return true
		}
		return strings.Contains(string(out), fmt.Sprintf("%d", pid))
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return processSignalErrorAlive(process.Signal(syscall.Signal(0)))
}

func processSignalErrorAlive(err error) bool {
	return err == nil || errors.Is(err, syscall.EPERM)
}

type worktreeInfo struct {
	Path   string
	Branch string
}

func (g Git) worktrees(ctx context.Context, repoRoot string) ([]worktreeInfo, error) {
	out, err := g.run(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	records := strings.Split(strings.TrimSpace(out), "\n\n")
	worktrees := make([]worktreeInfo, 0, len(records))
	for _, record := range records {
		var info worktreeInfo
		for _, line := range strings.Split(record, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "worktree "):
				info.Path = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			case strings.HasPrefix(line, "branch "):
				info.Branch = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, "branch ")), "refs/heads/")
			}
		}
		if info.Path != "" {
			worktrees = append(worktrees, info)
		}
	}
	return worktrees, nil
}

func findBranchWorktree(worktrees []worktreeInfo, branch string) (worktreeInfo, bool) {
	for _, worktree := range worktrees {
		if worktree.Branch == branch {
			return worktree, true
		}
	}
	return worktreeInfo{}, false
}

func pathsEqual(left, right string) bool {
	return comparablePath(left) == comparablePath(right)
}

func comparablePath(value string) string {
	value = strings.TrimSpace(value)
	if evaluated, err := filepath.EvalSymlinks(value); err == nil {
		value = evaluated
	}
	value = strings.ReplaceAll(value, "\\", "/")
	value = slashpath.Clean(value)
	value = strings.TrimRight(value, "/")
	if runtime.GOOS == "windows" || looksLikeWindowsPath(value) {
		value = strings.ToLower(value)
	}
	return value
}

func looksLikeWindowsPath(value string) bool {
	return len(value) >= 2 && value[1] == ':'
}
