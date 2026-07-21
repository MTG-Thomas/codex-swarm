package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PullRequestStatus is the compact PR stewardship state needed for handoffs.
type PullRequestStatus struct {
	URL               string
	State             string
	BaseBranch        string
	HeadBranch        string
	ReviewDecision    string
	ChecksPassed      int
	ChecksFailed      int
	ChecksPending     int
	CodeRabbitStatus  string
	CodeRabbitPending bool
}

func (s PullRequestStatus) CheckSummary() string {
	parts := []string{
		fmt.Sprintf("pass=%d", s.ChecksPassed),
		fmt.Sprintf("fail=%d", s.ChecksFailed),
		fmt.Sprintf("pending=%d", s.ChecksPending),
	}
	return strings.Join(parts, " ")
}

func (s PullRequestStatus) NextAction() string {
	switch strings.ToUpper(strings.TrimSpace(s.State)) {
	case "MERGED":
		return "complete"
	case "CLOSED":
		return "closed"
	case "OPEN":
		// Continue below.
	default:
		return "unknown"
	}
	if s.ChecksFailed > 0 {
		return "fix-ci"
	}
	if strings.EqualFold(strings.TrimSpace(s.ReviewDecision), "CHANGES_REQUESTED") {
		return "fix-review"
	}
	if s.ChecksPending > 0 || s.CodeRabbitPending {
		return "wait"
	}
	return "merge-ready"
}

// PRStatusProvider fetches PR status from a backing system.
type PRStatusProvider interface {
	PullRequestStatus(context.Context, string) (PullRequestStatus, error)
}

// CLIPRStatusProvider reads pull request status through the GitHub CLI.
type CLIPRStatusProvider struct{}

func (CLIPRStatusProvider) PullRequestStatus(ctx context.Context, url string) (PullRequestStatus, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", url, "--json", "url,state,baseRefName,headRefName,reviewDecision,statusCheckRollup")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return PullRequestStatus{}, fmt.Errorf("read GitHub PR status: %s", message)
	}
	var view prView
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		return PullRequestStatus{}, fmt.Errorf("parse GitHub PR status: %w", err)
	}
	status := PullRequestStatus{
		URL:            firstNonEmpty(view.URL, strings.TrimSpace(url)),
		State:          strings.TrimSpace(view.State),
		BaseBranch:     strings.TrimSpace(view.BaseRefName),
		HeadBranch:     strings.TrimSpace(view.HeadRefName),
		ReviewDecision: strings.TrimSpace(view.ReviewDecision),
	}
	for _, check := range view.StatusCheckRollup {
		name := strings.TrimSpace(firstNonEmpty(check.Name, check.Context))
		if strings.EqualFold(name, "CodeRabbit") {
			status.CodeRabbitStatus = firstNonEmpty(strings.TrimSpace(check.State), strings.TrimSpace(check.Conclusion), strings.TrimSpace(check.Status))
			if strings.EqualFold(status.CodeRabbitStatus, "PENDING") || strings.EqualFold(status.CodeRabbitStatus, "QUEUED") || strings.EqualFold(status.CodeRabbitStatus, "IN_PROGRESS") {
				status.CodeRabbitPending = true
			}
			continue
		}
		switch strings.ToUpper(firstNonEmpty(strings.TrimSpace(check.Conclusion), strings.TrimSpace(check.State), strings.TrimSpace(check.Status))) {
		case "SUCCESS", "PASS", "COMPLETED":
			status.ChecksPassed++
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			status.ChecksFailed++
		default:
			status.ChecksPending++
		}
	}
	return status, nil
}

type prView struct {
	URL               string          `json:"url"`
	State             string          `json:"state"`
	BaseRefName       string          `json:"baseRefName"`
	HeadRefName       string          `json:"headRefName"`
	ReviewDecision    string          `json:"reviewDecision"`
	StatusCheckRollup []prCheckRollup `json:"statusCheckRollup"`
}

type prCheckRollup struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
