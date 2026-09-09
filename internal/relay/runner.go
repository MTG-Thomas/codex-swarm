package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
	"time"
)

type Mailbox interface {
	Ready(context.Context) (bool, error)
	Inspect(context.Context, string) (*Message, error)
	Claim(context.Context, string) (*Message, error)
	Report(context.Context, Receipt) error
}
type Submitter interface {
	Submit(context.Context, string, string) (string, error)
}
type Runner struct {
	Host    string
	Journal *Journal
	Mailbox Mailbox
	Queue   Submitter
}

// Once never automatically repeats a Codex submission after it might have
// started. Network errors retain the exact claim/receipt request for replay.
func (r Runner) Once(ctx context.Context) error {
	e, err := r.Journal.next(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		ready, readyErr := r.Mailbox.Ready(ctx)
		if readyErr != nil {
			return readyErr
		}
		if !ready {
			return nil
		}
		e = entry{ID: NewID(), Phase: "claiming"}
		err = r.Journal.save(ctx, e)
	}
	if err != nil {
		return err
	}
	if e.Phase == "claiming" {
		e.Message, err = r.Mailbox.Claim(ctx, e.ID)
		if err != nil {
			return err
		}
		if e.Message == nil {
			e.Phase = "done"
			return r.Journal.save(ctx, e)
		}
		m := e.Message
		if m.TargetHost != r.Host || m.AttemptID != e.ID || !uuidPattern.MatchString(m.ThreadID) || m.ID == "" || len(m.Prompt) > 16384 {
			return fmt.Errorf("relay claim identity mismatch; inspect attempt %s", e.ID)
		}
		e.Phase = "ready"
		if err = r.Journal.save(ctx, e); err != nil {
			return err
		}
	}
	if e.Phase == "dispatching" {
		e.Phase = "reporting"
		e.Receipt = Receipt{RequestID: NewID(), MessageID: e.Message.ID, State: "uncertain", Evidence: "host restarted after submission began; inspect destination before any resend"}
		if err = r.Journal.save(ctx, e); err != nil {
			return err
		}
	}
	if e.Phase == "ready" {
		current, inspectErr := r.Mailbox.Inspect(ctx, e.Message.ID)
		if inspectErr != nil {
			return inspectErr
		}
		if current == nil || current.ID != e.Message.ID || current.ThreadID != e.Message.ThreadID || current.TargetHost != r.Host || current.AttemptID != e.ID || current.Prompt != e.Message.Prompt {
			return fmt.Errorf("relay message changed identity; inspect attempt %s", e.ID)
		}
		if current.State != "dispatching" {
			switch current.State {
			case "submitted", "acknowledged", "completed", "uncertain":
				e.Phase = "done"
				return r.Journal.save(ctx, e)
			default:
				return fmt.Errorf("unexpected relay state for attempt %s", e.ID)
			}
		}

		e.Receipt = Receipt{RequestID: NewID(), MessageID: e.Message.ID, State: "uncertain"}
		if !r.Journal.allowed(ctx, e.Message.ThreadID) {
			e.Receipt.Evidence = "destination task is not enabled in this host's local enrollment"
		} else {
			e.Phase = "dispatching"
			if err = r.Journal.save(ctx, e); err != nil {
				return err
			}
			submission, submitErr := r.Queue.Submit(ctx, e.Message.ThreadID, e.Message.Prompt)
			if submitErr == nil && uuidPattern.MatchString(submission) {
				e.Receipt.State = "submitted"
				e.Receipt.SubmissionID = submission
			} else {
				e.Receipt.Evidence = "Codex submission outcome uncertain; inspect destination and local process status before any resend"
			}
		}
		e.Phase = "reporting"
		if err = r.Journal.save(ctx, e); err != nil {
			return err
		}
	}
	if e.Phase == "reporting" {
		if err = r.Mailbox.Report(ctx, e.Receipt); err != nil {
			return err
		}
		e.Phase = "done"
		return r.Journal.save(ctx, e)
	}
	return nil
}

// CodexQueue uses the supported CLI and never starts/resumes a competing thread.
type CodexQueue struct{ Binary string }

func CheckUser() error {
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("verify relay OS identity: %w", err)
	}
	n := strings.ToLower(u.Username)
	if u.Uid == "0" || u.Uid == "S-1-5-18" || n == "root" || n == "system" || strings.HasSuffix(n, `\system`) {
		return fmt.Errorf("relay delivery must run as the Codex task owner, not root/SYSTEM")
	}
	return nil
}

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 8192 - len(b.data); remaining > 0 {
		b.data = append(b.data, p[:min(remaining, len(p))]...)
	}
	return n, nil
}
func (q CodexQueue) command(ctx context.Context, args ...string) (string, error) {
	if err := CheckUser(); err != nil {
		return "", err
	}
	binary := q.Binary
	if binary == "" {
		binary = "codex"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 2 * time.Second
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("Codex queue command failed: %w", err)
	}
	return string(out.data), nil
}
func (q CodexQueue) Check(ctx context.Context) error {
	output, err := q.command(ctx, "queue", "--help")
	if err != nil {
		return err
	}
	if !strings.Contains(output, "--thread") || !strings.Contains(output, "--message") {
		return fmt.Errorf("installed Codex does not support queue --thread --message")
	}
	return nil
}

var receiptPattern = regexp.MustCompile(`(?m)^Queued message ([0-9a-f-]{36}) for thread ([0-9a-f-]{36})\.[\r]?$`)

func (q CodexQueue) Submit(ctx context.Context, thread, prompt string) (string, error) {
	if !uuidPattern.MatchString(thread) || prompt == "" || len(prompt) > 16384 {
		return "", fmt.Errorf("invalid queue task or prompt")
	}
	output, err := q.command(ctx, "queue", "--thread", thread, "--message", prompt)
	if err != nil {
		return "", err
	}
	m := receiptPattern.FindStringSubmatch(output)
	if len(m) != 3 || m[2] != thread || !uuidPattern.MatchString(m[1]) {
		return "", fmt.Errorf("Codex queue returned no matching submission receipt")
	}
	return m[1], nil
}
