//go:build !windows

package store

import "os"

func replaceStateFile(tmp, target string) error {
	return os.Rename(tmp, target)
}
