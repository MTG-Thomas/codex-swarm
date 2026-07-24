package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const serviceName = "codex-swarm-daemon"

type serviceScope uint8

const (
	serviceScopeSystem serviceScope = iota
	serviceScopeUser
)

type serviceConfig struct {
	Name        string
	DisplayName string
	Description string
	Executable  string
	Args        []string
	Addr        string
	StatePath   string
}

func defaultServiceConfig() (serviceConfig, error) {
	exe, err := os.Executable()
	if err != nil {
		return serviceConfig{}, fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return serviceConfig{}, fmt.Errorf("resolve executable path: %w", err)
	}
	cfg := serviceConfig{
		Name:        serviceName,
		DisplayName: "Codex Swarm Daemon",
		Description: "Local Codex Swarm daemon",
		Executable:  exe,
		Addr:        envDefault("CODEX_SWARM_DAEMON_ADDR", "127.0.0.1:8787"),
		StatePath:   envDefault("CODEX_SWARM_STATE", defaultServiceStatePath()),
	}
	cfg.Args = []string{"serve", "--addr", cfg.Addr, "--state", cfg.StatePath}
	return cfg, nil
}

func parseServiceScope(command string, args []string) (serviceScope, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	user := fs.Bool("user", false, "install the service for the current user")
	if err := fs.Parse(args); err != nil {
		return serviceScopeSystem, fmt.Errorf("%s options: %w", command, err)
	}
	if fs.NArg() != 0 {
		return serviceScopeSystem, fmt.Errorf("%s accepts no positional arguments", command)
	}
	if *user {
		return serviceScopeUser, nil
	}
	return serviceScopeSystem, nil
}

func (s serviceScope) String() string {
	if s == serviceScopeUser {
		return "user"
	}
	return "system"
}
