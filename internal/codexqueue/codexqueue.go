package codexqueue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Minimum Codex CLI version that provides `codex queue`.
const (
	MinMajor = 0
	MinMinor = 149
	MinPatch = 0
)

// MinVersion is the operator-facing minimum Codex CLI version.
const MinVersion = "0.149.0"

// MaxPromptBytes bounds the prompt handed to `codex queue`, matching the
// relay queue adapter so one envelope cannot exhaust process arguments.
const MaxPromptBytes = 16384

var (
	uuidPattern         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	versionPattern      = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
	receiptPattern      = regexp.MustCompile(`(?m)^Queued message ([0-9a-f-]{36}) for thread ([0-9a-f-]{36})\.[\r]?$`)
	sshEndpointPattern  = regexp.MustCompile(`^[A-Za-z0-9._:@-]+$`)
	remoteBinaryPattern = regexp.MustCompile(`^[A-Za-z0-9._/+~-]+$`)
)

// UnsupportedError reports a Codex CLI that predates `codex queue`. It fails
// before delivery so callers leave durable state untouched.
type UnsupportedError struct {
	Found string
}

func (e *UnsupportedError) Error() string {
	found := strings.TrimSpace(e.Found)
	if found == "" {
		found = "unknown version"
	}
	return fmt.Sprintf("installed Codex CLI %s does not support queue delivery; upgrade to Codex CLI %s or newer", found, MinVersion)
}

// AmbiguousError reports a submission whose outcome is unknown: the prompt
// may or may not have been queued. Callers must not retry automatically and
// must leave durable state untouched until an operator inspects the
// destination thread.
type AmbiguousError struct {
	Op  string
	Err error
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s outcome uncertain: %v; inspect the destination thread before any resend", e.Op, e.Err)
}

func (e *AmbiguousError) Unwrap() error { return e.Err }

// Remote identifies the recorded SSH transport for a worker whose Codex host
// is not local. It carries no credentials; authentication and host-key policy
// stay with the operator's SSH configuration.
type Remote struct {
	Target      string
	Jump        string
	CodexBinary string
}

// Config selects the local binary or the recorded remote transport.
type Config struct {
	CodexBinary string
	SSHBinary   string
	Remote      *Remote
}

// Result is the bounded process output returned by a Runner.
type Result struct {
	Stdout string
	Stderr string
}

// Runner executes one process without a shell. Implementations must pass args
// as argv boundaries and must surface context cancellation in the returned
// error so Submit can classify ambiguous outcomes.
type Runner func(ctx context.Context, name string, args []string, stdin []byte) (Result, error)

// ExecRunner is the production Runner. Output is bounded so a chatty binary
// cannot exhaust memory; diagnostics stay in the returned error.
func ExecRunner(ctx context.Context, name string, args []string, stdin []byte) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr boundedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{Stdout: string(stdout.data), Stderr: string(stderr.data)}, err
}

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 8192 - len(b.data); remaining > 0 {
		b.data = append(b.data, p[:min(remaining, len(p))]...)
	}
	return n, nil
}

// ParseVersion extracts the first major.minor.patch triple from `codex
// --version` output such as "codex-cli 0.157.1".
func ParseVersion(output string) (major, minor, patch int, found string, err error) {
	m := versionPattern.FindStringSubmatch(output)
	if len(m) != 4 {
		return 0, 0, 0, "", fmt.Errorf("could not determine installed Codex CLI version from %q; need %s or newer", short(output), MinVersion)
	}
	_, _ = fmt.Sscanf(m[1], "%d", &major)
	_, _ = fmt.Sscanf(m[2], "%d", &minor)
	_, _ = fmt.Sscanf(m[3], "%d", &patch)
	return major, minor, patch, m[0], nil
}

// CheckVersion fails with UnsupportedError when the `--version` output
// predates `codex queue`.
func CheckVersion(output string) error {
	major, minor, patch, found, err := ParseVersion(output)
	if err != nil {
		return &UnsupportedError{}
	}
	if major > MinMajor {
		return nil
	}
	if major < MinMajor || minor < MinMinor || (minor == MinMinor && patch < MinPatch) {
		return &UnsupportedError{Found: found}
	}
	return nil
}

// ValidThread reports whether thread is an exact Codex thread UUID. Queue
// delivery never resolves mutable session names.
func ValidThread(thread string) bool { return uuidPattern.MatchString(thread) }

