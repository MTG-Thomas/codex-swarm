package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Client is the narrow boundary around Codex app-server JSON-RPC.
type Client struct {
	in     io.Writer
	out    *json.Decoder
	nextID atomic.Int64
	once   sync.Once
	msgs   chan clientMessage
}

type clientMessage struct {
	response Response
	err      error
}

// CompletionPolicy controls how long a turn wait should rely on completion
// events versus an agent-emitted signal.
type CompletionPolicy struct {
	Signal            string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
}

// TurnCompletion is the observed or synthesized outcome of waiting for a turn.
type TurnCompletion struct {
	Turn    Turn
	Warning string
}

// NewClient creates a JSON-RPC client over the provided app-server streams.
func NewClient(in io.Writer, out io.Reader) *Client {
	return &Client{
		in:   in,
		out:  json.NewDecoder(out),
		msgs: make(chan clientMessage, 16),
	}
}

// Call sends a JSON-RPC request and waits for the matching response.
func (c *Client) Call(ctx context.Context, method string, params any) (*Response, error) {
	id := c.nextID.Add(1)
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := c.in.Write(data); err != nil {
		return nil, err
	}
	for {
		resp, err := c.readMessage(ctx)
		if err != nil {
			return nil, err
		}
		if resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("app-server %s failed: %s", method, resp.Error.Message)
			}
			return &resp, nil
		}
		if resp.ID != 0 && resp.Method != "" {
			_ = c.respond(resp.ID, map[string]string{"decision": "accept"})
		}
	}
}

// Notify sends a JSON-RPC notification without waiting for a response.
func (c *Client) Notify(method string, params any) error {
	req := Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.in.Write(data)
	return err
}

// Initialize performs the app-server initialize/initialized handshake.
func (c *Client) Initialize(ctx context.Context) error {
	resp, err := c.Call(ctx, "initialize", InitializeParams{
		ClientInfo: ClientInfo{
			Name:    "codex-swarm",
			Title:   "codex-swarm",
			Version: "0.1.0",
		},
	})
	if err != nil {
		return err
	}
	if len(resp.Result) == 0 {
		return fmt.Errorf("initialize returned empty result")
	}
	return c.Notify("initialized", map[string]any{})
}

// ThreadStart starts a Codex thread through the app-server.
func (c *Client) ThreadStart(ctx context.Context, params ThreadStartParams) (ThreadStartResponse, error) {
	resp, err := c.Call(ctx, "thread/start", params)
	if err != nil {
		return ThreadStartResponse{}, err
	}
	var result ThreadStartResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return ThreadStartResponse{}, fmt.Errorf("decode thread/start result: %w", err)
	}
	if result.Thread.ID == "" {
		return ThreadStartResponse{}, fmt.Errorf("thread/start returned empty thread id")
	}
	return result, nil
}

// ThreadResume resumes an existing Codex thread through the app-server.
func (c *Client) ThreadResume(ctx context.Context, threadID string) (ThreadStartResponse, error) {
	resp, err := c.Call(ctx, "thread/resume", map[string]string{"threadId": threadID})
	if err != nil {
		return ThreadStartResponse{}, err
	}
	var result ThreadStartResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return ThreadStartResponse{}, fmt.Errorf("decode thread/resume result: %w", err)
	}
	if result.Thread.ID == "" {
		result.Thread.ID = threadID
	}
	return result, nil
}

// TurnStart starts a new turn in an existing app-server thread.
func (c *Client) TurnStart(ctx context.Context, params TurnStartParams) (TurnStartResponse, error) {
	resp, err := c.Call(ctx, "turn/start", params)
	if err != nil {
		return TurnStartResponse{}, err
	}
	var result TurnStartResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return TurnStartResponse{}, fmt.Errorf("decode turn/start result: %w", err)
	}
	return result, nil
}

// WaitTurnCompleted waits for the app-server turn/completed event.
func (c *Client) WaitTurnCompleted(ctx context.Context, threadID, turnID string) (Turn, error) {
	result, err := c.WaitTurnCompletedWithPolicy(ctx, threadID, turnID, CompletionPolicy{})
	return result.Turn, err
}

