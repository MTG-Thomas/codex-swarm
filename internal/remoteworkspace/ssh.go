package remoteworkspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

// Spec describes one isolated Git workspace on a remote host.
type Spec struct {
	WorkerID string `json:"worker_id"`
	RepoURL  string `json:"repo_url"`
	BaseRef  string `json:"base_ref"`
	Branch   string `json:"branch"`
	Root     string `json:"root,omitempty"`
	GitName  string `json:"git_name,omitempty"`
	GitEmail string `json:"git_email,omitempty"`
}

// Result is the remote workspace identity returned by the provider.
type Result struct {
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	RepoURL string `json:"repo_url"`
	BaseRef string `json:"base_ref"`
}

// Runner executes an SSH process. It is injectable so tests do not require a
// live host.
type Runner interface {
	Run(ctx context.Context, binary string, args []string, stdin []byte) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, binary string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("ssh remote workspace: %s", message)
	}
	return stdout.Bytes(), nil
}

var endpointPattern = regexp.MustCompile(`^[A-Za-z0-9._:@-]+$`)

// SSH prepares isolated Git worktrees through OpenSSH. The remote host owns
// Git credentials; codex-swarm never copies or persists them.
type SSH struct {
	Binary string
	Target string
	Jump   string
	Runner Runner
}

func (s SSH) Prepare(ctx context.Context, spec Spec) (Result, error) {
	if !endpointPattern.MatchString(s.Target) {
		return Result{}, fmt.Errorf("invalid SSH target %q", s.Target)
	}
	if s.Jump != "" && !endpointPattern.MatchString(s.Jump) {
		return Result{}, fmt.Errorf("invalid SSH jump host %q", s.Jump)
	}
	if strings.TrimSpace(spec.WorkerID) == "" || strings.TrimSpace(spec.RepoURL) == "" || strings.TrimSpace(spec.Branch) == "" {
		return Result{}, fmt.Errorf("remote workspace requires worker id, repository URL, and branch")
	}
	if strings.ContainsAny(spec.RepoURL, "\x00\r\n") {
		return Result{}, errors.New("remote repository URL contains control characters")
	}
	if parsed, err := url.Parse(spec.RepoURL); err == nil && parsed.User != nil {
		return Result{}, errors.New("remote repository URL must not contain credentials")
	}
	if spec.BaseRef == "" {
		spec.BaseRef = "main"
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return Result{}, fmt.Errorf("encode remote workspace request: %w", err)
	}
	script := strings.Replace(remoteWorkspaceScript, "REQUEST_BASE64", base64.StdEncoding.EncodeToString(payload), 1)
	args := []string{"-o", "BatchMode=yes"}
	if s.Jump != "" {
		args = append(args, "-J", s.Jump)
	}
	args = append(args, s.Target, "--", "python3", "-")
	binary := s.Binary
	if binary == "" {
		binary = "ssh"
	}
	runner := s.Runner
	if runner == nil {
		runner = commandRunner{}
	}
	out, err := runner.Run(ctx, binary, args, []byte(script))
	if err != nil {
		return Result{}, fmt.Errorf("prepare remote workspace worker=%s repo=%s: %w", spec.WorkerID, spec.RepoURL, err)
	}
	var result Result
	if err := json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
		return Result{}, fmt.Errorf("decode remote workspace result worker=%s: %w", spec.WorkerID, err)
	}
	if result.Path == "" || result.Branch != spec.Branch {
		return Result{}, fmt.Errorf("remote workspace returned inconsistent identity for worker=%s", spec.WorkerID)
	}
	return result, nil
}

const remoteWorkspaceScript = `
import base64
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

spec = json.loads(base64.b64decode("REQUEST_BASE64"))
worker_id = spec["worker_id"]
branch = spec["branch"]
if not re.fullmatch(r"w-[A-Za-z0-9-]+", worker_id):
    raise SystemExit("invalid worker id")
if not re.fullmatch(r"cs/w-[A-Za-z0-9-]+", branch):
    raise SystemExit("invalid managed branch")

home = Path.home().resolve()
root = Path(spec.get("root") or home / ".local/share/codex-swarm").expanduser().resolve()
if root != home and home not in root.parents:
    raise SystemExit("remote workspace root must remain under the remote home directory")

repo_url = spec["repo_url"]
base_ref = spec.get("base_ref") or "main"
if base_ref.startswith("refs/heads/"):
    base_ref = base_ref[len("refs/heads/"):]
if base_ref.startswith("origin/"):
    base_ref = base_ref[len("origin/"):]
if base_ref.startswith("-"):
    raise SystemExit("invalid base branch")
repo_key = hashlib.sha256(repo_url.encode()).hexdigest()[:20]
mirror = root / "mirrors" / (repo_key + ".git")
workspace = root / "workspaces" / worker_id
lock_path = root / "locks" / (repo_key + ".lock")
for directory in (mirror.parent, workspace.parent, lock_path.parent):
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)

def run(args, cwd=None):
    subprocess.run(args, cwd=cwd, check=True, stdout=subprocess.DEVNULL)

with lock_path.open("a+") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    run(["git", "check-ref-format", "--branch", base_ref])
    if not mirror.exists():
        run(["git", "clone", "--mirror", repo_url, str(mirror)])
    else:
        run(["git", "--git-dir", str(mirror), "remote", "set-url", "origin", repo_url])
        run(["git", "--git-dir", str(mirror), "fetch", "--prune", "origin"])
    if workspace.exists():
        raise SystemExit("remote workspace already exists: " + str(workspace))
    run(["git", "clone", "--reference-if-able", str(mirror), "--no-checkout", repo_url, str(workspace)])
    checkout_ref = "refs/remotes/origin/" + base_ref
    run(["git", "checkout", "-b", branch, checkout_ref], cwd=workspace)
    if spec.get("git_name"):
        run(["git", "config", "user.name", spec["git_name"]], cwd=workspace)
    if spec.get("git_email"):
        run(["git", "config", "user.email", spec["git_email"]], cwd=workspace)

print(json.dumps({
    "path": str(workspace),
    "branch": branch,
    "repo_url": repo_url,
    "base_ref": base_ref,
}))
`
