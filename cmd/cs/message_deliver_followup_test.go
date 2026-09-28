package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

const deliverFollowupTestThread = "11111111-1111-4111-8111-111111111111"

// runTestQueueHelper emulates the `codex` and `ssh` binaries for
// deliver-followup tests. CS_TEST_ARGV_FILE captures argv for boundary
// assertions, CS_TEST_STDIN_FILE captures stdin for remote assertions, and
// CS_TEST_QUEUE_MODE selects ok, ok-no-receipt, fail, or hang behavior.
func runTestQueueHelper() int {
	args := os.Args[1:]
	if path := os.Getenv("CS_TEST_ARGV_FILE"); path != "" {
		b, _ := json.Marshal(args)
		if err := os.WriteFile(path, b, 0600); err != nil {
			return 2
		}
	}
	if path := os.Getenv("CS_TEST_STDIN_FILE"); path != "" {
		data, _ := io.ReadAll(os.Stdin)
		if err := os.WriteFile(path, data, 0600); err != nil {
			return 2
		}
	}
	if len(args) > 0 && args[len(args)-1] == "--version" {
		version := os.Getenv("CS_TEST_CODEX_VERSION")
		if version == "" {
			version = "codex-cli 0.157.1"
		}
		fmt.Println(version)
		return 0
	}
	switch os.Getenv("CS_TEST_QUEUE_MODE") {
	case "fail":
		fmt.Fprintln(os.Stderr, "Error: failed to queue session message: no rollout found for thread")
		return 1
	case "hang":
		time.Sleep(30 * time.Second)
		return 0
	case "ok-no-receipt":
		fmt.Println("ok")
		return 0
	default:
		thread := ""
		for i, arg := range args {
			if arg == "--thread" && i+1 < len(args) {
				thread = args[i+1]
			}
		}
		fmt.Printf("Queued message 22222222-2222-4222-8222-222222222222 for thread %s.\n", thread)
		return 0
	}
}

func testBinary(t *testing.T) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func saveFollowupWorkers(t *testing.T, state string, now time.Time, mutate func(*store.Worker)) {
	t.Helper()
	st := store.NewJSONStore(state)
	to := store.Worker{
		ID: "w-to", Issue: "MTG-Thomas/codex-swarm#99", ProjectRoot: "/repo",
		Engine: "tracker", Status: store.WorkerIdle,
		HostID: "desktop-local", ThreadID: deliverFollowupTestThread,
		Prompt: "attached task", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if mutate != nil {
		mutate(&to)
	}
	if err := st.SaveWorkers(
		store.Worker{
			ID: "w-from", Issue: "MTG-Thomas/codex-swarm#99", ProjectRoot: "/repo",
			Engine: "mock", Status: store.WorkerIdle,
			Prompt: "sender", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		},
		to,
	); err != nil {
		t.Fatal(err)
	}
}

func sendFollowupEnvelope(t *testing.T, c cli, out *bytes.Buffer, state string) store.NativeFollowupRequest {
	t.Helper()
	out.Reset()
	if err := c.run([]string{"message", "--json", "--state", state, "--request-id", "followup-envelope", "w-from", "w-to", "respond and continue"}); err != nil {
		t.Fatal(err)
	}
	var response protocol.MessageResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("message JSON = %q: %v", out.String(), err)
	}
	if len(response.NativeFollowup) != 1 {
		t.Fatalf("native follow-ups = %#v", response.NativeFollowup)
	}
	return response.NativeFollowup[0]
}

func loadDelivery(t *testing.T, state, workerID, deliveryID string) store.DeliveredMessage {
	t.Helper()
	item, err := findWorkerDelivery(store.NewJSONStore(state), workerID, deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestDeliverFollowupLocalSuccessConfirmsDelivery(t *testing.T) {
	t.Setenv("CS_TEST_QUEUE_HELPER", "1")
	t.Setenv("CS_TEST_QUEUE_MODE", "ok")
	argvFile := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("CS_TEST_ARGV_FILE", argvFile)
	var out bytes.Buffer
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	saveFollowupWorkers(t, state, now, nil)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	envelope := sendFollowupEnvelope(t, c, &out, state)

	out.Reset()
	deliver := []string{"message", "deliver-followup", "--state", state,
		"--worker", "w-to", "--thread", deliverFollowupTestThread,
		"--via", "codex-queue", "--codex-binary", testBinary(t), envelope.DeliveryID}
	if err := c.run(deliver); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "confirmed native follow-up") || !strings.Contains(out.String(), "22222222-2222-4222-8222-222222222222") {
		t.Fatalf("deliver output = %q", out.String())
	}
	confirmed := loadDelivery(t, state, "w-to", envelope.DeliveryID)
	if confirmed.Delivery.State != store.DeliverySteered {
		t.Fatalf("delivery state = %s, want steered", confirmed.Delivery.State)
	}
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(raw, &argv); err != nil {
		t.Fatal(err)
	}
	// The queued prompt must be byte-identical to the native envelope the
	// owning host would inject, passed as one argv element without a shell.
	want := []string{"queue", "--thread", deliverFollowupTestThread, "--message", envelope.Prompt}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("queue argv = %#v, want %#v", argv, want)
	}
}

