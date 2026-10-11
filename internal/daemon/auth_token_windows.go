//go:build windows

package daemon

import (
	"fmt"
	"os"

	"github.com/MTG-Thomas/codex-swarm/internal/relay"
	"golang.org/x/sys/windows"
)

func openDaemonToken(path string) (*os.File, error) {
	return relay.OpenPrivateFile(path)
}

func restrictCreatedToken(file *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows identity: %w", err)
	}
	userSID := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;;FA;;;" + userSID + ")(A;;FA;;;SY)(A;;FA;;;BA)",
	)
	if err != nil {
		return fmt.Errorf("build private token DACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private token DACL: %w", err)
	}
	if dacl == nil {
		return fmt.Errorf("private token DACL is missing")
	}
	return windows.SetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}
