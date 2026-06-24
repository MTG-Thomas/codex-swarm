package config

import "path/filepath"

type Config struct {
	Home string
}

func DefaultHome(userHome string) string {
	return filepath.Join(userHome, ".codex-swarm")
}
