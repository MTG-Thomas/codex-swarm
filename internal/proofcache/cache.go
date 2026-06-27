package proofcache

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

// Query describes the immutable facts a cached gate proof must match.
type Query struct {
	Repo          string
	GateID        string
	Command       string
	Head          string
	NotBefore     time.Time
	RequirePassed bool
}

// Lookup returns the newest reusable proof for the requested gate.
func Lookup(evidence []store.GateEvidence, query Query) (store.GateEvidence, error) {
	gateID := strings.TrimSpace(query.GateID)
	if gateID == "" {
		return store.GateEvidence{}, fmt.Errorf("gate id is required")
	}
	for _, candidate := range evidence {
		if strings.TrimSpace(candidate.GateID) != gateID {
			continue
		}
		if candidate.Repo != "" && !samePath(candidate.Repo, query.Repo) {
			continue
		}
		if command := strings.TrimSpace(query.Command); command != "" && strings.TrimSpace(candidate.Command) != command {
			return store.GateEvidence{}, fmt.Errorf("command mismatch: cached %q does not match required %q", strings.TrimSpace(candidate.Command), command)
		}
		if head := strings.TrimSpace(query.Head); head != "" && strings.TrimSpace(candidate.Commit) != "" && strings.TrimSpace(candidate.Commit) != head {
			return store.GateEvidence{}, fmt.Errorf("HEAD mismatch: cached %s does not match current %s", strings.TrimSpace(candidate.Commit), head)
		}
		if !query.NotBefore.IsZero() && candidate.CreatedAt.Before(query.NotBefore) {
			return store.GateEvidence{}, fmt.Errorf("stale evidence: cached %s is older than required %s", candidate.CreatedAt.Format(time.RFC3339), query.NotBefore.Format(time.RFC3339))
		}
		if query.RequirePassed && candidate.ExitCode != 0 {
			return store.GateEvidence{}, fmt.Errorf("cached command failed with exit code %d", candidate.ExitCode)
		}
		return candidate, nil
	}
	return store.GateEvidence{}, fmt.Errorf("missing evidence")
}

func samePath(a, b string) bool {
	left, leftErr := filepath.Abs(a)
	right, rightErr := filepath.Abs(b)
	if leftErr == nil {
		a = left
	}
	if rightErr == nil {
		b = right
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
