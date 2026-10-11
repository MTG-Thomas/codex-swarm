//go:build windows

package daemon

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/MTG-Thomas/codex-swarm/internal/relay"
	"golang.org/x/sys/windows"
)

func openDaemonToken(path string) (*os.File, error) {
	return relay.OpenPrivateFile(path)
}

// createDaemonToken applies the private DACL as part of CREATE_NEW. This avoids
// both a permissive creation window and relying on an os.File handle that was
// opened without WRITE_DAC to replace the inherited ACL afterward.
func createDaemonToken(path string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read current Windows identity: %w", err)
	}
	userSID := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;FA;;;" + userSID + ")(A;;FA;;;SY)(A;;FA;;;BA)",
	)
	if err != nil {
		return nil, fmt.Errorf("build private token DACL: %w", err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("encode daemon token path: %w", err)
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_WRITE|windows.READ_CONTROL,
		0,
		attributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err == windows.ERROR_FILE_EXISTS || err == windows.ERROR_ALREADY_EXISTS {
		return nil, os.ErrExist
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("adopt daemon token handle")
	}
	return file, nil
}
