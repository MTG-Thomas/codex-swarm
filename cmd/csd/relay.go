package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/config"
	"github.com/MTG-Thomas/codex-swarm/internal/relay"
)

// relayHost is explicitly invoked as a user process; system service defaults
// remain unchanged, and no inbound network listener is introduced.
func relayHost(args []string) error {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	journal := fs.String("journal", filepath.Join(filepath.Dir(config.DefaultStatePath()), "relay.db"), "local delivery journal")
	binary := fs.String("codex", "codex", "installed Codex executable")
	once := fs.Bool("once", false, "process one delivery/recovery cycle")
	interval := fs.Duration("interval", 30*time.Second, "idle polling interval (at least 5s)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *interval < 5*time.Second {
		return fmt.Errorf("invalid relay arguments or polling interval below 5s")
	}
	if err := relay.CheckUser(); err != nil {
		return err
	}
	client, err := relay.FromEnv()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	queue := relay.CodexQueue{Binary: *binary}
	if err = queue.Check(ctx); err != nil {
		return err
	}
	// Acquire per cycle, so enrollment/revocation can run while polling is idle.
	for {
		j, e := relay.OpenJournal(*journal, client.URL, client.Host)
		if e == nil {
			e = (relay.Runner{Host: client.Host, Journal: j, Mailbox: client, Queue: queue}).Once(ctx)
			_ = j.Close()
		}
		if *once {
			return e
		}
		if e != nil && !errors.Is(e, context.Canceled) {
			fmt.Fprintln(os.Stderr, "relay:", e)
		}
		timer := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
