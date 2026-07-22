package version

import "fmt"

var (
	Version = "0.7.1-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("version=%s commit=%s date=%s", Version, Commit, Date)
}
