package protocol

import "time"

type EventEnvelope struct {
	Schema    string    `json:"schema"`
	Kind      string    `json:"kind"`
	At        time.Time `json:"at"`
	WorkerID  string    `json:"worker_id,omitempty"`
	Type      string    `json:"type"`
	Message   string    `json:"message,omitempty"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Issue     string    `json:"issue,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
}

type EventsResponse struct {
	Events []EventEnvelope `json:"events"`
}
