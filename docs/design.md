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

The first demoable slice intentionally uses a mock worker behind the same operator commands:

- `spawn` creates a worker record, thread placeholder, branch/worktree plan, and first mock event.
- `send` appends a message and mock completion event.
- `show` prints worker details and the event timeline.
- `report` records done/failed/idle state and a human-readable report.
- `status` lists current workers from local durable state.

This proves the operator workflow, persistence shape, and CLI contract before the daemon owns a long-running Codex app-server process.

Next real-worker slice:

1. Add an app-server runner that starts `codex app-server`.
2. Map `spawn` to `thread/start` plus `turn/start`.
3. Stream events into the same worker event model.
4. Keep `--mock` available for deterministic tests and demos.

## Dependency policy

Start with the Go standard library. Add dependencies only when they reduce operational risk, testing cost, or cross-platform edge cases enough to justify the extra moving part.
