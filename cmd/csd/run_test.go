package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestRunServerShutdownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, "127.0.0.1:0", filepath.Join(t.TempDir(), "state.json"), signalWriter{started: started})
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("server did not write its startup line")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServer() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServer did not exit after context cancellation")
	}
}

type signalWriter struct {
	started chan<- struct{}
}

func (w signalWriter) Write(p []byte) (int, error) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	return io.Discard.Write(p)
}
