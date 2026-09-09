package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("SWARM_TEST_QUEUE_HELPER") == "1" {
		args := os.Args[1:]
		if len(args) == 2 && args[0] == "queue" && args[1] == "--help" {
			fmt.Println("queue --thread --message")
			os.Exit(0)
		}
		if path := os.Getenv("SWARM_TEST_ARGV_FILE"); path != "" {
			b, _ := json.Marshal(args)
			if err := os.WriteFile(path, b, 0600); err != nil {
				os.Exit(2)
			}
		}
		if len(args) != 5 || args[0] != "queue" || args[1] != "--thread" || args[3] != "--message" {
			os.Exit(3)
		}
		fmt.Printf("Queued message 11111111-1111-4111-8111-111111111111 for thread %s.\n", args[2])
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestQueuePreservesArgumentBoundariesAndVerifiesReceipt(t *testing.T) {
	if err := CheckUser(); err != nil {
		t.Skip("privileged test process cannot invoke Codex adapter")
	}
	t.Setenv("SWARM_TEST_QUEUE_HELPER", "1")
	file := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("SWARM_TEST_ARGV_FILE", file)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	q := CodexQueue{Binary: binary}
	if err = q.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	thread := NewID()
	prompt := "quotes ' \" and $(not-a-command)\nsecond line; --dangerously-bypass-approvals-and-sandbox"
	id, err := q.Submit(context.Background(), thread, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if id != "11111111-1111-4111-8111-111111111111" {
		t.Fatal(id)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err = json.Unmarshal(b, &args); err != nil {
		t.Fatal(err)
	}
	if len(args) != 5 || args[4] != prompt {
		t.Fatalf("prompt became executable arguments: %#v", args)
	}
	if _, err = q.Submit(context.Background(), "not-a-uuid", prompt); err == nil {
		t.Fatal("accepted ambiguous task identity")
	}
}
func TestBoundedQueueOutput(t *testing.T) {
	var b boundedOutput
	payload := strings.Repeat("x", 20000)
	n, err := b.Write([]byte(payload))
	if err != nil || n != len(payload) || len(b.data) != 8192 {
		t.Fatal("queue output not bounded")
	}
}
