//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func installService(args []string) error {
	scope, err := parseServiceScope("install", args)
	if err != nil {
		return err
	}
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	path, err := systemdServicePath(cfg, scope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create systemd unit dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(systemdUnit(cfg, scope)), 0o644); err != nil {
		return fmt.Errorf("write systemd unit %s: %w", path, err)
	}
	if output, err := runSystemctl(scope, "daemon-reload"); err != nil {
		return fmt.Errorf("%s daemon-reload: %w: %s", systemctlName(scope), err, string(output))
	}
	if output, err := runSystemctl(scope, "enable", cfg.Name+".service"); err != nil {
		return fmt.Errorf("%s enable %s.service: %w: %s", systemctlName(scope), cfg.Name, err, string(output))
	}
	fmt.Printf("installed systemd=%s scope=%s\n", path, scope)
	return nil
}

func uninstallService(args []string) error {
	scope, err := parseServiceScope("uninstall", args)
	if err != nil {
		return err
	}
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	unit := cfg.Name + ".service"
	path, err := systemdServicePath(cfg, scope)
	if err != nil {
		return err
	}
	loaded, err := systemdUnitLoaded(scope, unit)
	if err != nil {
		return err
	}
	if loaded {
		if output, err := runSystemctl(scope, "stop", unit); err != nil {
			return fmt.Errorf("%s stop %s: %w: %s", systemctlName(scope), unit, err, string(output))
		}
		if output, err := runSystemctl(scope, "disable", unit); err != nil {
			return fmt.Errorf("%s disable %s: %w: %s", systemctlName(scope), unit, err, string(output))
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove systemd unit %s: %w", path, err)
	}
	if output, err := runSystemctl(scope, "daemon-reload"); err != nil {
		return fmt.Errorf("%s daemon-reload: %w: %s", systemctlName(scope), err, string(output))
	}
	fmt.Printf("uninstalled systemd=%s scope=%s\n", path, scope)
	return nil
}

func systemdServicePath(cfg serviceConfig, scope serviceScope) (string, error) {
	if scope == serviceScopeSystem {
		return filepath.Join("/etc/systemd/system", cfg.Name+".service"), nil
	}
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(configDir) {
		return "", fmt.Errorf("XDG_CONFIG_HOME must be absolute: %s", configDir)
	}
	return filepath.Join(configDir, "systemd", "user", cfg.Name+".service"), nil
}

func systemdUnit(cfg serviceConfig, scope serviceScope) string {
	args := append([]string{cfg.Executable}, cfg.Args...)
	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, systemdQuote(arg))
	}
	after := "After=network.target\n"
	wantedBy := "multi-user.target"
	if scope == serviceScopeUser {
		after = ""
		wantedBy = "default.target"
	}
	return fmt.Sprintf(`[Unit]
Description=%s
%s
[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=5
Environment=%s
Environment=%s

[Install]
WantedBy=%s
`, cfg.Description, after, strings.Join(quotedArgs, " "), systemdQuote("CODEX_SWARM_DAEMON_ADDR="+cfg.Addr), systemdQuote("CODEX_SWARM_STATE="+cfg.StatePath), wantedBy)
}

func systemdQuote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + replacer.Replace(value) + `"`
}

var runSystemctl = func(scope serviceScope, args ...string) ([]byte, error) {
	commandArgs := args
	if scope == serviceScopeUser {
		commandArgs = append([]string{"--user"}, args...)
	}
	return exec.Command("systemctl", commandArgs...).CombinedOutput()
}

func systemctlName(scope serviceScope) string {
	if scope == serviceScopeUser {
		return "systemctl --user"
	}
	return "systemctl"
}

func systemdUnitLoaded(scope serviceScope, unit string) (bool, error) {
	output, err := runSystemctl(scope, "show", "--property=LoadState", "--value", unit)
	if err != nil {
		return false, fmt.Errorf("%s show LoadState for %s: %w: %s", systemctlName(scope), unit, err, string(output))
	}
	state := strings.TrimSpace(string(output))
	if state == "" {
		return false, fmt.Errorf("%s returned an empty LoadState for %s", systemctlName(scope), unit)
	}
	return state != "not-found", nil
}
