package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "csd: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	addr := envDefault("CODEX_SWARM_DAEMON_ADDR", "127.0.0.1:8787")
	statePath := envDefault("CODEX_SWARM_STATE", ".codex-swarm/state.json")
	server := daemon.NewServer(statePath, store.NewJSONStore(statePath))
	fmt.Printf("csd listening addr=%s state=%s\n", addr, statePath)
	return http.ListenAndServe(addr, server.Handler())
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
