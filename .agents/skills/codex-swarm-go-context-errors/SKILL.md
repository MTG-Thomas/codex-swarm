---
name: codex-swarm-go-context-errors
description: Use when implementing or reviewing context propagation, cancellation, timeouts, goroutine ownership, error wrapping, error classification, logging, or operator-facing failure messages in codex-swarm Go code.
---

# codex-swarm Context and Errors

Context and errors are part of the operator contract. Make cancellation, timeouts, and failures visible enough to resume work safely.

## Context

- Accept `context.Context` as the first parameter for work that can block, call processes, perform I/O, or wait on workers.
- Do not store context in long-lived structs.
- Do not pass `nil` contexts.
- Call cancellation functions. Prefer `defer cancel()` near creation unless ownership is intentionally transferred.
- Preserve shutdown causes when they affect worker resume or reporting.
- Keep goroutine ownership explicit; every goroutine should have a clear stop path.

## Errors

- Wrap lower-level errors with `%w` when callers can act on them.
- Use `errors.Is` and `errors.As` for classification.
- Return errors once and log them once at the boundary that has useful context.
- Keep operator-facing messages specific: worker ID, repo path, thread ID, command, and next safe action when available.
- Avoid panic for expected process, file, network, JSON-RPC, or GitHub failures.

## Avoid

- Swallowing cancellation as generic failure.
- Adding third-party error packages before stdlib errors are insufficient.
- Comparing errors by string in tests unless the exact CLI output is the behavior under test.

## Source Inspiration

Adapted from `samber/cc-skills-golang` and `eduardo-sl/go-agent-skills` context/error guidance, with blanket rules softened for daemon/operator behavior.
