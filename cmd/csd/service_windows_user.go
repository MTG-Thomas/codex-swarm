//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/config"
	"github.com/MTG-Thomas/codex-swarm/internal/relay"
)

var runWindowsTask = func(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "schtasks.exe", args...).CombinedOutput()
}

func windowsUserTaskName() (string, string, error) {
	if err := relay.CheckUser(); err != nil {
		return "", "", err
	}
	u, err := user.Current()
	if err != nil {
		return "", "", err
	}
	return serviceName + "-" + u.Uid, u.Uid, nil
}

func installWindowsUserDaemon() error {
	name, sid, err := windowsUserTaskName()
	if err != nil {
		return err
	}
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	cfg.StatePath = envDefault("CODEX_SWARM_STATE", config.DefaultStatePath())
	cfg.Args = cfg.serveArgs()
	args := make([]string, len(cfg.Args))
	for i, arg := range cfg.Args {
		args[i] = syscall.EscapeArg(arg)
	}
	f, err := os.CreateTemp("", "codex-swarm-task-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(userTaskFile(sid, cfg.Executable, strings.Join(args, " ")))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	// No /F: never silently replace an existing task.
	if output, err := runWindowsTask("/Create", "/TN", name, "/XML", f.Name()); err != nil {
		return fmt.Errorf("create user startup task %s: %w: %s", name, err, output)
	}
	fmt.Printf("installed task=%s scope=user (starts at logon; start now with schtasks /Run /TN %s)\n", name, name)
	return nil
}

func uninstallWindowsUserDaemon() error {
	name, _, err := windowsUserTaskName()
	if err != nil {
		return err
	}
	// Delete only this user's named installation; it may already be stopped.
	_, _ = runWindowsTask("/End", "/TN", name)
	if output, err := runWindowsTask("/Delete", "/TN", name, "/F"); err != nil {
		return fmt.Errorf("delete user startup task %s: %w: %s", name, err, output)
	}
	fmt.Printf("uninstalled task=%s scope=user\n", name)
	return nil
}
