package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestServeOptionsAcceptsExplicitStateAndAddr(t *testing.T) {
	addr, state, err := serveOptions([]string{"--addr", "127.0.0.1:9999", "--state", "state.json"})
	if err != nil {
		t.Fatalf("serveOptions() error = %v", err)
	}
	if addr != "127.0.0.1:9999" || state != "state.json" {
		t.Fatalf("serveOptions() = addr:%q state:%q", addr, state)
	}
}

func TestServeConfigDerivesAndAcceptsDaemonTokenPath(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.db")
	cfg, err := parseServeConfig([]string{"--state", state}, state)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TokenFile != state+".daemon-token" {
		t.Fatalf("token file=%q", cfg.TokenFile)
	}
	explicit := filepath.Join(t.TempDir(), "explicit.token")
	cfg, err = parseServeConfig([]string{"--state", state, "--auth-token-file", explicit}, state)
	if err != nil || cfg.TokenFile != explicit {
		t.Fatalf("config=%+v error=%v", cfg, err)
	}
}

func TestServiceArgsPersistDaemonTokenPath(t *testing.T) {
	cfg := serviceConfig{
		Addr: "127.0.0.1:8787", StatePath: "/state/state.db", TokenFile: "/state/daemon.token",
	}
	want := []string{
		"serve", "--addr", "127.0.0.1:8787", "--state", "/state/state.db",
		"--auth-token-file", "/state/daemon.token",
	}
	if got := cfg.serveArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
	}
}

func TestParseServiceScope(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  serviceScope
		isErr bool
	}{
		{name: "system default", want: serviceScopeSystem},
		{name: "user", args: []string{"--user"}, want: serviceScopeUser},
		{name: "unknown flag", args: []string{"--unknown"}, isErr: true},
		{name: "positional argument", args: []string{"extra"}, isErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServiceScope("install", tt.args)
			if (err != nil) != tt.isErr {
				t.Fatalf("parseServiceScope() error = %v, want error %v", err, tt.isErr)
			}
			if !tt.isErr && got != tt.want {
				t.Fatalf("parseServiceScope() = %v, want %v", got, tt.want)
			}
		})
	}
}
