package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Spec struct {
	RepoRoot string
	Branch   string
	Path     string
}

type Status struct {
	Path  string
	Dirty bool
	Lines []string
}

type Git struct {
	Binary string
}

func (g Git) Create(ctx context.Context, spec Spec) error {
	if spec.RepoRoot == "" {
		return fmt.Errorf("repo root is required")
	}
	if spec.Branch == "" {
		return fmt.Errorf("branch is required")
	}
	if spec.Path == "" {
		return fmt.Errorf("worktree path is required")
	}
	if _, err := os.Stat(spec.Path); err == nil {
		return fmt.Errorf("worktree path already exists: %s", spec.Path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o755); err != nil {
		return fmt.Errorf("create worktree parent: %w", err)
	}
	_, err := g.run(ctx, spec.RepoRoot, "worktree", "add", "-b", spec.Branch, spec.Path, "HEAD")
	if err != nil {
		return err
	}
	return nil
}

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
