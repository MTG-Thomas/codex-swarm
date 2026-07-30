//go:build windows

package userpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func UpdateExecutableDir(action Action) (Result, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	entry := filepath.Dir(executable)

	key, err := openEnvironmentKey(action)
	if err != nil {
		if action == Remove && errors.Is(err, registry.ErrNotExist) {
			return Absent, nil
		}
		return "", fmt.Errorf("open HKCU Environment: %w", err)
	}
	defer key.Close()

	current, valueType, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return "", fmt.Errorf("read user PATH: %w", err)
	}
	if errors.Is(err, registry.ErrNotExist) {
		current = ""
		valueType = registry.EXPAND_SZ
	}

	updated, result, err := Apply(current, entry, action)
	if err != nil {
		return "", err
	}
	if updated == current {
		return result, nil
	}

	if updated == "" {
		if err := key.DeleteValue("Path"); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return "", fmt.Errorf("delete empty user PATH: %w", err)
		}
		return result, nil
	}

	if valueType == registry.SZ {
		err = key.SetStringValue("Path", updated)
	} else {
		err = key.SetExpandStringValue("Path", updated)
	}
	if err != nil {
		return "", fmt.Errorf("write user PATH: %w", err)
	}
	return result, nil
}

func openEnvironmentKey(action Action) (registry.Key, error) {
	const access = registry.QUERY_VALUE | registry.SET_VALUE
	if action == Add {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, access)
		return key, err
	}
	return registry.OpenKey(registry.CURRENT_USER, `Environment`, access)
}
