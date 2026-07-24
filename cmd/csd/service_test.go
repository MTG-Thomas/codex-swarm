package main

import "testing"

func TestServeOptionsAcceptsExplicitStateAndAddr(t *testing.T) {
	addr, state, err := serveOptions([]string{"--addr", "127.0.0.1:9999", "--state", "state.json"})
	if err != nil {
		t.Fatalf("serveOptions() error = %v", err)
	}
	if addr != "127.0.0.1:9999" || state != "state.json" {
		t.Fatalf("serveOptions() = addr:%q state:%q", addr, state)
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
