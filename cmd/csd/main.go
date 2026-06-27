package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "csd: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return serve()
		case "status":
			return status()
		case "install":
			return serviceStub("install")
		case "uninstall":
			return serviceStub("uninstall")
		default:
			return fmt.Errorf("unknown command %q", args[0])
		}
	}
	return serve()
}

func serve() error {
	addr := envDefault("CODEX_SWARM_DAEMON_ADDR", "127.0.0.1:8787")
	statePath := envDefault("CODEX_SWARM_STATE", defaultStatePath())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runServer(ctx, addr, statePath, os.Stdout)
}

func status() error {
	baseURL := envDefault("CODEX_SWARM_DAEMON_URL", "http://127.0.0.1:8787")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := (daemon.Client{BaseURL: baseURL}).Status(ctx)
	if err != nil {
		return fmt.Errorf("daemon status: %w", err)
	}
	fmt.Println(status.String())
	return nil
}

func serviceStub(action string) error {
	if action == "" {
		return errors.New("service action is required")
	}
	fmt.Printf("service %s is not implemented yet; run `csd serve` or install it with your OS service manager\n", action)
	return nil
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultStatePath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "codex-swarm", "state.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".codex-swarm", "state.json")
	}
	return filepath.Join(".codex-swarm", "state.json")
}