func TestDeliverFollowupRemoteSuccessUsesRecordedTransport(t *testing.T) {
	t.Setenv("CS_TEST_QUEUE_HELPER", "1")
	t.Setenv("CS_TEST_QUEUE_MODE", "ok")
	argvFile := filepath.Join(t.TempDir(), "args.json")
	stdinFile := filepath.Join(t.TempDir(), "stdin.bin")
	t.Setenv("CS_TEST_ARGV_FILE", argvFile)
	t.Setenv("CS_TEST_STDIN_FILE", stdinFile)
	var out bytes.Buffer
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	saveFollowupWorkers(t, state, now, func(w *store.Worker) {
		w.Remote = &store.RemoteExecution{Host: "agent@example", JumpHost: "jump@example", CodexBinary: "/opt/codex", RepoURL: "git@example/repo", BaseRef: "main"}
	})
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	envelope := sendFollowupEnvelope(t, c, &out, state)

	out.Reset()
	deliver := []string{"message", "deliver-followup", "--state", state,
		"--worker", "w-to", "--thread", deliverFollowupTestThread,
		"--via", "codex-queue", "--ssh-binary", testBinary(t), envelope.DeliveryID}
	if err := c.run(deliver); err != nil {
		t.Fatal(err)
	}
	confirmed := loadDelivery(t, state, "w-to", envelope.DeliveryID)
	if confirmed.Delivery.State != store.DeliverySteered {
		t.Fatalf("delivery state = %s, want steered", confirmed.Delivery.State)
	}
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(raw, &argv); err != nil {
		t.Fatal(err)
	}
	want := []string{"-o", "BatchMode=yes", "-J", "jump@example", "agent@example", "--",
		"xargs", "-0", "/opt/codex", "queue", "--thread", deliverFollowupTestThread, "--message"}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ssh argv = %#v, want %#v", argv, want)
	}
	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdin) != envelope.Prompt+"\x00" {
		t.Fatalf("ssh stdin = %q, want NUL-terminated envelope prompt", stdin)
	}
}

func TestDeliverFollowupUnsupportedCLIFailsBeforeDelivery(t *testing.T) {
	t.Setenv("CS_TEST_QUEUE_HELPER", "1")
	t.Setenv("CS_TEST_CODEX_VERSION", "codex-cli 0.148.0")
	var out bytes.Buffer
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	saveFollowupWorkers(t, state, now, nil)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	envelope := sendFollowupEnvelope(t, c, &out, state)

	err := c.run([]string{"message", "deliver-followup", "--state", state,
		"--worker", "w-to", "--thread", deliverFollowupTestThread,
		"--via", "codex-queue", "--codex-binary", testBinary(t), envelope.DeliveryID})
	if err == nil || !strings.Contains(err.Error(), "0.149.0") {
		t.Fatalf("unsupported CLI error = %v", err)
	}
	untouched := loadDelivery(t, state, "w-to", envelope.DeliveryID)
	if untouched.Delivery.State != store.DeliveryQueued || untouched.Delivery.LastError != "" || len(untouched.Delivery.History) != 1 {
		t.Fatalf("delivery changed before delivery attempt: %#v", untouched.Delivery)
	}
}

func TestDeliverFollowupDefiniteFailureRecordsFollowupFailed(t *testing.T) {
	t.Setenv("CS_TEST_QUEUE_HELPER", "1")
	t.Setenv("CS_TEST_QUEUE_MODE", "fail")
	var out bytes.Buffer
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	saveFollowupWorkers(t, state, now, nil)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	envelope := sendFollowupEnvelope(t, c, &out, state)

	out.Reset()
	err := c.run([]string{"message", "deliver-followup", "--state", state,
		"--worker", "w-to", "--thread", deliverFollowupTestThread,
		"--via", "codex-queue", "--codex-binary", testBinary(t), envelope.DeliveryID})
	if err == nil || !strings.Contains(err.Error(), "no rollout found") {
		t.Fatalf("delivery failure error = %v", err)
	}
	if !strings.Contains(out.String(), "recorded failure for native follow-up") {
		t.Fatalf("deliver output = %q", out.String())
	}
	failed := loadDelivery(t, state, "w-to", envelope.DeliveryID)
	if failed.Delivery.State != store.DeliveryQueued || !strings.Contains(failed.Delivery.LastError, "no rollout found") || len(failed.Delivery.History) != 2 {
		t.Fatalf("failed delivery = %#v", failed.Delivery)
	}
}

