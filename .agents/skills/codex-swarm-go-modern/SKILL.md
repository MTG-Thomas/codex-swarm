---
name: codex-swarm-go-modern
description: Use when editing or reviewing Go code in codex-swarm and deciding whether a modern Go language or standard-library feature is appropriate for the project go.mod version. Applies to idiomatic modernization, stdlib selection, and avoiding stale Go patterns while preserving clarity.
---

# codex-swarm Go Modernization

Inspect `go.mod` before recommending a language or standard-library feature. Do not use a feature just because the local toolchain supports it; use features compatible with the repo's declared Go version and CI.

## Rules

- Prefer boring, readable Go over novelty.
- Use modern stdlib helpers when they reduce code and remain obvious: `errors.Is/As`, `slices`, `maps`, `cmp`, typed atomics, and context cancellation causes when supported.
- Keep daemon lifecycle code explicit. Do not replace clear timer, goroutine, or cancellation ownership with clever helpers.
- Treat blanket modernization advice as suspect. Preserve intentional zero values, empty strings, nil slices, and operator-facing error text.
- Mention version-gated choices in reviews and PR notes.

## Avoid

- Shell activation snippets or automatic source rewriting.
- Adding third-party helpers when the standard library is enough.
- Using Go features newer than the repo's `go` directive without first updating CI and documenting the reason.

## Source Inspiration

Adapted from the assessment of JetBrains `go-modern-guidelines`, especially `use-modern-go`. Use the source as reference material, not an installed upstream dependency.
