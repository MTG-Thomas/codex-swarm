package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MTG-Thomas/codex-swarm/internal/protocol"
	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func (c cli) dispatch(args []string) error {
	if len(args) == 0 {
		return errors.New("dispatch requires <prepare|bind>")
	}
	switch args[0] {
	case "prepare":
		return c.dispatchPrepare(args[1:])
	case "bind":
		return c.dispatchBind(args[1:])
	default:
		return fmt.Errorf("unknown dispatch command %q", args[0])
	}
}

func (c cli) dispatchPrepare(args []string) error {
	fs := c.flagSet("dispatch prepare")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	repo := fs.String("repo", ".", "repository root")
	prompt := fs.String("prompt", "", "task prompt")
	role := fs.String("role", "", "worker role")
	parentID := fs.String("parent", "", "coordinator worker id")
	environmentValue := fs.String("environment", string(store.NativeTaskEnvironmentWorktree), "Codex task environment: worktree or local")
	requestIDValue := fs.String("request-id", "", "idempotency key")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*prompt) == "" {
		return errors.New("dispatch prepare requires --prompt")
	}
	environment := store.NativeTaskEnvironment(strings.ToLower(strings.TrimSpace(*environmentValue)))
	if environment != store.NativeTaskEnvironmentLocal && environment != store.NativeTaskEnvironmentWorktree {
		return errors.New("dispatch prepare --environment must be worktree or local")
	}
	repoRoot, err := filepath.Abs(*repo)
	if err != nil {
		return fmt.Errorf("resolve dispatch repo: %w", err)
	}
	resolvedStatePath, err := filepath.Abs(*statePath)
	if err != nil {
		return fmt.Errorf("resolve dispatch state path: %w", err)
	}
	now := c.now().UTC()
	requestID, err := c.requestID(*requestIDValue, now)
	if err != nil {
		return err
	}
	workerID, err := newWorkerID(now)
	if err != nil {
		return fmt.Errorf("generate native task worker id: %w", err)
	}
	worker := store.Worker{
		ID: workerID, ParentID: strings.TrimSpace(*parentID), Role: strings.TrimSpace(*role), ProjectRoot: repoRoot,
		Engine: "appserver", RuntimeOwner: store.RuntimeOwnerExternal, TaskEnvironment: environment,
		Status: store.WorkerPending, Prompt: strings.TrimSpace(*prompt), CreatedAt: now, UpdatedAt: now,
	}
	worker.ApplyStatusAt(store.WorkerPending, now)
	worker.LastMessage = fmt.Sprintf("native task reserved: environment=%s repo=%s", environment, repoRoot)
	worker.Events = append(worker.Events, store.Event{At: now, Type: "native.task.reserved", Message: worker.LastMessage, WorkerID: worker.ID, RequestID: requestID})
	fingerprint := dispatchFingerprint("prepare", repoRoot, worker.Prompt, worker.Role, worker.ParentID, string(environment))
	reservation, err := store.NewJSONStore(resolvedStatePath).ReserveNativeTask(store.NativeTaskReservationRequest{
		RequestID: requestID, Fingerprint: fingerprint, Worker: worker, At: now,
	})
	if err != nil {
		return fmt.Errorf("reserve native Codex task: %w", err)
	}
	worker = reservation.Worker
	create := protocol.NativeTaskCreateRequest{
		WorkerID: worker.ID, ParentWorkerID: worker.ParentID, ProjectRoot: worker.ProjectRoot,
		Prompt: nativeTaskPrompt(worker, resolvedStatePath), Environment: worker.TaskEnvironment,
		StatePath: resolvedStatePath, BindRequestID: "dispatch-bind-" + worker.ID,
	}
	response := protocol.DispatchPrepareResponse{Worker: worker, NativeTaskCreate: create, Replayed: reservation.Replayed}
	if *jsonOutput {
		return json.NewEncoder(c.out).Encode(response)
	}
	fmt.Fprintf(c.out, "reserved worker=%s environment=%s status=%s replayed=%t\n", worker.ID, worker.TaskEnvironment, displayWorkerStatus(worker), reservation.Replayed)
	fmt.Fprintf(c.out, "native_task_create_required worker=%s repo=%s environment=%s\n", worker.ID, worker.ProjectRoot, worker.TaskEnvironment)
	fmt.Fprintf(c.out, "  prompt=%s\n", create.Prompt)
	fmt.Fprintf(c.out, "  after_ready=cs dispatch bind --state %q --request-id %s --worker %s --host-id <host-id> --thread <thread-id> [--turn <turn-id>] --cwd <task-cwd> [--branch <branch>]\n", create.StatePath, create.BindRequestID, worker.ID)
	fmt.Fprintln(c.out, "  note=wait for a real thread-id; a client-thread-id from worktree setup is not bindable")
	return nil
}

