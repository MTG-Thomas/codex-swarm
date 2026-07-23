# codex-swarm roadmap

`codex-swarm` should become the dependable local coordination layer that makes
parallel Codex work safer and easier to resume. It should not become a general
agent platform or a second control plane for Git, GitHub, or Bifrost.

The current shipped baseline is summarized in [maturity.md](maturity.md). This
file contains unfinished work only.

## 1. Make coordination unavoidable in normal work

- Improve attachment and identity discovery so existing Codex tasks join the
  ledger without manual archaeology.
- Surface relevant claims, queued messages, file touches, and recent conflicts
  automatically at task start and resume.
- Feed the derived attention view into task start and resume without turning it
  into another source of truth.
- Consume and acknowledge native closeout callbacks automatically in Codex host
  entrypoints that expose a task-message tool.
- Measure coverage: active tasks attached, claims released, messages delivered,
  conflicts warned before Git, and workers closed with usable evidence.

Exit signal: running meaningful parallel work without swarm produces an
obvious loss of context or safety.

## 2. Strengthen daemon-owned delivery and recovery

- Recover active turn and delivery state after daemon restart without
  duplicating messages or confusing native CLI steering with runtime ownership.
- Stream bounded tool-intent and intermediate completion events into the
  durable event model; final agent responses are already retained.
- Improve health reporting for stalled delivery, dead turns, and inconsistent
  runtime ownership.

Exit signal: the daemon can restart without losing worker identity or silently
stranding messages and completions.

## 3. Improve conflict and ownership evidence

- Capture precise pre-edit read/write intent when Codex exposes a stable hook.
- Add operator-friendly conflict grouping across claims, file touches,
  worktrees, issues, and live-resource scopes.
- Preserve warning-only semantics while making the next action obvious.
- Add retention and compaction rules for high-volume touch and event history.

Exit signal: most overlapping work is visible before competing commits or live
mutations are attempted.

## 4. Complete scheduling deliberately

- Add daemon-owned run-now, enable, disable, missed-run, and concurrency policy.
- Require readiness and ownership checks before scheduled dispatch.
- Keep GitHub writes and platform mutations explicit in the scheduled task's
  declared authority.
- Record durable schedule, dispatch, completion, and suppression evidence.

Exit signal: routine repo hygiene and triage can run unattended without hidden
GitHub or platform mutation.

## 5. Harden cross-machine coordination

- Improve explicit issue-marker synchronization and conflict readback between
  machines without making GitHub authoritative.
- Carry capability, claim, gate, and handoff evidence across remote workers.
- Add safe, explicit cleanup for managed local and SSH worktrees.
- Keep credentials host-owned and out of the ledger.

Exit signal: a task can move between machines or remote execution hosts without
losing identity, ownership evidence, or the safe next action.

## 6. Maintain distribution and operator trust

- Keep cross-platform release builds, version metadata, changelogs, and local
  installation verification routine.
- Revisit binary signing only when an appropriate signing program and durable
  key-management path are available.
- Add dependencies only at proven cross-platform or API boundaries.
- Keep the README, agent instructions, architecture, maturity baseline, and
  CLI help synchronized with shipped behavior.

Exit signal: a new machine can install the latest release, understand the
operating model, and verify a healthy local service without reading source.
