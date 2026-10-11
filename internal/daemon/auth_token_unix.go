//go:build linux || darwin

package daemon

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openDaemonToken(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if uint32(stat.Mode)&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, fmt.Errorf("daemon token must be regular")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return nil, fmt.Errorf("daemon token must be owned by the current user")
	}
	if uint32(stat.Mode)&0o077 != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("daemon token permissions must not grant group or other access")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("adopt daemon token descriptor")
	}
	return file, nil
}

func createDaemonToken(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
