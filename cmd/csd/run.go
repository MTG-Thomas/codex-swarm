package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/daemon"
	"github.com/MTG-Thomas/codex-swarm/internal/relay"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func runConfiguredServer(ctx context.Context, c serveConfig, out io.Writer) error {
	out = &lockedWriter{out: out}
	var task func(context.Context) error
	if c.RelayConfig != "" {
		if err := relay.CheckUser(); err != nil {
			return fmt.Errorf("configured relay: %w; use a user-owned daemon (csd install --user)", err)
		}
		state, err := filepath.Abs(c.StatePath)
		if err != nil {
			return err
		}
		cfg, err := relay.LoadHostConfig(c.RelayConfig, state)
		if err != nil {
			return err
		}
		task = func(ctx context.Context) error { return relay.RunHost(ctx, cfg, out) }
	}
	return runServerWithTask(ctx, c.Addr, c.StatePath, out, task)
}
func runServer(ctx context.Context, addr, statePath string, out io.Writer) error {
	return runServerWithTask(ctx, addr, statePath, out, nil)
}
func runServerWithTask(parent context.Context, addr, statePath string, out io.Writer, task func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	server := daemon.NewServer(statePath, store.NewJSONStore(statePath))
	defer server.Close()
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	fmt.Fprintf(out, "csd listening addr=%s state=%s\n", listener.Addr().String(), statePath)

	results := make(chan error, 2)
	workers := 1
	go func() { results <- httpServer.Serve(listener) }()
	if task != nil {
		workers++
		go func() { results <- task(ctx) }()
	}
	var first error
	select {
	case <-ctx.Done():
	case first = <-results:
		workers--
		if first == nil && ctx.Err() == nil {
			first = fmt.Errorf("daemon background component exited unexpectedly")
		}
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	stop()
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	for range workers {
		if err := <-results; first == nil && err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			first = err
		}
	}
	if errors.Is(first, http.ErrServerClosed) || errors.Is(first, context.Canceled) {
		first = nil
	}
	return errors.Join(first, shutdownErr)
}

// The server and relay share one output stream; serialize writes for custom
// writers as well as files when both components emit diagnostics.
type lockedWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(b)
}
