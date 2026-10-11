//go:build !windows

package store

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openStateLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: state lock must not be a symlink", errUnsafeStateLock)
		}
		return nil, err
	}
	closeOnError := func(err error) (*os.File, error) {
		_ = unix.Close(fd)
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return closeOnError(fmt.Errorf("inspect state lock: %w", err))
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG {
		return closeOnError(fmt.Errorf("%w: state lock must be a regular file", errUnsafeStateLock))
	}
	if st.Uid != uint32(os.Geteuid()) {
		return closeOnError(fmt.Errorf("%w: state lock must be owned by the current user", errUnsafeStateLock))
	}
	if st.Nlink != 1 {
		return closeOnError(fmt.Errorf("%w: state lock must not have additional hard links", errUnsafeStateLock))
	}
	if uint32(st.Mode)&0o077 != 0 {
		if err := unix.Fchmod(fd, 0o600); err != nil {
			return closeOnError(fmt.Errorf("restrict state lock permissions: %w", err))
		}
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		return closeOnError(fmt.Errorf("adopt state lock descriptor"))
	}
	return file, nil
}

func tryLockStateFile(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockStateFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
