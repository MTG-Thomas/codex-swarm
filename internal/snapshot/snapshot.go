package snapshot

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const SchemaVersion = "codex-swarm.worker-snapshot.v1"

type Input struct {
	Worker       store.Worker
	Claims       []store.Claim
	GateEvidence []store.GateEvidence
	GeneratedAt  time.Time
}

type Snapshot struct {
	Schema       string    `json:"schema"`
	Worker       Worker    `json:"worker"`
	Claims       []Claim   `json:"claims,omitempty"`
	Gates        []Gate    `json:"gates,omitempty"`
	Report       string    `json:"report,omitempty"`
	LastMessage  string    `json:"last_message,omitempty"`
	RecentEvents []Event   `json:"recent_events,omitempty"`
	GeneratedAt  time.Time `json:"generated_at,omitempty"`
}

type Worker struct {
	ID               string `json:"id"`
	Role             string `json:"role,omitempty"`
	Status           string `json:"status"`
	Issue            string `json:"issue,omitempty"`
	ValidationOf     string `json:"validation_of,omitempty"`
	ValidationStatus string `json:"validation_status,omitempty"`
	Repo             string `json:"repo,omitempty"`
	Worktree         string `json:"worktree,omitempty"`
	Branch           string `json:"branch,omitempty"`
	ThreadID         string `json:"thread_id,omitempty"`
	Engine           string `json:"engine,omitempty"`
}

type Claim struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Scope    string `json:"scope,omitempty"`
	Issue    string `json:"issue,omitempty"`
	WorkerID string `json:"worker_id,omitempty"`
}

type Gate struct {
	ID        string    `json:"id"`
	ExitCode  int       `json:"exit_code"`
	Command   string    `json:"command,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type Event struct {
	At      time.Time `json:"at,omitempty"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
}

func Build(input Input) Snapshot {
	worker := input.Worker
	worktree, branch := truthfulCheckout(worker)
	return Snapshot{
		Schema: SchemaVersion,
		Worker: Worker{
			ID:               worker.ID,
			Role:             strings.TrimSpace(worker.Role),
			Status:           displayStatus(worker),
			Issue:            strings.TrimSpace(worker.Issue),
			ValidationOf:     strings.TrimSpace(worker.ValidationOf),
			ValidationStatus: strings.TrimSpace(worker.ValidationStatus),
			Repo:             strings.TrimSpace(worker.ProjectRoot),
			Worktree:         worktree,
			Branch:           branch,
			ThreadID:         strings.TrimSpace(worker.ThreadID),
			Engine:           strings.TrimSpace(worker.Engine),
		},
		Claims:       relevantClaims(worker, input.Claims),
		Gates:        relevantGateEvidence(worker, input.GateEvidence),
		Report:       strings.TrimSpace(worker.Report),
		LastMessage:  strings.TrimSpace(worker.LastMessage),
		RecentEvents: recentEvents(worker.Events, 5),
		GeneratedAt:  input.GeneratedAt.UTC(),
	}
}

func truthfulCheckout(worker store.Worker) (string, string) {
	for _, event := range worker.Events {
		if event.Type == "worktree.created" {
			return strings.TrimSpace(worker.Worktree), strings.TrimSpace(worker.Branch)
		}
	}
	return "", ""
}

