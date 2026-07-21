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
	in            io.Writer
	out           *json.Decoder
	nextID        atomic.Int64
	readOnce      sync.Once
	writeMu       sync.Mutex
	pendingMu     sync.Mutex
	pending       map[int64]chan clientMessage
	notifications chan clientMessage
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

type SteerDelivery struct {
	ID   string
	Text string
}

// SteeringPolicy polls durable deliveries while a turn is active and
// acknowledges only successful turn/steer calls.
type SteeringPolicy struct {
	PollInterval time.Duration
	Source       func(context.Context) ([]SteerDelivery, error)
	Acknowledge  func(string, error)
}

// TurnCompletion is the observed or synthesized outcome of waiting for a turn.
type TurnCompletion struct {
	Turn         Turn
	Warning      string
	FinalMessage string
	FileChanges  []FileChange
}

// NewClient creates a JSON-RPC client over the provided app-server streams.
func NewClient(in io.Writer, out io.Reader) *Client {
	return &Client{
		in:            in,
		out:           json.NewDecoder(out),
		pending:       make(map[int64]chan clientMessage),
		notifications: make(chan clientMessage, 64),
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

	response := make(chan clientMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = response
	c.pendingMu.Unlock()
	c.startReader()
	if err := c.write(data); err != nil {
		c.removePending(id)
		return nil, err
	}
	defer c.removePending(id)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case message := <-response:
		if message.err != nil {
			return nil, message.err
		}
		if message.response.Error != nil {
			return nil, fmt.Errorf("app-server %s failed: %s", method, message.response.Error.Message)
		}
		return &message.response, nil
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
	return c.write(data)
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

// TurnSteer appends input to a currently steerable turn.
func (c *Client) TurnSteer(ctx context.Context, params TurnSteerParams) (TurnSteerResponse, error) {
	resp, err := c.Call(ctx, "turn/steer", params)
	if err != nil {
		return TurnSteerResponse{}, err
	}
	var result TurnSteerResponse
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return TurnSteerResponse{}, fmt.Errorf("decode turn/steer result: %w", err)
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
	return c.WaitTurnCompletedWithSteering(ctx, threadID, turnID, policy, SteeringPolicy{})
}

// WaitTurnCompletedWithSteering waits for completion while injecting durable
// deliveries over the already-running app-server connection.
func (c *Client) WaitTurnCompletedWithSteering(ctx context.Context, threadID, turnID string, policy CompletionPolicy, steering SteeringPolicy) (TurnCompletion, error) {
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
	var finalMessage strings.Builder
	fileChanges := []FileChange(nil)
	steeringWarning := ""
	var steerTicker *time.Ticker
	var steerTick <-chan time.Time
	if steering.Source != nil {
		interval := steering.PollInterval
		if interval <= 0 {
			interval = 500 * time.Millisecond
		}
		steerTicker = time.NewTicker(interval)
		steerTick = steerTicker.C
		defer steerTicker.Stop()
	}
	warning := func(reason string) TurnCompletion {
		message := fmt.Sprintf("completion signal observed for thread %s turn %s", threadID, turnID)
		if reason != "" {
			message += ": " + reason
		}
		if steeringWarning != "" {
			message += "; delivery warning: " + steeringWarning
		}
		return TurnCompletion{
			Turn:         Turn{ID: turnID, Status: "completed"},
			Warning:      message,
			FinalMessage: finalMessage.String(),
			FileChanges:  fileChanges,
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
		case <-steerTick:
			deliveries, err := steering.Source(ctx)
			if err != nil {
				steeringWarning = err.Error()
				continue
			}
			for _, delivery := range deliveries {
				_, steerErr := c.TurnSteer(ctx, TurnSteerParams{
					ThreadID: threadID, ExpectedTurnID: turnID,
					Input: []UserInput{{Type: "text", Text: delivery.Text}},
				})
				if steering.Acknowledge != nil {
					steering.Acknowledge(delivery.ID, steerErr)
				}
				if steerErr != nil {
					steeringWarning = steerErr.Error()
				}
			}
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
			if msg.response.Method == "item/agentMessage/delta" {
				if delta := notificationText(msg.response.Params, threadID); delta != "" {
					finalMessage.WriteString(delta)
				}
			}
			if msg.response.Method == "item/completed" {
				text, changes := completedItem(msg.response.Params, threadID)
				if text != "" && finalMessage.Len() == 0 {
					finalMessage.WriteString(text)
				}
				fileChanges = append(fileChanges, changes...)
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
					return TurnCompletion{Turn: params.Turn, Warning: steeringWarning, FinalMessage: finalMessage.String(), FileChanges: fileChanges}, nil
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

func notificationText(params json.RawMessage, threadID string) string {
	var envelope struct {
		ThreadID string `json:"threadId"`
		Delta    string `json:"delta"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil || envelope.ThreadID != threadID {
		return ""
	}
	if envelope.Delta != "" {
		return envelope.Delta
	}
	return envelope.Text
}

func completedItem(params json.RawMessage, threadID string) (string, []FileChange) {
	var envelope struct {
		ThreadID string `json:"threadId"`
		Item     struct {
			Type    string       `json:"type"`
			Text    string       `json:"text"`
			Path    string       `json:"path"`
			Changes []FileChange `json:"changes"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil || envelope.ThreadID != threadID {
		return "", nil
	}
	switch envelope.Item.Type {
	case "agentMessage":
		return envelope.Item.Text, nil
	case "fileChange":
		if len(envelope.Item.Changes) > 0 {
			return "", envelope.Item.Changes
		}
		if envelope.Item.Path != "" {
			return "", []FileChange{{Path: envelope.Item.Path}}
		}
	}
	return "", nil
}

func (c *Client) messageChannel() <-chan clientMessage {
	c.startReader()
	return c.notifications
}

func (c *Client) startReader() {
	c.readOnce.Do(func() {
		go c.readLoop()
	})
}

func (c *Client) readLoop() {
	for {
		var resp Response
		if err := c.out.Decode(&resp); err != nil {
			c.pendingMu.Lock()
			for _, pending := range c.pending {
				select {
				case pending <- clientMessage{err: err}:
				default:
				}
			}
			c.pendingMu.Unlock()
			c.notifications <- clientMessage{err: err}
			return
		}
		if resp.ID != 0 && resp.Method != "" {
			_ = c.respond(resp.ID, map[string]string{"decision": "accept"})
			continue
		}
		if resp.ID != 0 {
			c.pendingMu.Lock()
			pending := c.pending[resp.ID]
			c.pendingMu.Unlock()
			if pending != nil {
				pending <- clientMessage{response: resp}
			}
			continue
		}
		c.notifications <- clientMessage{response: resp}
	}
}

func (c *Client) removePending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) write(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.in.Write(data)
	return err
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
	return c.write(data)
}

// Runner starts short-lived Codex app-server processes for CLI operations.
type Runner struct {
	Binary           string
	CompletionPolicy CompletionPolicy
}

// TurnObserver is called immediately after turn/start returns, before waiting
// for completion, so another process can discover and steer the active turn.
type TurnObserver func(RunResult) error

// SteerTurn asks Codex to append a message to an active turn. The caller owns
// fallback queueing when the turn is no longer steerable.
func (r Runner) SteerTurn(ctx context.Context, cwd, threadID, turnID, message string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("steer requires thread and turn ids")
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
		return fmt.Errorf("open app-server stdin for thread=%s turn=%s: %w", threadID, turnID, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open app-server stdout for thread=%s turn=%s: %w", threadID, turnID, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start codex app-server for thread=%s turn=%s: %w", threadID, turnID, err)
	}
	defer closeProcess(stdin, cmd)
	client := NewClient(stdin, stdout)
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize app-server for thread=%s turn=%s: %w", threadID, turnID, err)
	}
	if _, err := client.ThreadResume(ctx, threadID); err != nil {
		return fmt.Errorf("resume thread=%s before steer turn=%s: %w", threadID, turnID, err)
	}
	_, err = client.TurnSteer(ctx, TurnSteerParams{
		ThreadID:       threadID,
		ExpectedTurnID: turnID,
		Input:          []UserInput{{Type: "text", Text: message}},
	})
	if err != nil {
		return fmt.Errorf("steer thread=%s turn=%s: %w", threadID, turnID, err)
	}
	return nil
}

// RunTurn creates a thread and submits a prompt as the first turn.
func (r Runner) RunTurn(ctx context.Context, cwd, prompt string) (RunResult, error) {
	return r.runTurn(ctx, cwd, "", prompt, nil, SteeringPolicy{})
}

// RunTurnObserved is RunTurn with an active-turn persistence callback.
func (r Runner) RunTurnObserved(ctx context.Context, cwd, prompt string, observer TurnObserver) (RunResult, error) {
	return r.runTurn(ctx, cwd, "", prompt, observer, SteeringPolicy{})
}

// RunTurnCoordinated persists the active turn and polls daemon-routed deliveries.
func (r Runner) RunTurnCoordinated(ctx context.Context, cwd, prompt string, observer TurnObserver, steering SteeringPolicy) (RunResult, error) {
	return r.runTurn(ctx, cwd, "", prompt, observer, steering)
}

// SendTurn submits a prompt to an existing thread.
func (r Runner) SendTurn(ctx context.Context, cwd, threadID, prompt string) (RunResult, error) {
	if threadID == "" {
		return RunResult{}, fmt.Errorf("thread id is required")
	}
	return r.runTurn(ctx, cwd, threadID, prompt, nil, SteeringPolicy{})
}

// SendTurnObserved is SendTurn with an active-turn persistence callback.
func (r Runner) SendTurnObserved(ctx context.Context, cwd, threadID, prompt string, observer TurnObserver) (RunResult, error) {
	if threadID == "" {
		return RunResult{}, fmt.Errorf("thread id is required")
	}
	return r.runTurn(ctx, cwd, threadID, prompt, observer, SteeringPolicy{})
}

// SendTurnCoordinated resumes a thread and polls daemon-routed deliveries.
func (r Runner) SendTurnCoordinated(ctx context.Context, cwd, threadID, prompt string, observer TurnObserver, steering SteeringPolicy) (RunResult, error) {
	if threadID == "" {
		return RunResult{}, fmt.Errorf("thread id is required")
	}
	return r.runTurn(ctx, cwd, threadID, prompt, observer, steering)
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

func (r Runner) runTurn(ctx context.Context, cwd, threadID, prompt string, observer TurnObserver, steering SteeringPolicy) (RunResult, error) {
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
	started := RunResult{ThreadID: threadID, TurnID: turn.Turn.ID, Status: turn.Turn.Status}
	if observer != nil {
		if err := observer(started); err != nil {
			return started, fmt.Errorf("persist active app-server turn thread=%s turn=%s: %w", threadID, turn.Turn.ID, err)
		}
	}
	completed, err := client.WaitTurnCompletedWithSteering(ctx, threadID, turn.Turn.ID, r.CompletionPolicy, steering)
	if err != nil {
		return RunResult{ThreadID: threadID, TurnID: turn.Turn.ID, Status: turn.Turn.Status}, err
	}
	turn.Turn = completed.Turn
	warnings := []string(nil)
	if completed.Warning != "" {
		warnings = append(warnings, completed.Warning)
	}
	return RunResult{
		ThreadID:     threadID,
		TurnID:       turn.Turn.ID,
		Status:       turn.Turn.Status,
		Warnings:     warnings,
		FinalMessage: completed.FinalMessage,
		FileChanges:  completed.FileChanges,
	}, nil
}

func closeProcess(stdin io.Closer, cmd *exec.Cmd) {
	_ = stdin.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}
