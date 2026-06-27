package main

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/repohints"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) gate(args []string) error {
	if len(args) == 0 {
		return errors.New("gate requires <list|record>")
	}
	switch args[0] {
	case "list":
		return c.gateList(args[1:])
	case "record":
		return c.gateRecord(args[1:])
	default:
		return fmt.Errorf("unknown gate command %q", args[0])
	}
}

func (c cli) gateList(args []string) error {
	fs := c.flagSet("gate list")
	repo := fs.String("repo", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	hints, source, ok, err := repohints.Load(repoRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "repo=%s\n", repoRoot)
	if !ok {
		fmt.Fprintf(c.out, "gates=0\nchecked=%s,%s\n", repohints.CommittedFile, repohints.LocalFile)
		return nil
	}
	fmt.Fprintf(c.out, "gates=%d source=%s local=%t\n", len(hints.QualityGates), source.Path, source.Local)
	for _, gate := range hints.QualityGates {
		fmt.Fprintf(c.out, "%s\t%s\t%s\t%s\n", strings.TrimSpace(gate.ID), emptyDash(strings.TrimSpace(gate.Scope)), strings.TrimSpace(gate.Command), emptyDash(strings.TrimSpace(gate.Description)))
	}
	return nil
}

func (c cli) gateRecord(args []string) error {
	fs := c.flagSet("gate record")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	workerID := fs.String("worker", "", "worker that produced the evidence")
	gateID := fs.String("gate", "", "quality gate id")
	command := fs.String("command", "", "command that was evaluated")
	scope := fs.String("scope", "", "evidence scope")
	exitCode := fs.Int("exit-code", 0, "observed exit code")
	output := fs.String("output", "", "short evidence output or proof summary")
	commit := fs.String("commit", "", "git commit that evidence applies to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*gateID) == "" {
		return errors.New("gate record requires --gate")
	}
	if *exitCode < 0 {
		return errors.New("gate record requires --exit-code >= 0")
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}
	resolvedGate, err := resolveQualityGate(repoRoot, *gateID)
	if err != nil {
		return err
	}
	recordCommand := strings.TrimSpace(*command)
	if recordCommand == "" && resolvedGate != nil {
		recordCommand = strings.TrimSpace(resolvedGate.Command)
	}
	if recordCommand == "" {
		return errors.New("gate record requires --command when --gate is not configured in repo hints")
	}
	recordScope := strings.TrimSpace(*scope)
	if recordScope == "" && resolvedGate != nil {
		recordScope = strings.TrimSpace(resolvedGate.Scope)
	}
	recordCommit := strings.TrimSpace(*commit)
	if recordCommit == "" {
		recordCommit = bestEffortGitCommit(repoRoot)
	}

	now := c.now().UTC()
	evidenceID, err := newGateEvidenceID(now)
	if err != nil {
		return err
	}
	s := store.NewJSONStore(*statePath)
	workerIDValue := strings.TrimSpace(*workerID)
	if workerIDValue != "" {
		if _, err := s.GetWorker(workerIDValue); err != nil {
			if errors.Is(err, store.ErrWorkerNotFound) {
				return fmt.Errorf("worker %q not found", workerIDValue)
			}
			return err
		}
	}
	evidence := store.GateEvidence{
		ID:        evidenceID,
		GateID:    strings.TrimSpace(*gateID),
		WorkerID:  workerIDValue,
		Repo:      repoRoot,
		Scope:     recordScope,
		Command:   recordCommand,
		ExitCode:  *exitCode,
		Output:    strings.TrimSpace(*output),
		Commit:    recordCommit,
		CreatedAt: now,
	}
	if err := s.SaveGateEvidence(evidence); err != nil {
		return err
	}
	if evidence.WorkerID != "" {
		if _, err := s.UpdateWorker(evidence.WorkerID, func(worker *store.Worker) error {
			worker.Events = append(worker.Events, store.Event{
				At:       now,
				Type:     "quality.gate",
				Message:  fmt.Sprintf("gate=%s exit=%d command=%s", evidence.GateID, evidence.ExitCode, evidence.Command),
				WorkerID: evidence.WorkerID,
			})
			worker.UpdatedAt = now
			return nil
		}); err != nil {
			return err
		}
	}

	fmt.Fprintf(c.out, "gate evidence %s gate=%s exit=%d repo=%s\n", evidence.ID, evidence.GateID, evidence.ExitCode, evidence.Repo)
	if evidence.Commit != "" {
		fmt.Fprintf(c.out, "commit=%s\n", evidence.Commit)
	}
	return nil
}

func resolveQualityGate(repoRoot, id string) (*repohints.QualityGate, error) {
	hints, _, ok, err := repohints.Load(repoRoot)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	id = strings.TrimSpace(id)
	for _, gate := range hints.QualityGates {
		if strings.TrimSpace(gate.ID) == id {
			gateCopy := gate
			return &gateCopy, nil
		}
	}
	return nil, nil
}

func bestEffortGitCommit(repoRoot string) string {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func newGateEvidenceID(now time.Time) (string, error) {
	suffix, err := randomSuffix(4)
	if err != nil {
		return "", fmt.Errorf("generate gate evidence id: %w", err)
	}
	return fmt.Sprintf("g-%s-%s", now.UTC().Format("20060102-150405"), suffix), nil
}
