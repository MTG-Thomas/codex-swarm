package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
)

type Input struct {
	Readiness readiness.Report
	Prompt    string
	Gates     []string
}

type PlanResult struct {
	RequestID string
	Issue     string
	Repo      string
	Prompt    string
	Gates     []string
	Workers   []WorkerPlan
}

type WorkerPlan struct {
	Role             string
	ValidationOfRole string
}

func Plan(input Input) (PlanResult, error) {
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return PlanResult{}, errors.New("dispatch prompt is required")
	}
	if !input.Readiness.Ready {
		return PlanResult{}, fmt.Errorf("issue is not ready: %s", strings.Join(input.Readiness.Blockers, "; "))
	}
	gates := cleanGates(input.Gates)
	result := PlanResult{
		RequestID: requestID(input.Readiness.Issue.Ref, input.Readiness.Repo, prompt, gates),
		Issue:     strings.TrimSpace(input.Readiness.Issue.Ref),
		Repo:      strings.TrimSpace(input.Readiness.Repo),
		Prompt:    prompt,
		Gates:     gates,
		Workers: []WorkerPlan{
			{Role: "implementer"},
			{Role: "validator", ValidationOfRole: "implementer"},
		},
	}
	return result, nil
}

func cleanGates(values []string) []string {
	var gates []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				gates = append(gates, part)
			}
		}
	}
	return gates
}

func requestID(issue, repo, prompt string, gates []string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(issue) + "\x00" + strings.TrimSpace(repo) + "\x00" + strings.TrimSpace(prompt) + "\x00" + strings.Join(gates, ",")))
	return "dispatch-" + hex.EncodeToString(sum[:])[:16]
}
