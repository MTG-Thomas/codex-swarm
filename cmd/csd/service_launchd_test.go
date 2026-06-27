//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestLaunchAgentPlistEscapesAndIncludesServeArgs(t *testing.T) {
	plist := launchAgentPlist(serviceConfig{
		Name:       "codex-swarm-daemon",
		Executable: "/tmp/csd",
		Args:       []string{"serve", "--addr", "127.0.0.1:8787", "--state", "/tmp/a&b.json"},
		Addr:       "127.0.0.1:8787",
		StatePath:  "/tmp/a&b.json",
	})
	for _, want := range []string{
		"<string>codex-swarm-daemon</string>",
		"<string>/tmp/csd</string>",
		"<string>serve</string>",
		"<string>--addr</string>",
		"<string>127.0.0.1:8787</string>",
		"<string>/tmp/a&amp;b.json</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
}
