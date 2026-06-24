package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "cs: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		status := daemon.Status{
			Daemon:  "not-running",
			Workers: 0,
		}
		fmt.Println(status.String())
		return nil
	case "spawn", "send", "resume", "report":
		return fmt.Errorf("%q is planned but not implemented in the scaffold", args[0])
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Println(`cs - Codex swarm operator CLI

Usage:
  cs status
  cs spawn --repo . --prompt "..."
  cs send <worker> "..."
  cs resume <worker>
  cs report <worker> done`)
}
