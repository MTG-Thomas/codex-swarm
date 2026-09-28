package codexqueue

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

const testThread = "11111111-1111-4111-8111-111111111111"

func TestCheckVersion(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		wantErr bool
	}{
		{name: "current release", output: "codex-cli 0.157.1", wantErr: false},
		{name: "exact floor", output: "codex-cli 0.149.0", wantErr: false},
		{name: "newer minor", output: "codex-cli 0.150.2", wantErr: false},
		{name: "newer major", output: "codex-cli 1.0.0", wantErr: false},
		{name: "older minor", output: "codex-cli 0.148.9", wantErr: true},
		{name: "older major", output: "codex-cli 0.0.1", wantErr: true},
		{name: "unparseable", output: "codex version unknown", wantErr: true},
		{name: "empty", output: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckVersion(tc.output)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckVersion(%q) error = %v, wantErr = %v", tc.output, err, tc.wantErr)
			}
			if err != nil {
				var unsupported *UnsupportedError
				if !errors.As(err, &unsupported) {
					t.Fatalf("CheckVersion(%q) error type = %T, want *UnsupportedError", tc.output, err)
				}
				if !strings.Contains(err.Error(), MinVersion) {
					t.Fatalf("error %q does not name minimum version %s", err.Error(), MinVersion)
				}
			}
		})
	}
}

type capturedCall struct {
	name  string
	args  []string
	stdin []byte
}

func fakeRunner(calls *[]capturedCall, stdout string, err error) Runner {
	return func(_ context.Context, name string, args []string, stdin []byte) (Result, error) {
		*calls = append(*calls, capturedCall{name: name, args: append([]string{}, args...), stdin: append([]byte{}, stdin...)})
		if err != nil {
			return Result{}, err
		}
		return Result{Stdout: stdout}, nil
	}
}

func versionRunner(calls *[]capturedCall, version, queueStdout string, queueErr error) Runner {
	return func(ctx context.Context, name string, args []string, stdin []byte) (Result, error) {
		*calls = append(*calls, capturedCall{name: name, args: append([]string{}, args...), stdin: append([]byte{}, stdin...)})
		if len(args) > 0 && args[len(args)-1] == "--version" {
			return Result{Stdout: version}, nil
		}
		if queueErr != nil {
			return Result{}, queueErr
		}
		return Result{Stdout: queueStdout}, nil
	}
}

func TestSubmitLocalSuccessKeepsPromptAsOneArgument(t *testing.T) {
	var calls []capturedCall
	prompt := "quotes ' \" and $(not-a-command)\nsecond line; --danger"
	run := versionRunner(&calls, "codex-cli 0.157.1", "Queued message 22222222-2222-4222-8222-222222222222 for thread "+testThread+".\n", nil)
	id, err := Submit(context.Background(), run, Config{}, testThread, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if id != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("submission id = %q", id)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want version check plus queue invocation", len(calls))
	}
	queue := calls[1]
	if queue.name != "codex" {
		t.Fatalf("binary = %q", queue.name)
	}
	want := []string{"queue", "--thread", testThread, "--message", prompt}
	if strings.Join(queue.args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("queue args = %#v, want %#v", queue.args, want)
	}
	if len(queue.stdin) != 0 {
		t.Fatalf("local queue must not receive stdin, got %q", queue.stdin)
	}
}

func TestSubmitLocalSuccessWithoutReceiptIsStillSuccess(t *testing.T) {
	var calls []capturedCall
	run := versionRunner(&calls, "codex-cli 0.149.0", "ok\n", nil)
	id, err := Submit(context.Background(), run, Config{}, testThread, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("submission id = %q, want empty without a receipt", id)
	}
}

func TestSubmitRejectsSessionNames(t *testing.T) {
	var calls []capturedCall
	run := versionRunner(&calls, "codex-cli 0.157.1", "", nil)
	if _, err := Submit(context.Background(), run, Config{}, "my-session", "hello"); err == nil {
		t.Fatal("accepted a mutable session name")
	}
	if len(calls) != 0 {
		t.Fatalf("rejected submit invoked %d processes", len(calls))
	}
}

func TestSubmitChecksVersionBeforeDelivery(t *testing.T) {
	var calls []capturedCall
	run := versionRunner(&calls, "codex-cli 0.148.0", "", nil)
	_, err := Submit(context.Background(), run, Config{}, testThread, "hello")
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %v, want *UnsupportedError", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want only the version probe", len(calls))
	}
}

