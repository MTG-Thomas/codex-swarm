package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCallSkipsNotificationsAndMatchesResponseID(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","method":"thread/updated","params":{}}
{"jsonrpc":"2.0","id":1,"result":{"ok":true}}
`)
	client := NewClient(&written, server)

	resp, err := client.Call(context.Background(), "test/method", map[string]string{"value": "x"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK = false, want true")
	}

	var req Request
	if err := json.Unmarshal(firstLine(t, written.String()), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Method != "test/method" || req.ID != 1 {
		t.Fatalf("request = %#v", req)
	}
}

func TestInitializeSendsInitializedNotification(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"codexHome":"C:\\Users\\ThomasBray\\.codex","platformFamily":"windows","platformOs":"windows","userAgent":"test"}}
`)
	client := NewClient(&written, server)

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(written.String()))
	if !scanner.Scan() {
		t.Fatal("missing initialize request")
	}
	if !scanner.Scan() {
		t.Fatal("missing initialized notification")
	}
	var note Request
	if err := json.Unmarshal(scanner.Bytes(), &note); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if note.Method != "initialized" || note.ID != 0 {
		t.Fatalf("notification = %#v", note)
	}
}

func TestThreadStartIncludesModelOverride(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"thread":{"id":"thread-1"}}}` + "\n")
	client := NewClient(&written, server)

	if _, err := client.ThreadStart(context.Background(), ThreadStartParams{CWD: `C:\repo`, Model: "gpt-5.6-luna"}); err != nil {
		t.Fatalf("ThreadStart() error = %v", err)
	}
	if got := written.String(); !strings.Contains(got, `"model":"gpt-5.6-luna"`) {
		t.Fatalf("thread/start request = %s, want model override", got)
	}
}

func TestThreadResumeReassertsNonInteractivePolicy(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"thread":{"id":"thread-1"}}}` + "\n")
	client := NewClient(&written, server)

	_, err := client.ThreadResume(context.Background(), ThreadResumeParams{
		ThreadID:       "thread-1",
		CWD:            "/workspace/task",
		ApprovalPolicy: "never",
		Sandbox:        "read-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := written.String()
	for _, expected := range []string{`"method":"thread/resume"`, `"threadId":"thread-1"`, `"approvalPolicy":"never"`, `"sandbox":"read-only"`, `"cwd":"/workspace/task"`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("thread/resume request = %s, missing %s", got, expected)
		}
	}
}

func TestRunnerResumePolicyDefaultsFailClosed(t *testing.T) {
	params := (Runner{}).threadResumeParams("/workspace/task", "thread-1")
	if params.ThreadID != "thread-1" || params.CWD != "/workspace/task" || params.ApprovalPolicy != "never" || params.Sandbox != "read-only" {
		t.Fatalf("resume params = %#v", params)
	}
	params = (Runner{Sandbox: "danger-full-access"}).threadResumeParams("/workspace/task", "thread-1")
	if params.ApprovalPolicy != "never" || params.Sandbox != "danger-full-access" {
		t.Fatalf("explicit resume params = %#v", params)
	}
}

func TestServerRequestsNeverReceiveApproval(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		method    string
		params    string
		want      string
		wantError string
	}{
		{"command approval", `"approval-1"`, "item/commandExecution/requestApproval", `{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}`, `"decision":"decline"`, ""},
		{"file approval", `22`, "item/fileChange/requestApproval", `{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}`, `"decision":"decline"`, ""},
		{"legacy patch approval", `"patch-1"`, "applyPatchApproval", `{"conversationId":"thread-1","callId":"call-1"}`, `"denied":{"rejection":"` + deniedServerRequest + `"}`, ""},
		{"legacy command approval", `24`, "execCommandApproval", `{"conversationId":"thread-1","callId":"call-1","cwd":"/workspace"}`, `"denied":{"rejection":"` + deniedServerRequest + `"}`, ""},
		{"MCP elicitation", `"mcp-1"`, "mcpServer/elicitation/request", `{"threadId":"thread-1","serverName":"example"}`, `"action":"decline"`, ""},
		{"permission escalation", `26`, "item/permissions/requestApproval", `{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","cwd":"/workspace"}`, "", `"code":-32001`},
		{"user input", `"input-1"`, "item/tool/requestUserInput", `{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}`, "", `"code":-32601`},
		{"dynamic tool", `28`, "item/tool/call", `{"threadId":"thread-1","turnId":"turn-1","callId":"call-1","tool":"unsafe","arguments":{}}`, "", `"code":-32601`},
		{"unknown", `"unknown-1"`, "future/unsafe", `{}`, "", `"code":-32601`},
		{"malformed approval", `30`, "item/commandExecution/requestApproval", `{"threadId":"thread-1"}`, "", `"code":-32602`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var written bytes.Buffer
			server := strings.NewReader(fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"method\":%q,\"params\":%s}\n{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n", tc.id, tc.method, tc.params))
			client := NewClient(&written, server)
			if _, err := client.Call(context.Background(), "test/method", map[string]string{"value": "x"}); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(strings.NewReader(written.String()))
			response := ""
			idNeedle := `"id":` + tc.id
			for scanner.Scan() {
				if strings.Contains(scanner.Text(), idNeedle) && !strings.Contains(scanner.Text(), `"method"`) {
					response = scanner.Text()
				}
			}
			if response == "" {
				t.Fatalf("missing server-request response for %s: %q", idNeedle, written.String())
			}
			if tc.want != "" && !strings.Contains(response, tc.want) {
				t.Fatalf("response = %s, missing %s", response, tc.want)
			}
			if tc.wantError != "" && !strings.Contains(response, tc.wantError) {
				t.Fatalf("response = %s, missing %s", response, tc.wantError)
			}
			if strings.Contains(response, `"decision":"accept`) || strings.Contains(response, `"decision":"approved`) {
				t.Fatalf("server request was approved: %s", response)
			}
		})
	}
}

