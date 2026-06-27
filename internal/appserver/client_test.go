package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
