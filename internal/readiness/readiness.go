package readiness

import (
	"fmt"
	"strconv"
	"strings"
)

type Input struct {
	Issue  Issue
	Repo   string
	Gates  []Gate
	Claims []Claim
}

type Issue struct {
	Ref   string `json:"ref"`
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

type Gate struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Scope   string `json:"scope,omitempty"`
}

type Claim struct {
	ID       string `json:"id"`
	WorkerID string `json:"worker_id,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Status   string `json:"status"`
}

type Report struct {
	Ready    bool     `json:"ready"`
	Issue    Issue    `json:"issue"`
	Repo     string   `json:"repo"`
	Gates    []Gate   `json:"gates,omitempty"`
	Claims   []Claim  `json:"claims,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
}

func Evaluate(input Input) Report {
	report := Report{
		Issue:  input.Issue,
		Repo:   strings.TrimSpace(input.Repo),
		Gates:  append([]Gate(nil), input.Gates...),
		Claims: append([]Claim(nil), input.Claims...),
	}

	if err := validateIssueRef(input.Issue.Ref); err != nil {
		report.Blockers = append(report.Blockers, err.Error())
	}
	if strings.TrimSpace(input.Issue.Title) == "" {
		report.Blockers = append(report.Blockers, "issue title is missing")
	}
	if strings.TrimSpace(input.Issue.Body) == "" {
		report.Blockers = append(report.Blockers, "issue body is missing")
	}
	if report.Repo == "" {
		report.Blockers = append(report.Blockers, "repo path is missing")
	}
	if len(input.Gates) == 0 {
		report.Blockers = append(report.Blockers, "no quality gates configured")
	}
	for _, claim := range input.Claims {
		if isOpenClaim(claim) {
			report.Blockers = append(report.Blockers, "issue has open claim "+strings.TrimSpace(claim.ID))
		}
	}
	report.Ready = len(report.Blockers) == 0
	return report
}

func isOpenClaim(claim Claim) bool {
	switch strings.ToLower(strings.TrimSpace(claim.Status)) {
	case "active", "blocked":
		return true
	default:
		return false
	}
}

func validateIssueRef(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("issue reference is required")
	}
	hash := strings.LastIndex(value, "#")
	if hash < 0 {
		return fmt.Errorf("issue reference must look like owner/repo#123")
	}
	repoPart := value[:hash]
	numberPart := value[hash+1:]
	pieces := strings.Split(repoPart, "/")
	if len(pieces) != 2 || pieces[0] == "" || pieces[1] == "" {
		return fmt.Errorf("issue reference must look like owner/repo#123")
	}
	number, err := strconv.Atoi(numberPart)
	if err != nil || number <= 0 {
		return fmt.Errorf("issue number must be a positive integer")
	}
	return nil
}
