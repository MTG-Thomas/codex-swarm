//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func configureDetachedRuntime(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
