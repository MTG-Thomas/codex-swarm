package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	if err := git.Create(context.Background(), Spec{RepoRoot: root, Branch: "cs/w-test", Path: path}); err != nil {
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