func TestSubmitDefiniteFailure(t *testing.T) {
	var calls []capturedCall
	run := func(ctx context.Context, name string, args []string, stdin []byte) (Result, error) {
		calls = append(calls, capturedCall{name: name, args: args})
		if len(args) > 0 && args[len(args)-1] == "--version" {
			return Result{Stdout: "codex-cli 0.157.1"}, nil
		}
		return Result{Stderr: "Error: no rollout found for thread id"}, &exec.ExitError{}
	}
	_, err := Submit(context.Background(), run, Config{}, testThread, "hello")
	if err == nil {
		t.Fatal("expected delivery failure")
	}
	var ambiguous *AmbiguousError
	if errors.As(err, &ambiguous) {
		t.Fatalf("definite failure misclassified as ambiguous: %v", err)
	}
	if !strings.Contains(err.Error(), "no rollout found") {
		t.Fatalf("error %q does not carry CLI evidence", err.Error())
	}
}

func TestSubmitAmbiguousOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := func(context.Context, string, []string, []byte) (Result, error) {
		return Result{}, context.Canceled
	}
	_, err := Submit(ctx, run, Config{}, testThread, "hello")
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %v, want *AmbiguousError", err)
	}
}

func TestSubmitRemoteUsesRecordedTransportAndStdin(t *testing.T) {
	var calls []capturedCall
	prompt := "follow up; $(touch /tmp/pwned) 'quoted'"
	run := versionRunner(&calls, "codex-cli 0.157.1", "", nil)
	cfg := Config{Remote: &Remote{Target: "agent@example", Jump: "jump@example", CodexBinary: "/opt/codex"}}
	if _, err := Submit(context.Background(), run, cfg, testThread, prompt); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want version probe plus queue invocation", len(calls))
	}
	queue := calls[1]
	if queue.name != "ssh" {
		t.Fatalf("binary = %q, want ssh", queue.name)
	}
	wantArgs := []string{"-o", "BatchMode=yes", "-J", "jump@example", "agent@example", "--",
		"xargs", "-0", "/opt/codex", "queue", "--thread", testThread, "--message"}
	if strings.Join(queue.args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("ssh args = %#v, want %#v", queue.args, wantArgs)
	}
	wantStdin := append([]byte(prompt), 0)
	if string(queue.stdin) != string(wantStdin) {
		t.Fatalf("ssh stdin = %q, want NUL-terminated prompt", queue.stdin)
	}
	for _, arg := range queue.args {
		if strings.Contains(arg, prompt) {
			t.Fatalf("prompt interpolated into ssh argv: %#v", queue.args)
		}
	}
	version := calls[0]
	if version.name != "ssh" || strings.Join(version.args, "\x00") != strings.Join([]string{"-o", "BatchMode=yes", "-J", "jump@example", "agent@example", "--", "/opt/codex", "--version"}, "\x00") {
		t.Fatalf("version probe = %s %#v", version.name, version.args)
	}
}

func TestSubmitRemoteRejectsBadTransport(t *testing.T) {
	cases := []struct {
		name   string
		remote Remote
	}{
		{name: "target with spaces", remote: Remote{Target: "agent; rm -rf ~"}},
		{name: "jump with spaces", remote: Remote{Target: "agent@example", Jump: "jump; evil"}},
		{name: "binary with spaces", remote: Remote{Target: "agent@example", CodexBinary: "codex;id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []capturedCall
			run := versionRunner(&calls, "codex-cli 0.157.1", "", nil)
			if _, err := Submit(context.Background(), run, Config{Remote: &tc.remote}, testThread, "hello"); err == nil {
				t.Fatal("accepted invalid remote transport")
			}
			if len(calls) != 0 {
				t.Fatalf("invalid transport invoked %d processes", len(calls))
			}
		})
	}
}

func TestSubmitRemoteVersionFailureRunsNothingElse(t *testing.T) {
	var calls []capturedCall
	run := versionRunner(&calls, "codex-cli 0.140.0", "", nil)
	_, err := Submit(context.Background(), run, Config{Remote: &Remote{Target: "agent@example"}}, testThread, "hello")
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %v, want *UnsupportedError", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want only the remote version probe", len(calls))
	}
}
