package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// Client is the narrow boundary around Codex app-server JSON-RPC.
type Client struct {
	in     io.Writer
	out    *json.Decoder
	nextID atomic.Int64
}

type CompletionPolicy struct {
	Signal            string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
}

type TurnCompletion struct {
	Turn    Turn
	Warning string
}

func NewClient(in io.Writer, out io.Reader) *Client {
	return &Client{
		in:  in,
		out: json.NewDecoder(out),
	}
}

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

	type result struct {
		response *Response
		err      error
	}
	ch := make(chan result, 1)
	go func() {
		if _, err := c.in.Write(data); err != nil {
			ch <- result{err: err}
			return
		}
		for {
			var resp Response
			if err := c.out.Decode(&resp); err != nil {
				ch <- result{err: err}
				return
			}
			if resp.ID == id {
				if resp.Error != nil {
					ch <- result{err: fmt.Errorf("app-server %s failed: %s", method, resp.Error.Message)}
					return
				}
				ch <- result{response: &resp}
				return
			}
			if resp.ID != 0 && resp.Method != "" {
				_ = c.respond(resp.ID, map[string]string{"decision": "accept"})
			}
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.response, res.err
	}
}

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

func (c *Client) WaitTurnCompleted(ctx context.Context, threadID, turnID string) (Turn, error) {
	result, err := c.WaitTurnCompletedWithPolicy(ctx, threadID, turnID, CompletionPolicy{})
	return result.Turn, err
}

func (c *Client) WaitTurnCompletedWithPolicy(ctx context.Context, threadID, turnID string, policy CompletionPolicy) (TurnCompletion, error) {
	type result struct {
		turn   Turn
		signal bool
		err    error
	}
	ch := make(chan result, 1)
	send := func(res result) bool {
		select {
		case ch <- res:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		for {
			var msg Response
			if err := c.out.Decode(&msg); err != nil {
				send(result{err: err})
				return
			}
			if msg.ID != 0 && msg.Method != "" {
				_ = c.respond(msg.ID, map[string]string{"decision": "accept"})
				continue
			}
			if msg.Method == "item/agentMessage/delta" && paramsContainSignal(msg.Params, threadID, policy.Signal) {
				if !send(result{signal: true}) {
					return
				}
			}
			if msg.Method != "turn/completed" {
				continue
			}
			var params TurnCompletedParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				send(result{err: fmt.Errorf("decode turn/completed params: %w", err)})
				return
			}
			if params.ThreadID == threadID && params.Turn.ID == turnID {
				send(result{turn: params.Turn})
				return
			}
		}
	}()

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
		case res := <-ch:
			if res.err != nil {
				if signaled {
					return warning(fmt.Sprintf("turn finalization ended before completion metadata: %v", res.err)), nil
				}
				return TurnCompletion{}, res.err
			}
			if res.turn.ID != "" {
				return TurnCompletion{Turn: res.turn}, nil
			}
			if res.signal && !signaled {
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

type Runner struct {
	Binary           string
	CompletionPolicy CompletionPolicy
}

func (r Runner) RunTurn(ctx context.Context, cwd, prompt string) (RunResult, error) {
	return r.runTurn(ctx, cwd, "", prompt)
}

func (r Runner) SendTurn(ctx context.Context, cwd, threadID, prompt string) (RunResult, error) {
	if threadID == "" {
		return RunResult{}, fmt.Errorf("thread id is required")
	}
	return r.runTurn(ctx, cwd, threadID, prompt)
}

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
