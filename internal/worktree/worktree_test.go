package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGitCreateAndStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initial")

	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")
	git := Git{}
	if _, err := git.Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	status, err := git.Status(context.Background(), path)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Dirty {
		t.Fatalf("status dirty = true, lines=%v", status.Lines)
	}

	if err := os.WriteFile(filepath.Join(path, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	status, err = git.Status(context.Background(), path)
	if err != nil {
		t.Fatalf("Status() dirty error = %v", err)
	}
	if !status.Dirty || len(status.Lines) == 0 {
		t.Fatalf("status = %#v, want dirty lines", status)
	}
}

func TestGitCreateRefusesLiveBranchLock(t *testing.T) {
	root := initRepo(t)
	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")
	lockPath := lockFilePath(root, "cs/w-test")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	if err := writeLockFile(lockPath, worktreeLock{
		PID:        os.Getpid(),
		Branch:     "cs/w-test",
		Path:       path,
		AcquiredAt: "2026-06-26T00:00:00Z",
	}); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	_, err := (Git{}).Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path})
	if err == nil {
		t.Fatal("Create() error = nil, want live lock failure")
	}
	if !strings.Contains(err.Error(), "locked by live process") {
		t.Fatalf("Create() error = %v, want live lock message", err)
	}
}

func TestGitCreatePrunesStaleBranchLock(t *testing.T) {
	root := initRepo(t)
	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")
	lockPath := lockFilePath(root, "cs/w-test")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	if err := writeLockFile(lockPath, worktreeLock{
		PID:        0,
		Branch:     "cs/w-test",
		Path:       path,
		AcquiredAt: "2026-06-26T00:00:00Z",
	}); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	result, err := (Git{}).Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.Reused {
		t.Fatalf("Create() reused = true, want new worktree")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after Create(): %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("created worktree .git missing: %v", err)
	}
}

func TestGitCreatePrunesOldMalformedBranchLock(t *testing.T) {
	root := initRepo(t)
	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")
	lockPath := lockFilePath(root, "cs/w-test")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("{"), 0o644); err != nil {
		t.Fatalf("write malformed lock: %v", err)
	}
	old := time.Now().Add(-malformedLockStaleAge - time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("age malformed lock: %v", err)
	}

	if _, err := (Git{}).Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path}); err != nil {
		t.Fatalf("Create() error = %v, want stale malformed lock pruned", err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("created worktree .git missing: %v", err)
	}
}

func TestGitCreateRetriesRecentMalformedBranchLock(t *testing.T) {
	root := initRepo(t)
	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")
	lockPath := lockFilePath(root, "cs/w-test")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("{"), 0o644); err != nil {
		t.Fatalf("write partial lock: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(50 * time.Millisecond)
		_ = writeLockFile(lockPath, worktreeLock{
			PID:        0,
			Branch:     "cs/w-test",
			Path:       path,
			AcquiredAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}()

	if _, err := (Git{}).Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path}); err != nil {
		t.Fatalf("Create() error = %v, want retry then stale lock prune", err)
	}
	<-done
}

func TestSafeLockNameDistinguishesSlashAndUnderscore(t *testing.T) {
	left := safeLockName("cs/a/b")
	right := safeLockName("cs/a_b")

	if left == right {
		t.Fatalf("safeLockName collision: %q", left)
	}
}

func TestProcessSignalErrorAliveTreatsEPERMAsAlive(t *testing.T) {
	if !processSignalErrorAlive(nil) {
		t.Fatal("processSignalErrorAlive(nil) = false, want alive")
	}
	if !processSignalErrorAlive(syscall.EPERM) {
		t.Fatal("processSignalErrorAlive(EPERM) = false, want alive")
	}
	if processSignalErrorAlive(errors.New("not running")) {
		t.Fatal("processSignalErrorAlive(other error) = true, want dead")
	}
}

func TestGitCreateReusesDirtyManagedWorktreeWithWarning(t *testing.T) {
	root := initRepo(t)
	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")
	spec := Spec{RepoRoot: root, Branch: "cs/w-test", Path: path}
	if _, err := (Git{}).Create(context.Background(), spec); err != nil {
		t.Fatalf("initial Create() error = %v", err)
	}
	dirtyPath := filepath.Join(path, "dirty.txt")
	if err := os.WriteFile(dirtyPath, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	result, err := (Git{}).Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("reuse Create() error = %v", err)
	}
	if !result.Reused {
		t.Fatalf("Create() reused = false, want managed reuse")
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "uncommitted changes") {
		t.Fatalf("Create() warnings = %#v, want dirty warning", result.Warnings)
	}
	got, err := os.ReadFile(dirtyPath)
	if err != nil {
		t.Fatalf("dirty file missing after reuse: %v", err)
	}
	if string(got) != "keep me\n" {
		t.Fatalf("dirty file = %q, want preserved contents", got)
	}
}

func TestGitCreateFailsWhenBranchCheckedOutInMainRepo(t *testing.T) {
	root := initRepo(t)
	runGit(t, root, "checkout", "-b", "cs/w-test")
	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")

	_, err := (Git{}).Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path})
	if err == nil {
		t.Fatal("Create() error = nil, want branch-in-main failure")
	}
	if !strings.Contains(err.Error(), "main repository") || !strings.Contains(err.Error(), "cs/w-test") {
		t.Fatalf("Create() error = %v, want clear main repository branch message", err)
	}
}

func TestGitCreateFailsWhenBranchCheckedOutInExternalWorktree(t *testing.T) {
	root := initRepo(t)
	external := filepath.Join(root, "external")
	runGit(t, root, "worktree", "add", "-b", "cs/w-test", external, "HEAD")
	path := filepath.Join(root, ".codex-swarm", "worktrees", "w-test")

	_, err := (Git{}).Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path})
	if err == nil {
		t.Fatal("Create() error = nil, want external worktree failure")
	}
	if !strings.Contains(err.Error(), "external worktree") || !strings.Contains(err.Error(), "cs/w-test") {
		t.Fatalf("Create() error = %v, want clear external worktree branch message", err)
	}
}

func TestPathsEqualHandlesWindowsAndGitSlashes(t *testing.T) {
	windowsPath := `C:\Users\ThomasBray\src\repo\.codex-swarm\worktrees\w-test`
	gitPath := "C:/Users/ThomasBray/src/repo/.codex-swarm/worktrees/w-test"

	if !pathsEqual(windowsPath, gitPath) {
		t.Fatalf("pathsEqual(%q, %q) = false, want true", windowsPath, gitPath)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initial")
	return root
}

func writeLockFile(path string, lock worktreeLock) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(lock); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
