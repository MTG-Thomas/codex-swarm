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

## Dependency policy

Start with the Go standard library. Add dependencies only when they reduce operational risk, testing cost, or cross-platform edge cases enough to justify the extra moving part.
