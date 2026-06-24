---
name: codex-swarm-go-dependency-security
description: Use when adding, updating, auditing, or reviewing codex-swarm Go dependencies, GitHub integrations, local daemon security boundaries, command execution, path handling, secrets, tokens, vulnerability scanning, or supply-chain risk.
---

# codex-swarm Dependencies and Security

Treat dependencies and skills as supply chain. Prefer the standard library until a dependency removes real cross-platform risk or stabilizes a durable boundary.

## Dependency Gate

Before adding a dependency, identify:

- what code it replaces
- why it is needed now
- Windows, macOS, and Linux support
- maintenance and license signals
- how it will be verified in CI

Run:

```text
go mod tidy
go mod verify
go test ./...
govulncheck ./...
```

## Security Review

- Validate paths before recursive file operations.
- Avoid shell string construction for user-controlled paths, prompts, issue titles, or branch names.
- Keep GitHub tokens, Codex session IDs, and local daemon credentials out of logs.
- Treat local daemon APIs as privileged even when bound to loopback or named pipes.
- Make destructive actions explicit and narrow: worktree removal, branch deletion, session archival, schedule mutation.
- Keep skill imports pinned and reviewed. Do not run unpinned skill installers for this repo.

## Source Inspiration

Adapted from `samber/cc-skills-golang` security/dependency-management guidance and `eduardo-sl/go-agent-skills` dependency-audit/security guidance.
