package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
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

func firstLine(t *testing.T, value string) []byte {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(value))
	if !scanner.Scan() {
		t.Fatal("missing line")
	}
	return scanner.Bytes()
}
