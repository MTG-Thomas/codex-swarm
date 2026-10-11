//go:build windows

package store

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
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

func openStateLockFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0)
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (*os.File, error) {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return closeOnError(fmt.Errorf("inspect state lock: %w", err))
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return closeOnError(fmt.Errorf("%w: state lock must not be a reparse point", errUnsafeStateLock))
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_DEVICE) != 0 {
		return closeOnError(fmt.Errorf("%w: state lock must be a regular file", errUnsafeStateLock))
	}
	if info.NumberOfLinks != 1 {
		return closeOnError(fmt.Errorf("%w: state lock must not have additional hard links", errUnsafeStateLock))
	}
	file := os.NewFile(uintptr(h), path)
	if file == nil {
		return closeOnError(fmt.Errorf("adopt state lock handle"))
	}
	return file, nil
}

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
