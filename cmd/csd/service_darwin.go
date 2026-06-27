//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func installService() error {
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	path, err := launchAgentPath(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create launch agent dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(launchAgentPlist(cfg)), 0o600); err != nil {
		return fmt.Errorf("write launch agent %s: %w", path, err)
	}
	_ = exec.Command("launchctl", "unload", path).Run()
	if output, err := exec.Command("launchctl", "load", path).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load %s: %w: %s", path, err, string(output))
	}
	fmt.Printf("installed launchagent=%s\n", path)
	return nil
}

func uninstallService() error {
	cfg, err := defaultServiceConfig()
	if err != nil {
		return err
	}
	path, err := launchAgentPath(cfg)
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove launch agent %s: %w", path, err)
	}
	fmt.Printf("uninstalled launchagent=%s\n", path)
	return nil
}

func launchAgentPath(cfg serviceConfig) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", cfg.Name+".plist"), nil
}
