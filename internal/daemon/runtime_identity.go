package daemon

import (
	"os/user"
	"strings"
)

func privilegedRuntimeReason() string {
	current, err := user.Current()
	if err != nil {
		return "cannot verify daemon operating-system identity: " + err.Error()
	}
	username := strings.ToLower(strings.TrimSpace(current.Username))
	uid := strings.TrimSpace(current.Uid)
	if uid == "0" || uid == "S-1-5-18" || username == "root" || username == "system" || strings.HasSuffix(username, `\system`) {
		return "privileged_service_identity"
	}
	return ""
}
