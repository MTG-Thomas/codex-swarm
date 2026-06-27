package repohints

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	CommittedFile = "codex-swarm.hints.json"
	LocalFile     = ".codex-swarm/repo-hints.json"
)

type Hints struct {
	RemoteDevcontainer *RemoteDevcontainer `json:"remote_devcontainer,omitempty"`
	Commands           []CommandHint       `json:"commands,omitempty"`
	QualityGates       []QualityGate       `json:"quality_gates,omitempty"`
}

type RemoteDevcontainer struct {
	Command string `json:"command,omitempty"`
	Image   string `json:"image,omitempty"`
	Docs    string `json:"docs,omitempty"`
	Note    string `json:"note,omitempty"`
}

type CommandHint struct {
	Name    string `json:"name,omitempty"`
	Command string `json:"command,omitempty"`
	Docs    string `json:"docs,omitempty"`
	Note    string `json:"note,omitempty"`
}

type QualityGate struct {
	ID          string `json:"id,omitempty"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

type Source struct {
	Path  string
	Local bool
}

func Load(repoRoot string) (Hints, Source, bool, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return Hints{}, Source{}, false, fmt.Errorf("resolve repo root: %w", err)
	}
	candidates := []Source{
		{Path: filepath.Join(root, CommittedFile)},
		{Path: filepath.Join(root, filepath.FromSlash(LocalFile)), Local: true},
	}
	for _, source := range candidates {
		data, err := os.ReadFile(source.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return Hints{}, Source{}, false, fmt.Errorf("read repo hints %s: %w", source.Path, err)
		}
		var hints Hints
		if err := json.Unmarshal(data, &hints); err != nil {
			return Hints{}, Source{}, false, fmt.Errorf("parse repo hints %s: %w", source.Path, err)
		}
		if err := hints.Validate(); err != nil {
			return Hints{}, Source{}, false, fmt.Errorf("validate repo hints %s: %w", source.Path, err)
		}
		return hints, source, true, nil
	}
	return Hints{}, Source{}, false, nil
}

func (h Hints) Validate() error {
	if h.RemoteDevcontainer != nil && strings.TrimSpace(h.RemoteDevcontainer.Command) == "" {
		return errors.New("remote_devcontainer.command is required")
	}
	for i, command := range h.Commands {
		if strings.TrimSpace(command.Command) == "" {
			return fmt.Errorf("commands[%d].command is required", i)
		}
	}
	seenGateIDs := map[string]bool{}
	for i, gate := range h.QualityGates {
		id := strings.TrimSpace(gate.ID)
		if id == "" {
			return fmt.Errorf("quality_gates[%d].id is required", i)
		}
		if strings.TrimSpace(gate.Command) == "" {
			return fmt.Errorf("quality_gates[%d].command is required", i)
		}
		if seenGateIDs[id] {
			return fmt.Errorf("quality_gates[%d].id %q is duplicated", i, id)
		}
		seenGateIDs[id] = true
	}
	return nil
}

func (h Hints) Lines() []string {
	lines := []string(nil)
	if h.RemoteDevcontainer != nil {
		remote := *h.RemoteDevcontainer
		lines = append(lines, "repo hint: remote devcontainer command: "+strings.TrimSpace(remote.Command))
		if image := strings.TrimSpace(remote.Image); image != "" {
			lines = append(lines, "repo hint: remote devcontainer image: "+image)
			if looksMutableImageTag(image) {
				lines = append(lines, "repo hint: prefer immutable image tags for proof-sensitive remote execution")
			}
		}
		if docs := strings.TrimSpace(remote.Docs); docs != "" {
			lines = append(lines, "repo hint: docs: "+docs)
		}
		if note := strings.TrimSpace(remote.Note); note != "" {
			lines = append(lines, "repo hint: "+note)
		}
	}
	for _, command := range h.Commands {
		name := strings.TrimSpace(command.Name)
		prefix := "repo hint: command"
		if name != "" {
			prefix += " " + name
		}
		lines = append(lines, prefix+": "+strings.TrimSpace(command.Command))
		if docs := strings.TrimSpace(command.Docs); docs != "" {
			lines = append(lines, "repo hint: docs: "+docs)
		}
		if note := strings.TrimSpace(command.Note); note != "" {
			lines = append(lines, "repo hint: "+note)
		}
	}
	for _, gate := range h.QualityGates {
		id := strings.TrimSpace(gate.ID)
		lines = append(lines, "repo hint: quality gate "+id+": "+strings.TrimSpace(gate.Command))
		if scope := strings.TrimSpace(gate.Scope); scope != "" {
			lines = append(lines, "repo hint: quality gate "+id+" scope: "+scope)
		}
		if description := strings.TrimSpace(gate.Description); description != "" {
			lines = append(lines, "repo hint: quality gate "+id+" description: "+description)
		}
	}
	return lines
}

func looksMutableImageTag(image string) bool {
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon <= lastSlash {
		return false
	}
	tag := strings.ToLower(strings.TrimSpace(image[lastColon+1:]))
	return tag == "latest" || tag == "main" || strings.HasSuffix(tag, "-main") || strings.Contains(tag, "-main-")
}