func TestWaitTurnCompleted(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-1"}}
{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","items":[]}}}
`)
	client := NewClient(&written, server)

	turn, err := client.WaitTurnCompleted(context.Background(), "thread-1", "turn-1")
	if err != nil {
		t.Fatalf("WaitTurnCompleted() error = %v", err)
	}
	if turn.ID != "turn-1" || turn.Status != "completed" {
		t.Fatalf("turn = %#v", turn)
	}
}

func TestTurnSteerUsesExpectedTurnID(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{"turnId":"turn-1"}}
`)
	client := NewClient(&written, server)
	result, err := client.TurnSteer(context.Background(), TurnSteerParams{
		ThreadID: "thread-1", ExpectedTurnID: "turn-1", Input: []UserInput{{Type: "text", Text: "peer changed main.go"}},
	})
	if err != nil {
		t.Fatalf("TurnSteer() error = %v", err)
	}
	if result.TurnID != "turn-1" {
		t.Fatalf("TurnSteer() = %#v", result)
	}
	var request Request
	if err := json.Unmarshal(firstLine(t, written.String()), &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != "turn/steer" {
		t.Fatalf("request method = %q", request.Method)
	}
	params, err := json.Marshal(request.Params)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(params, []byte(`"expectedTurnId":"turn-1"`)) {
		t.Fatalf("request params = %s", params)
	}
}

func TestWaitTurnCompletedCapturesAgentMessageAndFileChanges(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-1","delta":"done"}}
{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thread-1","item":{"type":"fileChange","changes":[{"path":"internal/store/store.go","kind":"update"}]}}}
{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}
`)
	client := NewClient(&written, server)
	result, err := client.WaitTurnCompletedWithPolicy(context.Background(), "thread-1", "turn-1", CompletionPolicy{})
	if err != nil {
		t.Fatalf("WaitTurnCompletedWithPolicy() error = %v", err)
	}
	if result.FinalMessage != "done" || len(result.FileChanges) != 1 || result.FileChanges[0].Path != "internal/store/store.go" {
		t.Fatalf("completion = %#v", result)
	}
}

func TestWaitTurnCompletedSeparatesAgentMessageItems(t *testing.T) {
	var written bytes.Buffer
	server := strings.NewReader(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-1","delta":"waiting"}}
{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thread-1","item":{"type":"agentMessage","text":"waiting"}}}
{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-1","delta":"acknowledged"}}
{"jsonrpc":"2.0","method":"item/completed","params":{"threadId":"thread-1","item":{"type":"agentMessage","text":"acknowledged"}}}
{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}
`)
	client := NewClient(&written, server)
	result, err := client.WaitTurnCompletedWithPolicy(context.Background(), "thread-1", "turn-1", CompletionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "waiting\nacknowledged" {
		t.Fatalf("FinalMessage = %q", result.FinalMessage)
	}
}

func TestWaitTurnCompletedSteersQueuedDeliveryOnExistingConnection(t *testing.T) {
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	defer func() { _ = requestReader.Close() }()
	defer func() { _ = requestWriter.Close() }()
	defer func() { _ = responseReader.Close() }()
	defer func() { _ = responseWriter.Close() }()
	written := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(requestReader)
		if scanner.Scan() {
			written <- scanner.Text()
		}
		_, _ = responseWriter.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"turnId":"turn-1"}}` + "\n"))
		_, _ = responseWriter.Write([]byte(`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}` + "\n"))
	}()
	client := NewClient(requestWriter, responseReader)
	acknowledged := ""
	result, err := client.WaitTurnCompletedWithSteering(context.Background(), "thread-1", "turn-1", CompletionPolicy{}, SteeringPolicy{
		PollInterval: time.Millisecond,
		Source: func(context.Context) ([]SteerDelivery, error) {
			return []SteerDelivery{{ID: "delivery-1", Text: "peer conflict warning"}}, nil
		},
		Acknowledge: func(id string, err error) {
			if err == nil {
				acknowledged = id
			}
		},
	})
	if err != nil {
		t.Fatalf("WaitTurnCompletedWithSteering() error = %v", err)
	}
	if result.Turn.Status != "completed" || acknowledged != "delivery-1" {
		t.Fatalf("completion=%#v acknowledged=%q", result, acknowledged)
	}
	request := <-written
	if !strings.Contains(request, `"method":"turn/steer"`) || !strings.Contains(request, "peer conflict warning") {
		t.Fatalf("written request = %s", request)
	}
}

func TestWaitTurnCompletedWithPolicyReturnsWarningAfterCompletionSignalGrace(t *testing.T) {
	var written bytes.Buffer
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	go func() {
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-1","delta":"work is DONE"}}` + "\n"))
	}()
	client := NewClient(&written, reader)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := client.WaitTurnCompletedWithPolicy(ctx, "thread-1", "turn-1", CompletionPolicy{
		Signal:            "DONE",
		IdleTimeout:       time.Second,
		CompletionTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitTurnCompletedWithPolicy() error = %v", err)
	}
	if result.Turn.ID != "turn-1" || result.Turn.Status != "completed" {
		t.Fatalf("Turn = %#v, want synthetic completed turn", result.Turn)
	}
	if !strings.Contains(result.Warning, "completion signal") || !strings.Contains(result.Warning, "turn-1") {
		t.Fatalf("Warning = %q, want completion-signal warning with turn id", result.Warning)
	}
}

