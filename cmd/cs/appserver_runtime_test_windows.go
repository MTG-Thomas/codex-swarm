//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func verifyDetachedRuntimeConfiguration(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.SysProcAttr == nil {
		t.Fatal("detached runtime must configure Windows process attributes")
	}
	want := uint32(windows.CREATE_BREAKAWAY_FROM_JOB | windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS)
	if got := cmd.SysProcAttr.CreationFlags; got != want {
		t.Fatalf("detached runtime creation flags = %#x, want %#x", got, want)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("detached runtime must hide its Windows process window")
	}
}

func detachedRuntimeStartRestricted(err error) bool {
	// GitHub-hosted Windows runners place tests in a job that expressly denies
	// CREATE_BREAKAWAY_FROM_JOB. Validate the production flags above, but do not
	// mistake that runner policy for an application start failure.
	return os.Getenv("GITHUB_ACTIONS") == "true" && errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
