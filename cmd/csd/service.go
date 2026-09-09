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
	RelayConfig string
	LogFile     string
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
	cfg.RelayConfig = os.Getenv("CODEX_SWARM_RELAY_CONFIG")
	if cfg.RelayConfig != "" && !filepath.IsAbs(cfg.RelayConfig) {
		return serviceConfig{}, fmt.Errorf("CODEX_SWARM_RELAY_CONFIG must be absolute")
	}
	cfg.LogFile = os.Getenv("CODEX_SWARM_LOG_FILE")
	if cfg.LogFile != "" && !filepath.IsAbs(cfg.LogFile) {
		return serviceConfig{}, fmt.Errorf("CODEX_SWARM_LOG_FILE must be absolute")
	}
	cfg.Args = cfg.serveArgs()
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

func (c serviceConfig) serveArgs() []string {
	args := []string{"serve", "--addr", c.Addr, "--state", c.StatePath}
	if c.RelayConfig != "" {
		args = append(args, "--relay-config", c.RelayConfig)
	}
	if c.LogFile != "" {
		args = append(args, "--log-file", c.LogFile)
	}
	return args
}