func TestDeliverFollowupAmbiguousOutcomeLeavesStateUntouched(t *testing.T) {
	t.Setenv("CS_TEST_QUEUE_HELPER", "1")
	t.Setenv("CS_TEST_QUEUE_MODE", "hang")
	var out bytes.Buffer
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := filepath.Join(t.TempDir(), "state.db")
	saveFollowupWorkers(t, state, now, nil)
	c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
	envelope := sendFollowupEnvelope(t, c, &out, state)

	err := c.run([]string{"message", "deliver-followup", "--state", state,
		"--worker", "w-to", "--thread", deliverFollowupTestThread, "--timeout", "300ms",
		"--via", "codex-queue", "--codex-binary", testBinary(t), envelope.DeliveryID})
	if err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("ambiguous error = %v", err)
	}
	// No automatic retry and no recorded transition: the operator must
	// inspect the destination thread before any resend.
	untouched := loadDelivery(t, state, "w-to", envelope.DeliveryID)
	if untouched.Delivery.State != store.DeliveryQueued || untouched.Delivery.LastError != "" || len(untouched.Delivery.History) != 1 {
		t.Fatalf("ambiguous delivery changed state: %#v", untouched.Delivery)
	}
}

func TestDeliverFollowupRefusals(t *testing.T) {
	t.Setenv("CS_TEST_QUEUE_HELPER", "1")
	argvFile := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("CS_TEST_ARGV_FILE", argvFile)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	t.Run("missing via", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "state.db")
		saveFollowupWorkers(t, state, now, nil)
		c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}
		err := c.run([]string{"message", "deliver-followup", "--state", state, "--worker", "w-to", "--thread", deliverFollowupTestThread, "d-1"})
		if err == nil || !strings.Contains(err.Error(), "--via codex-queue") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("unknown via", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "state.db")
		saveFollowupWorkers(t, state, now, nil)
		c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}
		err := c.run([]string{"message", "deliver-followup", "--state", state, "--worker", "w-to", "--thread", deliverFollowupTestThread, "--via", "pigeon", "d-1"})
		if err == nil || !strings.Contains(err.Error(), "--via codex-queue") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("active turn steering rejected", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "state.db")
		saveFollowupWorkers(t, state, now, func(w *store.Worker) {
			w.Engine = "appserver"
			w.RuntimeOwner = store.RuntimeOwnerExternal
			w.Status = store.WorkerRunning
			w.TurnID = "turn-active"
		})
		st := store.NewJSONStore(state)
		_, deliveries, _, err := st.CreateMessage(store.Message{ID: "m-steer", RequestID: "steer-1", Kind: store.MessageDirect, From: "w-from", Body: "steer", CreatedAt: now}, []string{"w-to"})
		if err != nil {
			t.Fatal(err)
		}
		c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}
		err = c.run([]string{"message", "deliver-followup", "--state", state,
			"--worker", "w-to", "--thread", deliverFollowupTestThread,
			"--via", "codex-queue", "--codex-binary", testBinary(t), deliveries[0].ID})
		if err == nil || !strings.Contains(err.Error(), "active turn") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("wrong thread refused", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "state.db")
		saveFollowupWorkers(t, state, now, nil)
		var out bytes.Buffer
		c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
		envelope := sendFollowupEnvelope(t, c, &out, state)
		err := c.run([]string{"message", "deliver-followup", "--state", state,
			"--worker", "w-to", "--thread", "22222222-2222-4222-8222-222222222222",
			"--via", "codex-queue", "--codex-binary", testBinary(t), envelope.DeliveryID})
		if err == nil || !strings.Contains(err.Error(), "worker runtime is thread=") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("non UUID thread refused", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "state.db")
		saveFollowupWorkers(t, state, now, func(w *store.Worker) { w.ThreadID = "desktop-session" })
		c := cli{out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: func() time.Time { return now }}
		st := store.NewJSONStore(state)
		_, deliveries, _, err := st.CreateMessage(store.Message{ID: "m-name", RequestID: "name-1", Kind: store.MessageDirect, From: "w-from", Body: "hi", CreatedAt: now}, []string{"w-to"})
		if err != nil {
			t.Fatal(err)
		}
		err = c.run([]string{"message", "deliver-followup", "--state", state,
			"--worker", "w-to", "--thread", "desktop-session",
			"--via", "codex-queue", "--codex-binary", testBinary(t), deliveries[0].ID})
		if err == nil || !strings.Contains(err.Error(), "not an exact thread UUID") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("already confirmed delivery is not resent", func(t *testing.T) {
		t.Setenv("CS_TEST_QUEUE_MODE", "ok")
		state := filepath.Join(t.TempDir(), "state.db")
		saveFollowupWorkers(t, state, now, nil)
		var out bytes.Buffer
		c := cli{out: &out, err: &bytes.Buffer{}, now: func() time.Time { return now }}
		envelope := sendFollowupEnvelope(t, c, &out, state)
		deliver := []string{"message", "deliver-followup", "--state", state,
			"--worker", "w-to", "--thread", deliverFollowupTestThread,
			"--via", "codex-queue", "--codex-binary", testBinary(t), envelope.DeliveryID}
		if err := c.run(deliver); err != nil {
			t.Fatal(err)
		}
		before := loadDelivery(t, state, "w-to", envelope.DeliveryID)
		err := c.run(deliver)
		if err == nil || !strings.Contains(err.Error(), "delivery state is steered") {
			t.Fatalf("error = %v", err)
		}
		after := loadDelivery(t, state, "w-to", envelope.DeliveryID)
		if len(after.Delivery.History) != len(before.Delivery.History) {
			t.Fatalf("resend changed history: %#v", after.Delivery)
		}
	})
}
