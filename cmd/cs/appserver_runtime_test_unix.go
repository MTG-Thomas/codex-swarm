//go:build !windows

package main

import (
	"os/exec"
	"testing"
)

func verifyDetachedRuntimeConfiguration(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("detached runtime must start in a new session")
	}
}

func detachedRuntimeStartRestricted(error) bool { return false }
