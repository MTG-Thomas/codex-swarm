package store

import "testing"

func TestCapabilitiesForWorkerAreStableAcrossEngineIdentity(t *testing.T) {
	tests := []struct {
		name   string
		worker Worker
		want   RuntimeCapabilities
	}{
		{name: "appserver", worker: Worker{Engine: "appserver"}, want: RuntimeCapabilities{CapabilityLiveMessage, CapabilityResume, CapabilityAutomaticCompletion}},
		{name: "external appserver", worker: Worker{Engine: "appserver", RuntimeOwner: RuntimeOwnerExternal}, want: RuntimeCapabilities{CapabilityLiveMessage, CapabilityResume, CapabilityAutomaticCompletion, CapabilityNativeSteeringBridge}},
		{name: "tracker", worker: Worker{Engine: "tracker"}, want: RuntimeCapabilities{CapabilityExternalTracker}},
		{name: "mock", worker: Worker{Engine: "mock"}, want: RuntimeCapabilities{CapabilityAutomaticCompletion}},
		{name: "new engine remains protocol compatible", worker: Worker{Engine: "future-engine"}, want: RuntimeCapabilities{}},
		{name: "managed worktree requires evidence", worker: Worker{Engine: "future-engine", Worktree: "/repo/worktree", Events: []Event{{Type: "worktree.created"}}}, want: RuntimeCapabilities{CapabilityManagedWorktree}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CapabilitiesForWorker(tt.worker)
			if len(got) != len(tt.want) {
				t.Fatalf("capabilities = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("capabilities = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestCapabilitiesForWorkerRejectsFabricatedWorktree(t *testing.T) {
	worker := Worker{Engine: "tracker", Worktree: "/repo/fabricated"}
	if CapabilitiesForWorker(worker).Has(CapabilityManagedWorktree) {
		t.Fatal("fabricated worktree reported as managed")
	}
}
