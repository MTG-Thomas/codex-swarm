package protocol

import "github.com/MTG-Thomas/codex-swarm/internal/store"

type MessageRequest struct {
	RequestID string            `json:"request_id"`
	Kind      store.MessageKind `json:"kind"`
	From      string            `json:"from"`
	To        string            `json:"to"`
	Body      string            `json:"body"`
}

type MessageResponse struct {
	Message        store.Message                 `json:"message"`
	Deliveries     []store.Delivery              `json:"deliveries"`
	NativeSteering []store.NativeSteeringRequest `json:"native_steering,omitempty"`
	Replayed       bool                          `json:"replayed"`
}

type InboxResponse struct {
	Messages []store.DeliveredMessage `json:"messages"`
}

type TouchRequest struct {
	RequestID string `json:"request_id"`
	WorkerID  string `json:"worker_id"`
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Operation string `json:"operation"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Intent    string `json:"intent,omitempty"`
}

type TouchResponse struct {
	Touch     store.FileTouch       `json:"touch"`
	Conflicts []store.TouchConflict `json:"conflicts"`
	Warnings  []MessageResponse     `json:"warnings,omitempty"`
}

type CompletionRequest struct {
	RequestID string `json:"request_id"`
	WorkerID  string `json:"worker_id"`
	Report    string `json:"report"`
}

type CompletionResponse struct {
	Forwarded bool             `json:"forwarded"`
	Message   *MessageResponse `json:"message,omitempty"`
}