// WaitTurnCompletedWithPolicy waits for a turn completion event with optional
// signal and grace-period handling for app-server finalization lag.
func (c *Client) WaitTurnCompletedWithPolicy(ctx context.Context, threadID, turnID string, policy CompletionPolicy) (TurnCompletion, error) {
	var idleTimer *time.Timer
	var idle <-chan time.Time
	if policy.Signal != "" && policy.IdleTimeout > 0 {
		idleTimer = time.NewTimer(policy.IdleTimeout)
		idle = idleTimer.C
		defer idleTimer.Stop()
	}
	var completionTimer *time.Timer
	var completion <-chan time.Time
	if policy.CompletionTimeout < 0 {
		policy.CompletionTimeout = 0
	}
	signaled := false
	warning := func(reason string) TurnCompletion {
		message := fmt.Sprintf("completion signal observed for thread %s turn %s", threadID, turnID)
		if reason != "" {
			message += ": " + reason
		}
		return TurnCompletion{
			Turn:    Turn{ID: turnID, Status: "completed"},
			Warning: message,
		}
	}

	for {
		select {
		case <-ctx.Done():
			return TurnCompletion{}, ctx.Err()
		case <-idle:
			return TurnCompletion{}, fmt.Errorf("completion signal %q not observed for thread %s turn %s before idle timeout %s", policy.Signal, threadID, turnID, policy.IdleTimeout)
		case <-completion:
			return warning(fmt.Sprintf("turn finalization did not arrive within %s", policy.CompletionTimeout)), nil
		case msg := <-c.messageChannel():
			if msg.err != nil {
				if signaled {
					return warning(fmt.Sprintf("turn finalization ended before completion metadata: %v", msg.err)), nil
				}
				return TurnCompletion{}, msg.err
			}
			if msg.response.ID != 0 && msg.response.Method != "" {
				_ = c.respond(msg.response.ID, map[string]string{"decision": "accept"})
				continue
			}
			if msg.response.Method == "turn/completed" {
				var params TurnCompletedParams
				if err := json.Unmarshal(msg.response.Params, &params); err != nil {
					if signaled {
						return warning(fmt.Sprintf("turn finalization ended before completion metadata: %v", err)), nil
					}
					return TurnCompletion{}, fmt.Errorf("decode turn/completed params: %w", err)
				}
				if params.ThreadID == threadID && params.Turn.ID == turnID {
					return TurnCompletion{Turn: params.Turn}, nil
				}
			}
			if msg.response.Method == "item/agentMessage/delta" && paramsContainSignal(msg.response.Params, threadID, policy.Signal) && !signaled {
				signaled = true
				if idleTimer != nil {
					idleTimer.Stop()
				}
				idle = nil
				if policy.CompletionTimeout == 0 {
					return warning("turn finalization was not awaited"), nil
				}
				completionTimer = time.NewTimer(policy.CompletionTimeout)
				defer completionTimer.Stop()
				completion = completionTimer.C
			}
		}
	}
}

func (c *Client) readMessage(ctx context.Context) (Response, error) {
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case msg := <-c.messageChannel():
		return msg.response, msg.err
	}
}

func (c *Client) messageChannel() <-chan clientMessage {
	c.once.Do(func() {
		go func() {
			for {
				var resp Response
				if err := c.out.Decode(&resp); err != nil {
					c.msgs <- clientMessage{err: err}
					return
				}
				c.msgs <- clientMessage{response: resp}
			}
		}()
	})
	return c.msgs
}

