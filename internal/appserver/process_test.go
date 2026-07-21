package appserver

import (
	"context"
	"reflect"
	"testing"
)

func TestLocalProcessUsesWorkspaceAsCommandDirectory(t *testing.T) {
	cmd, err := (LocalProcess{Binary: "codex-test"}).Command(context.Background(), "/workspace/task")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != "codex-test" {
		t.Fatalf("path = %q, want codex-test", cmd.Path)
	}
	if cmd.Dir != "/workspace/task" {
		t.Fatalf("dir = %q, want /workspace/task", cmd.Dir)
	}
}

func TestSSHProcessBuildsArgumentSafeTransport(t *testing.T) {
	cmd, err := (SSHProcess{
		Binary:      "ssh-test",
		Target:      "thomas@remote.example",
		Jump:        "root@jump.example",
		CodexBinary: "/home/thomas/.local/bin/codex",
	}).Command(context.Background(), "/remote/worktree")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ssh-test", "-o", "BatchMode=yes", "-J", "root@jump.example",
		"thomas@remote.example", "--", "/home/thomas/.local/bin/codex", "app-server",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args = %#v, want %#v", cmd.Args, want)
	}
	if cmd.Dir != "" {
		t.Fatalf("local command dir = %q, want empty", cmd.Dir)
	}
}

func TestSSHProcessRejectsShellSyntax(t *testing.T) {
	cases := []SSHProcess{
		{Target: "host; touch /tmp/pwn"},
		{Target: "host", Jump: "jump$(id)"},
		{Target: "host", CodexBinary: "codex;id"},
	}
	for _, process := range cases {
		if _, err := process.Command(context.Background(), "/workspace"); err == nil {
			t.Fatalf("Command(%+v) error = nil", process)
		}
	}
}
