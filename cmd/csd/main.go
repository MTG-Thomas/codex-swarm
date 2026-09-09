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
		case "relay":
			return relayHost(args[1:])
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
			return installService(args[1:])
		case "uninstall":
			return uninstallService(args[1:])
		default:
			return fmt.Errorf("unknown command %q", args[0])
		}
	}
	return serve(nil)
}

func serve(args []string) error {
	cfg, err := parseServeConfig(args, defaultStatePath())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	return runConfiguredServer(ctx, cfg, os.Stdout)
}

func serveOptions(args []string) (string, string, error) {
	return serveOptionsWithDefaultState(args, defaultStatePath())
}

type serveConfig struct{ Addr, StatePath, RelayConfig, LogFile string }

func serveOptionsWithDefaultState(args []string, defaultState string) (string, string, error) {
	c, err := parseServeConfig(args, defaultState)
	return c.Addr, c.StatePath, err
}
func parseServeConfig(args []string, defaultState string) (serveConfig, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", envDefault("CODEX_SWARM_DAEMON_ADDR", "127.0.0.1:8787"), "daemon listen address")
	state := fs.String("state", envDefault("CODEX_SWARM_STATE", defaultState), "local ledger path")
	relay := fs.String("relay-config", os.Getenv("CODEX_SWARM_RELAY_CONFIG"), "absolute private relay JSON config path (opt-in)")
	logFile := fs.String("log-file", os.Getenv("CODEX_SWARM_LOG_FILE"), "absolute daemon log path")
	if err := fs.Parse(args); err != nil {
		return serveConfig{}, err
	}
	if fs.NArg() != 0 {
		return serveConfig{}, fmt.Errorf("serve accepts no positional arguments")
	}
	return serveConfig{Addr: *addr, StatePath: *state, RelayConfig: *relay, LogFile: *logFile}, nil
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
