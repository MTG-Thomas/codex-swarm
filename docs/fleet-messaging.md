# Fleet messaging rollout

The shared relay is the fleet mailbox. Local swarm ledgers and Codex session
history remain local, including for enrolled tasks. D1 stores explicitly shared
task enrollment, messages, delivery evidence, and advisory resource claims.
Running the integrated receiver does not export ordinary swarm activity.

## Host onboarding

1. Verify the real hostname, non-root/non-SYSTEM task owner, installed Codex queue
   support, and the existing daemon, ledger and journal paths. Desktop `local`
   is relative to the desktop, not necessarily the agent shell. Keep the desktop
   host ID and relay host ID as separate fields in deployment evidence.
2. Provision a unique host credential through the coordinator administrator.
   Preserve existing host keys when adding its hash to `HOST_KEYS_JSON`. Deliver
   only that host's credential through an authenticated private channel; never
   clone another host's credentials or relay journal.
3. Install matching reviewed `cs` and `csd` binaries. Configure the user-owned
   integrated daemon following [cross-host-relay.md](cross-host-relay.md).
   Preserve existing local state. Verify startup, owner, health and one receiver
   after restart. On Windows use the user logon task and physically accessible
   paths; retain the disabled original service for rollback.
4. Install the repository's `.agents/skills/codex-task-handoff` in the owning
   user's Codex skills directory. Future sessions can discover it; an already
   running agent may need to read it explicitly.
5. Enroll each authorized existing task from its actual host and OS user with
   `cs relay register --thread <task-uuid> --title <non-sensitive-title>`.
   Read it back using `cs relay tasks --host <relay-host-id>`. The local allowlist
   and central enrollment must agree. Discovery alone does not enroll tasks.
6. Send a harmless acceptance message in each direction. Require actual task
   receipt, acknowledged readback, and completed readback, with separate stable
   receipt UUIDs. Retain message, host, task, submission and receipt IDs. A queued
   or submitted message is not proof that the destination agent consumed it.

## Agent operation

For a new or resumed task that needs fleet messaging, resolve its exact identity
and enroll it within the operator's authorized scope. Registration is explicit;
there is no automatic all-session enrollment hook. Do not wake every discovered
historical task to establish availability. A bounded desktop inventory is a
window, never proof of complete fleet coverage.

Use native messaging when available, otherwise the configured relay for enrolled
recipients; use the authorized destination-user queue fallback for unenrolled
recipients. Choose one lane for each payload. Preserve request IDs across uncertain
responses and inspect the durable record before retrying. Relay queue execution
belongs to the receiver. Never retry an uncertain Codex submission independently.

At receipt, the destination agent verifies the source, destination and task,
records `acknowledged` with actual observation evidence, and reads it back. After
finishing the requested work, it records `completed` with a different UUID and
result evidence. Completion is not an acceptance shortcut. For blocked work,
report the blocker without claiming the requested outcome completed.

## Rollout evidence and remaining work

Keep a nonsecret deployment record per host: desktop and relay identity, task
owner, reviewed binary revision, startup owner, ledger/journal paths, installed
skill revision, enrolled tasks and bidirectional receipt evidence. Record
unreachable or undiscovered hosts as unverified. Keep tokens out of this record.

Integrated Windows/Linux round-trip acceptance was verified on 2026-09-09 with
runtime commit `206238f8de70dd4f11dcbeed321cdac8b6f8841e`. That proves the
implementation on those hosts, not universal fleet enrollment.

Remaining rollout work is host discovery and onboarding, task-start enrollment
within explicit policy, and distribution of the handoff skill. Product follow-up
includes automated host-observed acknowledgments, retention with a replay horizon,
and an explicit export contract if local coordination records ever need sharing.
None of those future features is implied by enabling today's relay.