// Submit delivers one prompt to an existing Codex thread by exact UUID,
// either locally or through the recorded SSH transport. The prompt is never
// interpolated into a shell command: locally it travels as argv, remotely it
// travels over SSH stdin into `xargs -0`, which reconstitutes the exact argv
// on the far side.
//
// A nil error means Codex durably accepted the message; the returned string
// carries the submission receipt when the CLI printed one. A non-nil error
// from the queue invocation means definite failure. An AmbiguousError means
// the outcome is unknown and the caller must not retry automatically.
func Submit(ctx context.Context, run Runner, cfg Config, thread, prompt string) (string, error) {
	if !ValidThread(thread) {
		return "", fmt.Errorf("refuse codex queue delivery: thread %q is not an exact thread UUID", thread)
	}
	if strings.TrimSpace(prompt) == "" || len(prompt) > MaxPromptBytes {
		return "", fmt.Errorf("refuse codex queue delivery: prompt must be 1-%d bytes", MaxPromptBytes)
	}
	if strings.ContainsRune(prompt, 0) {
		return "", fmt.Errorf("refuse codex queue delivery: prompt contains a NUL byte")
	}
	if run == nil {
		run = ExecRunner
	}
	codexBinary := strings.TrimSpace(cfg.CodexBinary)
	if codexBinary == "" {
		codexBinary = "codex"
	}

	if cfg.Remote == nil {
		if err := checkVersion(ctx, run, codexBinary, []string{"--version"}, nil); err != nil {
			return "", err
		}
		result, err := run(ctx, codexBinary, []string{"queue", "--thread", thread, "--message", prompt}, nil)
		if err != nil {
			return "", classifySubmitError(ctx, result, err)
		}
		return matchReceipt(result.Stdout, thread), nil
	}

	remote := cfg.Remote
	if !sshEndpointPattern.MatchString(remote.Target) {
		return "", fmt.Errorf("invalid SSH target %q", remote.Target)
	}
	if remote.Jump != "" && !sshEndpointPattern.MatchString(remote.Jump) {
		return "", fmt.Errorf("invalid SSH jump host %q", remote.Jump)
	}
	remoteBinary := strings.TrimSpace(remote.CodexBinary)
	if remoteBinary == "" {
		remoteBinary = "codex"
	}
	if remoteBinary == "" || !remoteBinaryPattern.MatchString(remoteBinary) {
		return "", fmt.Errorf("invalid remote Codex binary %q", remote.CodexBinary)
	}
	sshBinary := strings.TrimSpace(cfg.SSHBinary)
	if sshBinary == "" {
		sshBinary = "ssh"
	}
	base := sshBaseArgs(remote.Target, remote.Jump)
	versionArgs := append(append([]string{}, base...), "--", remoteBinary, "--version")
	if err := checkVersion(ctx, run, sshBinary, versionArgs, nil); err != nil {
		return "", err
	}
	queueArgs := append(append([]string{}, base...), "--", "xargs", "-0", remoteBinary, "queue", "--thread", thread, "--message")
	result, err := run(ctx, sshBinary, queueArgs, append([]byte(prompt), 0))
	if err != nil {
		return "", classifySubmitError(ctx, result, err)
	}
	return matchReceipt(result.Stdout, thread), nil
}

func sshBaseArgs(target, jump string) []string {
	args := []string{"-o", "BatchMode=yes"}
	if jump != "" {
		args = append(args, "-J", jump)
	}
	return append(args, target)
}

func checkVersion(ctx context.Context, run Runner, name string, args []string, stdin []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := run(ctx, name, args, stdin)
	if err != nil {
		if ctx.Err() != nil {
			return &AmbiguousError{Op: "codex version check", Err: err}
		}
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = err.Error()
		}
		return &UnsupportedError{Found: detail}
	}
	return CheckVersion(result.Stdout)
}

// classifySubmitError distinguishes definite queue failures, which callers
// record, from ambiguous outcomes, which callers must leave untouched.
func classifySubmitError(ctx context.Context, result Result, err error) error {
	if ctx.Err() != nil {
		return &AmbiguousError{Op: "codex queue submission", Err: err}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("codex queue delivery failed: %s", short(detail))
	}
	return &AmbiguousError{Op: "codex queue submission", Err: err}
}

func matchReceipt(stdout, thread string) string {
	m := receiptPattern.FindStringSubmatch(stdout)
	if len(m) != 3 || m[2] != thread || !uuidPattern.MatchString(m[1]) {
		return ""
	}
	return m[1]
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500] + "..."
	}
	if s == "" {
		return "(empty)"
	}
	return s
}
