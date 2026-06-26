package appserver

import "encoding/json"

// Request is the JSON-RPC envelope sent to `codex app-server`.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method,omitempty"`
	Params  any    `json:"params,omitempty"`
	Result  any    `json:"result,omitempty"`
}

// Response is the JSON-RPC envelope returned by `codex app-server`.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ThreadStartParams struct {
	CWD            string `json:"cwd,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
	ServiceName    string `json:"serviceName,omitempty"`
	ThreadSource   string `json:"threadSource,omitempty"`
}

type TurnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []UserInput `json:"input"`
	CWD      string      `json:"cwd,omitempty"`
}

type UserInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type InitializeParams struct {
	ClientInfo   ClientInfo `json:"clientInfo"`
	Capabilities any        `json:"capabilities,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type ThreadStartResponse struct {
	Thread Thread `json:"thread"`
}

type TurnStartResponse struct {
	Turn Turn `json:"turn"`
}

type TurnCompletedParams struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type Thread struct {
	ID string `json:"id"`
}

type Turn struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Usage  json.RawMessage `json:"usage,omitempty"`
}

type RunResult struct {
	ThreadID string
	TurnID   string
	Status   string
	Warnings []string
}
