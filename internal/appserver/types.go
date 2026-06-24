package appserver

// Request is the JSON-RPC envelope sent to `codex app-server`.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Response is the JSON-RPC envelope returned by `codex app-server`.
type Response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *Error         `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ThreadStartParams struct {
	CWD string `json:"cwd,omitempty"`
}

type TurnStartParams struct {
	ThreadID string `json:"thread_id"`
	Prompt   string `json:"prompt"`
}