func (s Snapshot) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "STATE_SNAPSHOT worker=%s status=%s", s.Worker.ID, s.Worker.Status)
	if s.Worker.Role != "" {
		fmt.Fprintf(&b, " role=%s", s.Worker.Role)
	}
	if s.Worker.Issue != "" {
		fmt.Fprintf(&b, " issue=%s", s.Worker.Issue)
	}
	b.WriteByte('\n')
	if s.Worker.Repo != "" {
		fmt.Fprintf(&b, "repo=%s\n", s.Worker.Repo)
	}
	if s.Worker.Worktree != "" {
		fmt.Fprintf(&b, "worktree=%s\n", s.Worker.Worktree)
	}
	if s.Worker.Branch != "" {
		fmt.Fprintf(&b, "branch=%s\n", s.Worker.Branch)
	}
	if s.Worker.ThreadID != "" {
		fmt.Fprintf(&b, "thread=%s\n", s.Worker.ThreadID)
	}
	for _, claim := range s.Claims {
		fmt.Fprintf(&b, "claim=%s status=%s", claim.ID, claim.Status)
		if claim.Scope != "" {
			fmt.Fprintf(&b, " scope=%s", claim.Scope)
		}
		if claim.Issue != "" && claim.Issue != s.Worker.Issue {
			fmt.Fprintf(&b, " issue=%s", claim.Issue)
		}
		b.WriteByte('\n')
	}
	for _, gate := range s.Gates {
		fmt.Fprintf(&b, "gate=%s exit=%d", gate.ID, gate.ExitCode)
		if gate.Commit != "" {
			fmt.Fprintf(&b, " commit=%s", gate.Commit)
		}
		if gate.Command != "" {
			fmt.Fprintf(&b, " command=%s", gate.Command)
		}
		b.WriteByte('\n')
	}
	if s.Report != "" {
		fmt.Fprintf(&b, "report=%s\n", s.Report)
	}
	if s.LastMessage != "" {
		fmt.Fprintf(&b, "last=%s\n", s.LastMessage)
	}
	for _, event := range s.RecentEvents {
		fmt.Fprintf(&b, "event=%s %s\n", event.Type, event.Message)
	}
	return strings.TrimRight(b.String(), "\n")
}

func relevantClaims(worker store.Worker, all []store.Claim) []Claim {
	claims := []Claim(nil)
	for _, claim := range all {
		if claim.WorkerID != worker.ID && (worker.Issue == "" || claim.Issue != worker.Issue) {
			continue
		}
		claims = append(claims, Claim{
			ID:       strings.TrimSpace(claim.ID),
			Status:   string(claim.Status),
			Scope:    strings.TrimSpace(claim.Scope),
			Issue:    strings.TrimSpace(claim.Issue),
			WorkerID: strings.TrimSpace(claim.WorkerID),
		})
	}
	sort.SliceStable(claims, func(i, j int) bool { return claims[i].ID < claims[j].ID })
	return claims
}

func relevantGateEvidence(worker store.Worker, all []store.GateEvidence) []Gate {
	gates := []Gate(nil)
	for _, gate := range all {
		if gate.WorkerID != worker.ID {
			continue
		}
		gates = append(gates, Gate{
			ID:        strings.TrimSpace(gate.GateID),
			ExitCode:  gate.ExitCode,
			Command:   strings.TrimSpace(gate.Command),
			Commit:    strings.TrimSpace(gate.Commit),
			CreatedAt: gate.CreatedAt,
		})
	}
	sort.SliceStable(gates, func(i, j int) bool {
		if gates[i].ID == gates[j].ID {
			return gates[i].CreatedAt.After(gates[j].CreatedAt)
		}
		return gates[i].ID < gates[j].ID
	})
	return gates
}

func recentEvents(all []store.Event, limit int) []Event {
	if limit <= 0 || len(all) == 0 {
		return nil
	}
	start := len(all) - limit
	if start < 0 {
		start = 0
	}
	events := make([]Event, 0, len(all)-start)
	for _, event := range all[start:] {
		events = append(events, Event{
			At:      event.At,
			Type:    strings.TrimSpace(event.Type),
			Message: strings.TrimSpace(event.Message),
			From:    strings.TrimSpace(event.From),
			To:      strings.TrimSpace(event.To),
		})
	}
	return events
}

func displayStatus(worker store.Worker) string {
	if worker.Lifecycle != nil {
		return string(worker.Lifecycle.DeriveStatus())
	}
	return string(worker.Status)
}

func samePath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
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
