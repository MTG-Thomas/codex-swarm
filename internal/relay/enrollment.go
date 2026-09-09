package relay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

// EnrollmentPolicy grants only current-session enrollment on one host/user.
// It is separate from credentials so installing a receiver never opts in.
type EnrollmentPolicy struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	OSUser  string `json:"os_user"`
}

type SessionStart struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Source    string `json:"source"`
}

func LoadEnrollmentPolicy(path string) (EnrollmentPolicy, error) {
	var p EnrollmentPolicy
	if !filepath.IsAbs(path) {
		return p, fmt.Errorf("enrollment policy path must be absolute")
	}
	f, err := os.Open(path)
	if err != nil {
		return p, fmt.Errorf("open enrollment policy: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return p, fmt.Errorf("enrollment policy must be a regular file no larger than 4 KiB")
	}
	dec := json.NewDecoder(io.LimitReader(f, 4097))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("invalid enrollment policy")
	}
	if dec.Decode(new(any)) != io.EOF {
		return p, fmt.Errorf("enrollment policy must contain one JSON object")
	}
	if !hostPattern.MatchString(p.Host) || p.OSUser == "" {
		return p, fmt.Errorf("enrollment policy requires host and OS user ID")
	}
	return p, nil
}

// DecodeSessionStart deliberately ignores unrelated hook fields. They never
// enter the relay request, local journal, logs or model output.
func DecodeSessionStart(r io.Reader) (SessionStart, error) {
	var e SessionStart
	b, err := io.ReadAll(io.LimitReader(r, 65537))
	if err != nil || len(b) > 65536 {
		return e, fmt.Errorf("hook input must be at most 64 KiB")
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("invalid hook input")
	}
	if e.Event != "SessionStart" || (e.Source != "startup" && e.Source != "resume") || !uuidPattern.MatchString(e.SessionID) {
		return e, fmt.Errorf("expected SessionStart startup/resume with an exact session UUID")
	}
	return e, nil
}

type enrollmentTask struct {
	Host    string `json:"host_id"`
	Thread  string `json:"thread_id"`
	Title   string `json:"title"`
	Enabled int    `json:"enabled"`
}

// AutoEnroll never reverses a local or central explicit revocation. Holding the
// journal lock serializes it with manual registration and receiver polling.
func AutoEnroll(ctx context.Context, c *Client, j *Journal, p EnrollmentPolicy, uid string, e SessionStart) (string, error) {
	if !p.Enabled {
		return "disabled", nil
	}
	if p.Host != c.Host || p.OSUser != uid {
		return "", fmt.Errorf("enrollment policy does not match this host and OS user")
	}
	if e.Event != "SessionStart" || (e.Source != "startup" && e.Source != "resume") || !uuidPattern.MatchString(e.SessionID) {
		return "", fmt.Errorf("invalid enrollment event")
	}
	var enabled bool
	err := j.db.QueryRowContext(ctx, "SELECT enabled FROM enrolled WHERE thread=?", e.SessionID).Scan(&enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && !enabled {
		return "revoked", nil
	}
	// Read all central pages. Neither a 100-row window nor an unrelated task is
	// authority to re-enable a revoked task.
	cursor := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Tasks []enrollmentTask `json:"tasks"`
			Next  string           `json:"next_cursor"`
		}
		if err := c.Call(ctx, "GET", "/v1/tasks?host="+url.QueryEscape(c.Host)+"&after="+url.QueryEscape(cursor), nil, &page); err != nil {
			return "", err
		}
		for _, t := range page.Tasks {
			if t.Host != c.Host {
				return "", fmt.Errorf("enrollment readback returned another host")
			}
			if t.Thread != e.SessionID {
				continue
			}
			if t.Enabled == 0 {
				if err := j.Enroll(ctx, e.SessionID, false); err != nil {
					return "", err
				}
				return "revoked", nil
			}
			if t.Enabled != 1 {
				return "", fmt.Errorf("invalid enrollment enabled state")
			}
			if err := j.Enroll(ctx, e.SessionID, true); err != nil {
				return "", err
			}
			return "enrolled", nil
		}
		if page.Next == "" {
			break
		}
		if !uuidPattern.MatchString(page.Next) || seen[page.Next] {
			return "", fmt.Errorf("invalid enrollment pagination cursor")
		}
		seen[page.Next] = true
		cursor = page.Next
	}
	title := "Codex task " + e.SessionID
	var result struct {
		Host    string `json:"host_id"`
		Thread  string `json:"thread_id"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.Call(ctx, "POST", "/v1/tasks", map[string]any{"thread_id": e.SessionID, "title": title, "enabled": true}, &result); err != nil {
		return "", err
	}
	if result.Host != c.Host || result.Thread != e.SessionID || !result.Enabled {
		return "", fmt.Errorf("enrollment response identity mismatch")
	}
	if err := j.Enroll(ctx, e.SessionID, true); err != nil {
		return "", err
	}
	return "enrolled", nil
}
