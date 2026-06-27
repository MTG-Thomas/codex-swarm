package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunServerShutdownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, "127.0.0.1:0", filepath.Join(t.TempDir(), "state.json"), &out)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "csd listening addr=") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start, output=%q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
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
