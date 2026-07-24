//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	}, serviceScopeSystem)
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

func TestSystemdUserUnitUsesDefaultTarget(t *testing.T) {
	unit := systemdUnit(serviceConfig{
		Name:        "codex-swarm-daemon",
		Description: "Local Codex Swarm daemon",
		Executable:  "/usr/local/bin/csd",
		Args:        []string{"serve", "--addr", "127.0.0.1:8787", "--state", "/home/operator/.config/codex-swarm/state.db"},
		Addr:        "127.0.0.1:8787",
		StatePath:   "/home/operator/.config/codex-swarm/state.db",
	}, serviceScopeUser)
	if !strings.Contains(unit, "WantedBy=default.target") {
		t.Fatalf("user unit missing default target:\n%s", unit)
	}
	if strings.Contains(unit, "After=network.target") || strings.Contains(unit, "WantedBy=multi-user.target") {
		t.Fatalf("user unit contains system-service targets:\n%s", unit)
	}
}

func TestSystemdUserServiceInstallAndUninstall(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("CODEX_SWARM_STATE", filepath.Join(configHome, "codex-swarm", "state.db"))

	originalRunSystemctl := runSystemctl
	t.Cleanup(func() { runSystemctl = originalRunSystemctl })
	var calls [][]string
	runSystemctl = func(scope serviceScope, args ...string) ([]byte, error) {
		call := append([]string{scope.String()}, args...)
		calls = append(calls, call)
		if slices.Equal(args, []string{"show", "--property=LoadState", "--value", serviceName + ".service"}) {
			return []byte("loaded\n"), nil
		}
		return nil, nil
	}

	if err := installService([]string{"--user"}); err != nil {
		t.Fatalf("installService(--user): %v", err)
	}
	cfg, err := defaultServiceConfig()
	if err != nil {
		t.Fatal(err)
	}
	path, err := systemdServicePath(cfg, serviceScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read installed user unit: %v", err)
	}
	if !strings.Contains(string(content), "WantedBy=default.target") {
		t.Fatalf("installed user unit missing default target:\n%s", content)
	}
	if !slices.Equal(calls[0], []string{"user", "daemon-reload"}) ||
		!slices.Equal(calls[1], []string{"user", "enable", serviceName + ".service"}) {
		t.Fatalf("install systemctl calls = %#v", calls)
	}

	calls = nil
	if err := uninstallService([]string{"--user"}); err != nil {
		t.Fatalf("uninstallService(--user): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("user unit still exists after uninstall: %v", err)
	}
	wantCalls := [][]string{
		{"user", "show", "--property=LoadState", "--value", serviceName + ".service"},
		{"user", "stop", serviceName + ".service"},
		{"user", "disable", serviceName + ".service"},
		{"user", "daemon-reload"},
	}
	if !slices.EqualFunc(calls, wantCalls, slices.Equal[[]string]) {
		t.Fatalf("uninstall systemctl calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestSystemdUserServicePathRejectsRelativeConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative")
	originalRunSystemctl := runSystemctl
	t.Cleanup(func() { runSystemctl = originalRunSystemctl })
	runSystemctl = func(serviceScope, ...string) ([]byte, error) {
		t.Fatal("systemctl called before path validation")
		return nil, nil
	}
	if _, err := systemdServicePath(serviceConfig{Name: serviceName}, serviceScopeUser); err == nil {
		t.Fatal("systemdServicePath accepted relative XDG_CONFIG_HOME")
	}
	if err := uninstallService([]string{"--user"}); err == nil {
		t.Fatal("uninstallService accepted relative XDG_CONFIG_HOME")
	}
}

func TestSystemdUninstallToleratesMissingUnit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	originalRunSystemctl := runSystemctl
	t.Cleanup(func() { runSystemctl = originalRunSystemctl })
	var calls [][]string
	runSystemctl = func(scope serviceScope, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{scope.String()}, args...))
		if args[0] == "show" {
			return []byte("not-found\n"), nil
		}
		return nil, nil
	}
	if err := uninstallService([]string{"--user"}); err != nil {
		t.Fatalf("uninstallService missing unit: %v", err)
	}
	wantCalls := [][]string{
		{"user", "show", "--property=LoadState", "--value", serviceName + ".service"},
		{"user", "daemon-reload"},
	}
	if !slices.EqualFunc(calls, wantCalls, slices.Equal[[]string]) {
		t.Fatalf("systemctl calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestSystemdUninstallPropagatesStopFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	originalRunSystemctl := runSystemctl
	t.Cleanup(func() { runSystemctl = originalRunSystemctl })
	runSystemctl = func(_ serviceScope, args ...string) ([]byte, error) {
		switch args[0] {
		case "show":
			return []byte("loaded\n"), nil
		case "stop":
			return []byte("stop failed"), errors.New("exit status 1")
		default:
			t.Fatalf("unexpected systemctl call after stop failure: %v", args)
			return nil, nil
		}
	}
	err := uninstallService([]string{"--user"})
	if err == nil || !strings.Contains(err.Error(), "stop codex-swarm-daemon.service") {
		t.Fatalf("uninstallService stop error = %v", err)
	}
}
