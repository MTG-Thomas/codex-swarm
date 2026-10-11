//go:build windows

package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileDeleteChild windows.ACCESS_MASK = 0x00000040

// openPrivateFile resolves every component relative to an already-opened
// no-reparse handle. Omitting FILE_SHARE_DELETE keeps each component stable
// until the next handle is acquired, and the final file remains locked against
// replacement for the caller's entire read.
func openPrivateFile(path string) (*os.File, error) {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	if !filepath.IsAbs(clean) || len(volume) != 2 || volume[1] != ':' {
		return nil, fmt.Errorf("private file must use an absolute local drive path")
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read current Windows identity: %w", err)
	}
	trusted, err := privateWindowsSIDs(user.User.Sid)
	if err != nil {
		return nil, err
	}

	root := volume + `\`
	h, err := openWindowsComponent(0, `\??\`+root, true, false)
	if err != nil {
		return nil, fmt.Errorf("open private path volume: %w", err)
	}
	if err = checkPrivateWindowsHandle(h, trusted, false, true); err != nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("private path volume: %w", err)
	}

	parts := strings.Split(strings.TrimLeft(strings.TrimPrefix(clean, volume), `\/`), `\`)
	for i, part := range parts {
		if strings.Contains(part, ":") {
			windows.CloseHandle(h)
			return nil, fmt.Errorf("private file path must not use an alternate data stream")
		}
		last := i == len(parts)-1
		next, openErr := openWindowsComponent(h, part, !last, last)
		windows.CloseHandle(h)
		if openErr != nil {
			return nil, fmt.Errorf("open private path component %q: %w", part, openErr)
		}
		h = next
		if err = checkPrivateWindowsHandle(h, trusted, last, false); err != nil {
			windows.CloseHandle(h)
			return nil, fmt.Errorf("private path component %q: %w", part, err)
		}
	}

	f := os.NewFile(uintptr(h), clean)
	if f == nil {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("adopt private file handle")
	}
	return f, nil
}

func openWindowsComponent(root windows.Handle, name string, directory, readable bool) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: root,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	access := uint32(windows.READ_CONTROL | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		access |= windows.FILE_TRAVERSE
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
		if readable {
			access |= windows.FILE_GENERIC_READ
		}
	}
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, access, oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN, options, 0, 0)
	return h, err
}

func privateWindowsSIDs(user *windows.SID) ([]*windows.SID, error) {
	values := []string{
		"S-1-5-18",     // LocalSystem
		"S-1-5-32-544", // Builtin Administrators
		"S-1-3-0",      // Creator Owner
		"S-1-3-4",      // Owner Rights
		// NT SERVICE\TrustedInstaller, which owns protected Windows ancestors.
		"S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464",
	}
	result := []*windows.SID{user}
	for _, value := range values {
		sid, err := windows.StringToSid(value)
		if err != nil {
			return nil, fmt.Errorf("parse trusted Windows SID: %w", err)
		}
		result = append(result, sid)
	}
	return result, nil
}

func checkPrivateWindowsHandle(h windows.Handle, trusted []*windows.SID, final, volumeRoot bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return fmt.Errorf("inspect file identity: %w", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("reparse points are not allowed")
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DEVICE != 0 {
		return fmt.Errorf("device paths are not allowed")
	}
	if final == (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) {
		if final {
			return fmt.Errorf("expected a regular file")
		}
		return fmt.Errorf("expected a directory")
	}

	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read owner and DACL: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read owner SID: %w", err)
	}
	if final {
		if !owner.Equals(trusted[0]) {
			return fmt.Errorf("private file must be owned by the current user")
		}
	} else if !volumeRoot && !sidIn(owner, trusted) {
		return fmt.Errorf("private path directory has an untrusted owner")
	}

	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("private path must have a restrictive DACL")
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fmt.Errorf("read DACL entry: %w", err)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("private path contains an unsupported allow DACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sidIn(sid, trusted) {
			continue
		}
		if final && ace.Mask != 0 {
			return fmt.Errorf("private file grants access to another principal")
		}
		forbidden := windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_ALL | fileDeleteChild
		if !final && ace.Mask&forbidden != 0 {
			return fmt.Errorf("private path directory can be replaced by another principal")
		}
	}
	return nil
}

func sidIn(candidate *windows.SID, values []*windows.SID) bool {
	for _, value := range values {
		if candidate.Equals(value) {
			return true
		}
	}
	return false
}
