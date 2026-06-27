//go:build windows

package store

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

const errorSharingViolation syscall.Errno = 32

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceStateFile(tmp, target string) error {
	tmpPtr, err := syscall.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(time.Second)
	for {
		ret, _, callErr := moveFileExW.Call(
			uintptr(unsafe.Pointer(tmpPtr)),
			uintptr(unsafe.Pointer(targetPtr)),
			uintptr(moveFileReplaceExisting|moveFileWriteThrough),
		)
		if ret != 0 {
			return nil
		}
		if callErr == syscall.Errno(0) {
			return fmt.Errorf("MoveFileExW failed")
		}
		if !isTransientReplaceError(callErr) || time.Now().After(deadline) {
			return callErr
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isTransientReplaceError(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.ERROR_ACCESS_DENIED || errno == errorSharingViolation
	}
	return false
}