func (c cli) dispatchBind(args []string) error {
	fs := c.flagSet("dispatch bind")
	statePath := fs.String("state", defaultStatePath(), "state file path")
	workerID := fs.String("worker", "", "reserved worker id")
	hostID := fs.String("host-id", "", "Codex host id returned by task creation")
	threadID := fs.String("thread", "", "Codex thread id returned by task creation")
	turnID := fs.String("turn", "", "active turn id from task readback")
	cwd := fs.String("cwd", "", "task cwd from task readback")
	branch := fs.String("branch", "", "host-created worktree branch")
	requestIDValue := fs.String("request-id", "", "idempotency key; defaults to one stable key per worker")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*workerID) == "" || strings.TrimSpace(*hostID) == "" || strings.TrimSpace(*threadID) == "" {
		return errors.New("dispatch bind requires --worker, --host-id, and --thread")
	}
	requestID := strings.TrimSpace(*requestIDValue)
	if requestID == "" {
		requestID = "dispatch-bind-" + strings.TrimSpace(*workerID)
	}
	now := c.now().UTC()
	fingerprint := dispatchFingerprint("bind", *workerID, *hostID, *threadID, *turnID, *cwd, *branch)
	result, err := store.NewJSONStore(*statePath).BindNativeTask(store.NativeTaskBindingRequest{
		RequestID: requestID, Fingerprint: fingerprint, WorkerID: *workerID, HostID: *hostID,
		ThreadID: *threadID, TurnID: *turnID, CWD: *cwd, Branch: *branch, At: now,
	})
	if err != nil {
		return fmt.Errorf("bind native Codex task worker=%s host=%s thread=%s: %w", strings.TrimSpace(*workerID), strings.TrimSpace(*hostID), strings.TrimSpace(*threadID), err)
	}
	response := protocol.DispatchBindResponse{Worker: result.Worker, Replayed: result.Replayed}
	if *jsonOutput {
		return json.NewEncoder(c.out).Encode(response)
	}
	fmt.Fprintf(c.out, "bound worker=%s host=%s thread=%s turn=%s status=%s worktree=%s branch=%s replayed=%t\n",
		result.Worker.ID, result.Worker.HostID, result.Worker.ThreadID, emptyDash(result.Worker.TurnID), displayWorkerStatus(result.Worker), emptyDash(result.Worker.Worktree), emptyDash(result.Worker.Branch), result.Replayed)
	return nil
}

func nativeTaskPrompt(worker store.Worker, statePath string) string {
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(worker.Prompt))
	builder.WriteString("\n\nCoordination contract:\n")
	fmt.Fprintf(&builder, "- Your existing codex-swarm worker ID is %s. Do not create or attach a duplicate worker.\n", worker.ID)
	if worker.ParentID != "" {
		fmt.Fprintf(&builder, "- Your coordinator worker is %s; preserve it as the parent.\n", worker.ParentID)
	}
	fmt.Fprintf(&builder, "- Use state %s for coordination commands.\n", statePath)
	fmt.Fprintf(&builder, "- The coordinator will bind your Codex identity after task creation. Before any coordination mutation or closeout, run cs show --state %s %s and confirm its thread matches CODEX_THREAD_ID; if it is still blank, wait briefly and retry.\n", statePath, worker.ID)
	fmt.Fprintf(&builder, "- Before meaningful edits, run cs worker check %s --repo %s and claim/touch the exact scope.\n", worker.ID, worker.ProjectRoot)
	fmt.Fprintf(&builder, "- Ensure CODEX_SWARM_DAEMON_URL serves state %s before closeout; otherwise unset it so close reads and forwards through this ledger directly.\n", statePath)
	fmt.Fprintf(&builder, "- Finish with cs close --json --state %s --note <summary> %s and deliver every returned native callback.\n", statePath, worker.ID)
	return builder.String()
}

func dispatchFingerprint(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(strings.TrimSpace(part)))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
