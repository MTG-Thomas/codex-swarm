package repohints

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCommittedHints(t *testing.T) {
	repo := t.TempDir()
	body := `{
  "remote_devcontainer": {
    "command": "just talos-dev-run \"just --list\"",
    "image": "ghcr.io/mtg-thomas/bifrost-devcontainer:devcontainer-main-abc123",
    "docs": "docs/devcontainer.md",
    "note": "No secrets are injected by default."
  }
}`
	if err := os.WriteFile(filepath.Join(repo, CommittedFile), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	hints, source, ok, err := Load(repo)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("Load() ok = false")
	}
	if source.Local {
		t.Fatal("source.Local = true, want committed source")
	}
	if hints.RemoteDevcontainer == nil || hints.RemoteDevcontainer.Command == "" {
		t.Fatalf("RemoteDevcontainer = %#v", hints.RemoteDevcontainer)
	}
	lines := strings.Join(hints.Lines(), "\n")
	for _, want := range []string{
		`just talos-dev-run "just --list"`,
		"devcontainer-main-abc123",
		"prefer immutable image tags",
		"No secrets are injected",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("Lines() missing %q:\n%s", want, lines)
		}
	}
}

func TestLoadLocalHintsWhenCommittedMissing(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, filepath.FromSlash(LocalFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"remote_devcontainer":{"command":"just remote"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	hints, source, ok, err := Load(repo)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok || !source.Local {
		t.Fatalf("Load() ok=%t source=%#v, want local source", ok, source)
	}
	if got := hints.RemoteDevcontainer.Command; got != "just remote" {
		t.Fatalf("Command = %q, want just remote", got)
	}
}

func TestLoadRejectsIncompleteRemoteDevcontainerHint(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, CommittedFile), []byte(`{"remote_devcontainer":{"image":"image:tag"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, _, _, err := Load(repo)
	if err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "remote_devcontainer.command is required") {
		t.Fatalf("Load() error = %v", err)
	}
}
