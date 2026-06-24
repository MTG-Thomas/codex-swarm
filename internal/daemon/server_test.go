package daemon

import "testing"

func TestStatusString(t *testing.T) {
	status := Status{Daemon: "scaffold", Workers: 2}
	got := status.String()
	want := "daemon=scaffold workers=2"
	if got != want {
		t.Fatalf("Status.String() = %q, want %q", got, want)
	}
}
