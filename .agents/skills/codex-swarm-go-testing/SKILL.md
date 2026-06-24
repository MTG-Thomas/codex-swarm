---
name: codex-swarm-go-testing
description: Use when adding or reviewing codex-swarm Go tests, table-driven tests, fake app-server tests, CLI command tests, daemon lifecycle tests, race tests, fuzz tests, or integration test boundaries.
---

# codex-swarm Go Testing

Test behavior at the smallest boundary that proves the contract. Prefer deterministic fakes before driving real Codex or GitHub.

## Patterns

- Use table-driven tests when cases share setup and assertion shape.
- Split tests when table cases need branching setup that hides the behavior.
- Test command parsing and output without invoking the real daemon when possible.
- Test `internal/appserver` against fake line-oriented JSON-RPC readers/writers before using `codex app-server`.
- Use temp dirs for worktree/store tests.
- Use integration build tags for tests that require real Codex, GitHub, or long-running daemons.

## Verification

Use targeted tests first, then:

```text
go test ./...
go test -race ./...
govulncheck ./...
```

Run `go test -race ./...` when changing worker lifecycle, daemon shutdown, JSON-RPC multiplexing, store access, or goroutine code.

## Avoid

- Sleeping as synchronization unless the behavior is explicitly time-based.
- Tests that depend on the user's live Codex session history by default.
- Golden output that makes CLI messages impossible to improve.

## Source Inspiration

Adapted from `samber/cc-skills-golang` testing guidance and `eduardo-sl/go-agent-skills` table-driven testing guidance.
