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
