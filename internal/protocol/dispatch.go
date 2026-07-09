package protocol

import "encoding/json"

type MutationEnvelope struct {
	RequestID string          `json:"request_id"`
	Command   string          `json:"command"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type DispatchRequest struct {
	RequestID string   `json:"request_id"`
	Issue     string   `json:"issue"`
	Repo      string   `json:"repo"`
	Prompt    string   `json:"prompt"`
	Gates     []string `json:"gates,omitempty"`
}

type DispatchResponse struct {
	RequestID   string `json:"request_id"`
	Implementer string `json:"implementer"`
	Validator   string `json:"validator"`
	Replayed    bool   `json:"replayed"`
}
