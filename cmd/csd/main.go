package main

import (
	"fmt"
	"os"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "csd: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	status := daemon.Status{
		Daemon:  "scaffold",
		Workers: 0,
	}
	fmt.Println(status.String())
	return nil
}
