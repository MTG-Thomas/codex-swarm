package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
)

func appserverRuntime(args []string) error {
	fs := flag.NewFlagSet("appserver-runtime", flag.ContinueOnError)
	statePath := fs.String("state", defaultStatePath(), "state file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var request protocol.AppserverSpawnRequest
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, (1<<20)+(64<<10)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("read app-server runtime request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("app-server runtime request must contain one JSON document")
	}
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	return daemon.RunAppserverRuntime(ctx, *statePath, request, os.Stdout)
}