func paramsContainSignal(params json.RawMessage, threadID, signal string) bool {
	if signal == "" || !bytes.Contains(params, []byte(signal)) {
		return false
	}
	var envelope struct {
		ThreadID string `json:"threadId"`
		Delta    string `json:"delta"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return false
	}
	if envelope.ThreadID != threadID {
		return false
	}
	return strings.Contains(envelope.Delta, signal) || strings.Contains(envelope.Text, signal)
}

func (c *Client) respond(id int64, result any) error {
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.in.Write(data)
	return err
}

// Runner starts short-lived Codex app-server processes for CLI operations.
type Runner struct {
	Binary           string
	CompletionPolicy CompletionPolicy
}

// RunTurn creates a thread and submits a prompt as the first turn.
func (r Runner) RunTurn(ctx context.Context, cwd, prompt string) (RunResult, error) {
	return r.runTurn(ctx, cwd, "", prompt)
}

// SendTurn submits a prompt to an existing thread.
func (r Runner) SendTurn(ctx context.Context, cwd, threadID, prompt string) (RunResult, error) {
	if threadID == "" {
		return RunResult{}, fmt.Errorf("thread id is required")
	}
	return r.runTurn(ctx, cwd, threadID, prompt)
}

// Resume verifies that an existing app-server thread can be resumed.
func (r Runner) Resume(ctx context.Context, cwd, threadID string) (RunResult, error) {
	if threadID == "" {
		return RunResult{}, fmt.Errorf("thread id is required")
	}
	binary := r.Binary
	if binary == "" {
		binary = "codex"
	}

	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Dir = cwd
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("open app-server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("start codex app-server: %w", err)
	}
	defer closeProcess(stdin, cmd)

	client := NewClient(stdin, stdout)
	if err := client.Initialize(ctx); err != nil {
		return RunResult{}, err
	}
	thread, err := client.ThreadResume(ctx, threadID)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{ThreadID: thread.Thread.ID, Status: "resumed"}, nil
}

// Check verifies that the Codex app-server can be started and initialized.
func (r Runner) Check(ctx context.Context, cwd string) error {
	binary := r.Binary
	if binary == "" {
		binary = "codex"
	}

	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Dir = cwd
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open app-server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start codex app-server: %w", err)
	}
	defer closeProcess(stdin, cmd)

	return NewClient(stdin, stdout).Initialize(ctx)
}

func (r Runner) runTurn(ctx context.Context, cwd, threadID, prompt string) (RunResult, error) {
	binary := r.Binary
	if binary == "" {
		binary = "codex"
	}

	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Dir = cwd
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("open app-server stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("start codex app-server: %w", err)
	}
	defer closeProcess(stdin, cmd)

	client := NewClient(stdin, stdout)
	if err := client.Initialize(ctx); err != nil {
		return RunResult{}, err
	}
	if threadID == "" {
		thread, err := client.ThreadStart(ctx, ThreadStartParams{
			CWD:            cwd,
			ApprovalPolicy: "never",
			Sandbox:        "read-only",
			ServiceName:    "codex-swarm",
			ThreadSource:   "user",
		})
		if err != nil {
			return RunResult{}, err
		}
		threadID = thread.Thread.ID
	} else {
		if _, err := client.ThreadResume(ctx, threadID); err != nil {
			return RunResult{}, err
		}
	}
	turn, err := client.TurnStart(ctx, TurnStartParams{
		ThreadID: threadID,
		CWD:      cwd,
		Input: []UserInput{{
			Type: "text",
			Text: prompt,
		}},
	})
	if err != nil {
		return RunResult{}, err
	}
	completed, err := client.WaitTurnCompletedWithPolicy(ctx, threadID, turn.Turn.ID, r.CompletionPolicy)
	if err != nil {
		return RunResult{ThreadID: threadID, TurnID: turn.Turn.ID, Status: turn.Turn.Status}, err
	}
	turn.Turn = completed.Turn
	warnings := []string(nil)
	if completed.Warning != "" {
		warnings = append(warnings, completed.Warning)
	}
	return RunResult{
		ThreadID: threadID,
		TurnID:   turn.Turn.ID,
		Status:   turn.Turn.Status,
		Warnings: warnings,
	}, nil
}

func closeProcess(stdin io.Closer, cmd *exec.Cmd) {
	_ = stdin.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}
