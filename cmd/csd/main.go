package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/config"
	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "csd: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if handled, err := maybeRunService(); handled || err != nil {
		return err
	}
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return serve(args[1:])
		case "status":
			return status()
		case "appserver-runtime":
			return appserverRuntime(args[1:])
		case "version":
			fmt.Println(version.String())
			return nil
		case "install":
			return installService()
		case "uninstall":
			return uninstallService()
		default:
			return fmt.Errorf("unknown command %q", args[0])
		}
	}
	return serve(nil)
}

func serve(args []string) error {
	addr, statePath, err := serveOptions(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	return runServer(ctx, addr, statePath, os.Stdout)
}

func serveOptions(args []string) (string, string, error) {
	return serveOptionsWithDefaultState(args, defaultStatePath())
}

func serveOptionsWithDefaultState(args []string, defaultState string) (string, string, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", envDefault("CODEX_SWARM_DAEMON_ADDR", "127.0.0.1:8787"), "daemon listen address")
	statePath := fs.String("state", envDefault("CODEX_SWARM_STATE", defaultState), "state file path")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	return *addr, *statePath, nil
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

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultStatePath() string {
	return config.DefaultStatePath()
}
