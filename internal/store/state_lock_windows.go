//go:build windows

package store

import (
	"os"
	"syscall"
	"unsafe"
)

const (
	lockFileExclusiveLock   = 0x2
	lockFileFailImmediately = 0x1
)

const errorLockViolation syscall.Errno = 33

var (
	lockFileExProc   = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	unlockFileExProc = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
)

func tryLockStateFile(file *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	ret, _, err := lockFileExProc.Call(
		file.Fd(),
		uintptr(lockFileExclusiveLock|lockFileFailImmediately),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret != 0 {
		return true, nil
	}
	if errno, ok := err.(syscall.Errno); ok {
		if errno == errorLockViolation {
			return false, nil
		}
	}
	return false, err
}

func unlockStateFile(file *os.File) error {
	var overlapped syscall.Overlapped
	ret, _, err := unlockFileExProc.Call(
		file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret != 0 {
		return nil
	}
	if err != syscall.Errno(0) {
		return err
	}
	return nil
}
