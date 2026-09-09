package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestRunServerJoinsTaskOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, joined := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	state := filepath.Join(t.TempDir(), "state.db")
	go func() {
		done <- runServerWithTask(ctx, "127.0.0.1:0", state, io.Discard, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(joined)
			return ctx.Err()
		})
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join task")
	}
	select {
	case <-joined:
	default:
		t.Fatal("task not joined")
	}
}

func TestRunServerStopsWhenTaskFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	expected := errors.New("relay startup failed")
	err := runServerWithTask(ctx, "127.0.0.1:0", filepath.Join(t.TempDir(), "state.db"), io.Discard, func(context.Context) error { return expected })
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunConfiguredServerLogsStartupFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "csd.log")
	err := runConfiguredServer(context.Background(), serveConfig{Addr: "invalid-address", StatePath: filepath.Join(dir, "state.db"), LogFile: path}, io.Discard)
	if err == nil {
		t.Fatal("expected startup error")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), err.Error()) {
		t.Fatalf("startup error absent from log: %s", data)
	}
}
