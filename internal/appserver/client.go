package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync/atomic"
)

// Client is the narrow boundary around Codex app-server JSON-RPC.
type Client struct {
	in     io.Writer
	out    *json.Decoder
	nextID atomic.Int64
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
	type result struct {
		turn Turn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			var msg Response
			if err := c.out.Decode(&msg); err != nil {
				ch <- result{err: err}
				return
			}
			if msg.ID != 0 && msg.Method != "" {
				_ = c.respond(msg.ID, map[string]string{"decision": "accept"})
				continue
			}
			if msg.Method != "turn/completed" {
				continue
			}
			var params TurnCompletedParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				ch <- result{err: fmt.Errorf("decode turn/completed params: %w", err)}
				return
			}
			if params.ThreadID == threadID && params.Turn.ID == turnID {
				ch <- result{turn: params.Turn}
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return Turn{}, ctx.Err()
	case res := <-ch:
		return res.turn, res.err
	}
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
	Binary string
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
	completed, err := client.WaitTurnCompleted(ctx, threadID, turn.Turn.ID)
	if err == nil {
		turn.Turn = completed
	}
	return RunResult{
		ThreadID: threadID,
		TurnID:   turn.Turn.ID,
		Status:   turn.Turn.Status,
	}, nil
}

func closeProcess(stdin io.Closer, cmd *exec.Cmd) {
	_ = stdin.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}
