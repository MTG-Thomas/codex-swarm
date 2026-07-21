package version

import "fmt"

var (
	Version = "0.4.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("version=%s commit=%s date=%s", Version, Commit, Date)
}
