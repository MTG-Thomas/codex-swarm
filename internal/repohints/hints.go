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
}

type RemoteDevcontainer struct {
	Command string `json:"command,omitempty"`
	Image   string `json:"image,omitempty"`
	Docs    string `json:"docs,omitempty"`
	Note    string `json:"note,omitempty"`
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
	if h.RemoteDevcontainer == nil {
		return nil
	}
	if strings.TrimSpace(h.RemoteDevcontainer.Command) == "" {
		return errors.New("remote_devcontainer.command is required")
	}
	return nil
}

func (h Hints) Lines() []string {
	lines := []string(nil)
	if h.RemoteDevcontainer == nil {
		return lines
	}
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
