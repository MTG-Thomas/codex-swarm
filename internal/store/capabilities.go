package store

import "strings"

// RuntimeCapability is a stable behavioral contract independent of engine identity.
type RuntimeCapability string

const (
	CapabilityLiveMessage         RuntimeCapability = "live_message"
	CapabilityResume              RuntimeCapability = "resume"
	CapabilityManagedWorktree     RuntimeCapability = "managed_worktree"
	CapabilityAutomaticCompletion RuntimeCapability = "automatic_completion"
	CapabilityExternalTracker     RuntimeCapability = "external_tracker"
)

// RuntimeCapabilities is a deterministic capability set for operator and protocol output.
type RuntimeCapabilities []RuntimeCapability

// Has reports whether the set includes capability.
func (c RuntimeCapabilities) Has(capability RuntimeCapability) bool {
	for _, candidate := range c {
		if candidate == capability {
			return true
		}
	}
	return false
}

// Strings returns a protocol-safe copy of the capability names.
func (c RuntimeCapabilities) Strings() []string {
	result := make([]string, 0, len(c))
	for _, capability := range c {
		result = append(result, string(capability))
	}
	return result
}

// EngineCapabilities is the canonical compatibility seam between an engine
// adapter and stable coordination behavior. New engines extend this projection
// instead of changing status or daemon protocol shapes.
func EngineCapabilities(engine string) RuntimeCapabilities {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "appserver":
		return RuntimeCapabilities{CapabilityLiveMessage, CapabilityResume, CapabilityAutomaticCompletion}
	case "mock":
		return RuntimeCapabilities{CapabilityAutomaticCompletion}
	case "tracker":
		return RuntimeCapabilities{CapabilityExternalTracker}
	default:
		return RuntimeCapabilities{}
	}
}

// CapabilitiesForWorker derives runtime behavior from the canonical engine
// profile plus durable worker evidence.
func CapabilitiesForWorker(worker Worker) RuntimeCapabilities {
	capabilities := append(RuntimeCapabilities(nil), EngineCapabilities(worker.Engine)...)
	if truthfulManagedWorktree(worker) {
		capabilities = append(capabilities, CapabilityManagedWorktree)
	}
	return capabilities
}

func truthfulManagedWorktree(worker Worker) bool {
	if strings.TrimSpace(worker.Worktree) == "" {
		return false
	}
	for _, event := range worker.Events {
		if event.Type == "worktree.created" {
			return true
		}
	}
	return false
}
