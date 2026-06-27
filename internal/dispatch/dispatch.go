package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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
	gates := canonicalGates(input.Gates)
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

func canonicalGates(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				seen[part] = true
			}
		}
	}
	gates := make([]string, 0, len(seen))
	for gate := range seen {
		gates = append(gates, gate)
	}
	sort.Strings(gates)
	return gates
}

func requestID(issue, repo, prompt string, gates []string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(issue) + "\x00" + strings.TrimSpace(repo) + "\x00" + strings.TrimSpace(prompt) + "\x00" + strings.Join(gates, ",")))
	return "dispatch-" + hex.EncodeToString(sum[:])[:16]
}