func TestWaitTurnCompletedWithPolicyFailsWithoutCompletionSignal(t *testing.T) {
	var written bytes.Buffer
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	client := NewClient(&written, reader)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.WaitTurnCompletedWithPolicy(ctx, "thread-1", "turn-1", CompletionPolicy{
		Signal:            "DONE",
		IdleTimeout:       10 * time.Millisecond,
		CompletionTimeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("WaitTurnCompletedWithPolicy() error = nil, want missing-signal timeout")
	}
	if !strings.Contains(err.Error(), "completion signal") {
		t.Fatalf("error = %v, want completion-signal timeout", err)
	}
}

func TestWaitTurnCompletedWithPolicyIgnoresSignalInUnrelatedNotification(t *testing.T) {
	var written bytes.Buffer
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	go func() {
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","method":"item/userMessage/delta","params":{"threadId":"thread-1","delta":"DONE"}}` + "\n"))
	}()
	client := NewClient(&written, reader)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.WaitTurnCompletedWithPolicy(ctx, "thread-1", "turn-1", CompletionPolicy{
		Signal:            "DONE",
		IdleTimeout:       10 * time.Millisecond,
		CompletionTimeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("WaitTurnCompletedWithPolicy() error = nil, want missing-signal timeout")
	}
	if !strings.Contains(err.Error(), "completion signal") {
		t.Fatalf("error = %v, want completion-signal timeout", err)
	}
}

func TestWaitTurnCompletedWithPolicyRequiresMatchingThreadForSignal(t *testing.T) {
	var written bytes.Buffer
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	go func() {
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"other-thread","delta":"DONE"}}` + "\n"))
	}()
	client := NewClient(&written, reader)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.WaitTurnCompletedWithPolicy(ctx, "thread-1", "turn-1", CompletionPolicy{
		Signal:            "DONE",
		IdleTimeout:       10 * time.Millisecond,
		CompletionTimeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("WaitTurnCompletedWithPolicy() error = nil, want missing-signal timeout")
	}
	if !strings.Contains(err.Error(), "completion signal") {
		t.Fatalf("error = %v, want completion-signal timeout", err)
	}
}

func TestWaitTurnCompletedWithPolicyRequiresThreadForSignal(t *testing.T) {
	var written bytes.Buffer
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	go func() {
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"delta":"DONE"}}` + "\n"))
	}()
	client := NewClient(&written, reader)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := client.WaitTurnCompletedWithPolicy(ctx, "thread-1", "turn-1", CompletionPolicy{
		Signal:            "DONE",
		IdleTimeout:       10 * time.Millisecond,
		CompletionTimeout: 10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("WaitTurnCompletedWithPolicy() error = nil, want missing-signal timeout")
	}
	if !strings.Contains(err.Error(), "completion signal") {
		t.Fatalf("error = %v, want completion-signal timeout", err)
	}
}

func TestWaitTurnCompletedWithPolicyPreservesTrailingMetadataWithinGrace(t *testing.T) {
	var written bytes.Buffer
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	go func() {
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","method":"item/agentMessage/delta","params":{"threadId":"thread-1","delta":"DONE"}}` + "\n"))
		time.Sleep(5 * time.Millisecond)
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed","usage":{"totalTokens":17}}}}` + "\n"))
	}()
	client := NewClient(&written, reader)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := client.WaitTurnCompletedWithPolicy(ctx, "thread-1", "turn-1", CompletionPolicy{
		Signal:            "DONE",
		IdleTimeout:       time.Second,
		CompletionTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitTurnCompletedWithPolicy() error = %v", err)
	}
	if result.Warning != "" {
		t.Fatalf("Warning = %q, want clean completion when metadata arrives within grace", result.Warning)
	}
	var usage struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.Unmarshal(result.Turn.Usage, &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	if usage.TotalTokens != 17 {
		t.Fatalf("usage.TotalTokens = %d, want 17", usage.TotalTokens)
	}
}

func firstLine(t *testing.T, value string) []byte {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(value))
	if !scanner.Scan() {
		t.Fatal("missing line")
	}
	return scanner.Bytes()
}
