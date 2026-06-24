package daemon

import "fmt"

// Status is the compact operator-facing state returned by the daemon.
type Status struct {
	Daemon  string
	Workers int
}

func (s Status) String() string {
	return fmt.Sprintf("daemon=%s workers=%d", s.Daemon, s.Workers)
}
