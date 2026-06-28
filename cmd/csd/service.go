package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const serviceName = "codex-swarm-daemon"

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
