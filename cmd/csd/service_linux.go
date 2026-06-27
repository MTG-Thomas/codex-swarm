//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func installService() error {
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	path := systemdServicePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create systemd unit dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(systemdUnit(cfg)), 0o644); err != nil {
		return fmt.Errorf("write systemd unit %s: %w", path, err)
	}
	if output, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, string(output))
	}
	if output, err := exec.Command("systemctl", "enable", cfg.Name+".service").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable %s.service: %w: %s", cfg.Name, err, string(output))
	}
	fmt.Printf("installed systemd=%s\n", path)
	return nil
}

func uninstallService() error {
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	unit := cfg.Name + ".service"
	_ = exec.Command("systemctl", "stop", unit).Run()
	_ = exec.Command("systemctl", "disable", unit).Run()
	path := systemdServicePath(cfg)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove systemd unit %s: %w", path, err)
	}
	if output, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, string(output))
	}
	fmt.Printf("uninstalled systemd=%s\n", path)
	return nil
}

func systemdServicePath(cfg serviceConfig) string {
	return filepath.Join("/etc/systemd/system", cfg.Name+".service")
}

func systemdUnit(cfg serviceConfig) string {
	args := append([]string{cfg.Executable}, cfg.Args...)
	quotedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, systemdQuote(arg))
	}
	return fmt.Sprintf(`[Unit]
Description=%s
After=network.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=5
Environment=%s
Environment=%s

[Install]
WantedBy=multi-user.target
`, cfg.Description, strings.Join(quotedArgs, " "), systemdQuote("CODEX_SWARM_DAEMON_ADDR="+cfg.Addr), systemdQuote("CODEX_SWARM_STATE="+cfg.StatePath))
}

func systemdQuote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + replacer.Replace(value) + `"`
}
