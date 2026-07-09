package protocol

import "testing"

func TestStatusString(t *testing.T) {
	status := Status{Daemon: "running", Version: "0.2.0", StatePath: "state.json", WorkerCount: 2, ClaimCount: 1, ConflictCount: 1}
	want := "daemon=running version=0.2.0 workers=2 claims=1 conflicts=1 state=state.json"
	if got := status.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
