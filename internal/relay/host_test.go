package relay

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

func TestHostConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host.json")
	valid := `{"url":"https://relay.example.com","host":"test-host","token":"test-token-1234567890"}`
	for _, tc := range []struct {
		name, data string
		valid      bool
	}{
		{"defaults", valid, true},
		{"unknown", strings.TrimSuffix(valid, "}") + `,"secret-typo":"do-not-print"}`, false},
		{"trailing", valid + ` {}`, false},
		{"relative journal", strings.TrimSuffix(valid, "}") + `,"journal":"local.db"}`, false},
		{"fast poll", strings.TrimSuffix(valid, "}") + `,"interval":"1ms"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadHostConfig(path, filepath.Join(dir, "state.db"))
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Journal != filepath.Join(dir, "relay.db") || cfg.Interval != "30s" {
					t.Fatalf("wrong defaults")
				}
			} else if err == nil || strings.Contains(err.Error(), "do-not-print") {
				t.Fatalf("invalid error: %v", err)
			}
		})
	}
	if _, err := LoadHostConfig("relative.json", "state.db"); err == nil {
		t.Fatal("accepted relative config")
	}
}

func TestHostLoopCancellationInterruptsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runHostLoop(ctx, time.Hour, io.Discard, func(context.Context) error { close(called); return errors.New("temporary") })
	}()
	<-called
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("poll wait ignored cancellation")
	}
}
