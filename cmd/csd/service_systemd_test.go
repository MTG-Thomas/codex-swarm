//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestSystemdUnitQuotesAndIncludesServeArgs(t *testing.T) {
	unit := systemdUnit(serviceConfig{
		Name:        "codex-swarm-daemon",
		Description: "Local Codex Swarm daemon",
		Executable:  `/opt/codex swarm/csd`,
		Args:        []string{"serve", "--addr", "127.0.0.1:18787", "--state", `/var/lib/codex swarm/state.json`},
		Addr:        "127.0.0.1:18787",
		StatePath:   `/var/lib/codex swarm/state.json`,
	})
	for _, want := range []string{
		"[Unit]",
		"Description=Local Codex Swarm daemon",
		`ExecStart="/opt/codex swarm/csd" "serve" "--addr" "127.0.0.1:18787" "--state" "/var/lib/codex swarm/state.json"`,
		`Environment="CODEX_SWARM_DAEMON_ADDR=127.0.0.1:18787"`,
		`Environment="CODEX_SWARM_STATE=/var/lib/codex swarm/state.json"`,
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}
