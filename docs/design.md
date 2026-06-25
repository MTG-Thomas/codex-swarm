# codex-swarm design sketch

## Goal

Build a small local daemon and CLI that use Codex's app-server JSON-RPC API as the agent execution primitive. The project should make Codex workers easier to run, resume, schedule, and connect to GitHub issues without carrying a full dashboard/plugin/runtime framework.

## Non-goals

- replacing Codex CLI or Codex app-server
- implementing a terminal multiplexer
- building a general agent marketplace
- requiring MCP for routine worker communication

## Shape

```text
cs CLI
  -> csd local daemon
      -> codex app-server child process
      -> local state store
      -> git worktrees
      -> optional GitHub integration
```

## Core records

- project: repository root, default branch, config path
- worker: local ID, project, worktree, branch, status
- codex thread: app-server thread ID, model, last event timestamp
- task: prompt, issue/PR links, lifecycle status, report summary

## Initial commands

```text
cs status
cs spawn --repo . --prompt "..."
cs send <worker> "..."
cs resume <worker>
cs report <worker> done
```

## MVP slice

The first demoable slice supports both a deterministic mock worker and a real `codex app-server` engine behind the same operator commands:

- `spawn --engine appserver` initializes `codex app-server`, starts a thread, sends the first turn, waits for `turn/completed`, and stores the real thread and turn IDs.
- `send` resumes the stored app-server thread, starts another turn, waits for completion, and appends the event.
- `resume` verifies that the stored app-server thread can be reattached.
- `show` prints worker details and the event timeline.
- `report` records done/failed/idle state and a human-readable report.
- `status` lists current workers from local durable state.
- `--engine mock` uses the same state model without live Codex calls.

This proves the operator workflow, persistence shape, real app-server protocol boundary, and CLI contract before the daemon owns a long-running app-server process.

Next real-worker slice:

1. Keep a daemon-owned app-server process alive instead of one-shot startup per command.
2. Stream assistant deltas and item events into the worker event model.
3. Add worktree creation and branch isolation.
4. Add GitHub issue linkage.

## Dependency policy

Start with the Go standard library. Add dependencies only when they reduce operational risk, testing cost, or cross-platform edge cases enough to justify the extra moving part.
