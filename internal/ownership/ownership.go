package ownership

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/claims"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

type Severity string

const (
	SeverityOK      Severity = "ok"
	SeverityWarning Severity = "warning"
)

type Check struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	ClaimID  string   `json:"claim_id,omitempty"`
}

type Report struct {
	WorkerID string  `json:"worker_id"`
	Repo     string  `json:"repo,omitempty"`
	Issue    string  `json:"issue,omitempty"`
	Worktree string  `json:"worktree,omitempty"`
	OK       bool    `json:"ok"`
	Checks   []Check `json:"checks"`
}

type Input struct {
	Worker   store.Worker
	Claims   []store.Claim
	Repo     string
	Issue    string
	Worktree string
	Now      time.Time
}

func CheckWorker(input Input) Report {
	report := Report{
		WorkerID: input.Worker.ID,
		Repo:     input.Repo,
		Issue:    input.Issue,
		Worktree: input.Worktree,
		OK:       true,
	}
	add := func(code string, severity Severity, message string, claimID ...string) {
		if severity == SeverityWarning {
			report.OK = false
		}
		check := Check{Code: code, Severity: severity, Message: message}
		if len(claimID) != 0 {
			check.ClaimID = claimID[0]
		}
		report.Checks = append(report.Checks, check)
	}
	if samePath(input.Worker.ProjectRoot, input.Repo) {
		add("repo_match", SeverityOK, "worker repo matches requested repo")
	} else {
		add("repo_mismatch", SeverityWarning, "worker repo does not match requested repo")
	}
	if strings.TrimSpace(input.Issue) != "" {
		if input.Worker.Issue == input.Issue {
			add("issue_match", SeverityOK, "worker issue matches requested issue")
		} else {
			add("issue_mismatch", SeverityWarning, "worker issue does not match requested issue")
		}
	}
	if strings.TrimSpace(input.Worktree) != "" {
		if samePath(input.Worker.Worktree, input.Worktree) {
			add("worktree_match", SeverityOK, "worker worktree matches requested worktree")
		} else {
			add("worktree_mismatch", SeverityWarning, "worker worktree does not match requested worktree")
		}
	} else if strings.TrimSpace(input.Worker.Worktree) == "" {
		add("worktree_missing", SeverityWarning, "worker has no worktree; filesystem isolation is not proven")
	}
	if strings.TrimSpace(input.Worker.ParentID) != "" && strings.TrimSpace(input.Worker.Issue) == "" {
		add("parent_without_issue", SeverityWarning, "child worker has no issue ref to compare with parent scope")
	}
	if input.Worker.Engine == "appserver" && strings.TrimSpace(input.Worker.ThreadID) == "" {
		add("thread_missing", SeverityWarning, "appserver worker has no thread id")
	}
	for _, claim := range input.Claims {
		if !claims.IsOpen(claim, input.Now.UTC()) {
			continue
		}
		if claim.WorkerID == input.Worker.ID {
			continue
		}
		if samePath(claim.Repo, input.Repo) || (input.Issue != "" && claim.Issue == input.Issue) {
			add("active_claim_warning", SeverityWarning, "another active claim exists in the requested repo or issue scope", claim.ID)
		}
	}
	return report
}

func samePath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	return filepath.Clean(strings.ToLower(left)) == filepath.Clean(strings.ToLower(right))
}
