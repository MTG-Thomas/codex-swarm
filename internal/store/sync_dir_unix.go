//go:build !windows

package store

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func syncParentDir(path string) (err error) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dir.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err := dir.Sync(); err != nil {
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
			return nil
		}
		return err
	}
	return nil
}
