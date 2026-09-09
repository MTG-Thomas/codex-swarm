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
and enroll it within the operator's authorized scope. Enrollment can be explicit or performed by the opt-in SessionStart hook below. Do not wake every discovered
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

Remaining rollout work is host discovery and onboarding, installation/trust of
the policy-bound enrollment hook, and distribution of the handoff skill. Product follow-up
includes automated host-observed acknowledgments, retention with a replay horizon,
and an explicit export contract if local coordination records ever need sharing.
None of those future features is implied by enabling today's relay.

## Automatic enrollment at startup and resume

`cs relay session-start --config <absolute-daemon-relay.json> --policy
<absolute-enrollment-policy.json>` reads the Codex `SessionStart` JSON event on
stdin. It accepts only `startup` and `resume` with an exact session UUID. It
uses the supplied relay configuration, not relay credential environment variables.
No session database or transcript is read. The only new central task fields are
host ID, session UUID, a generic `Codex task <UUID>` title and enabled state.
Existing task titles are preserved.

Create a separate private policy file. `os_user` is the numeric UID on Unix or
the current user's SID on Windows, matching the journal identity:

```json
{"enabled": true, "host": "your-enrolled-host-id", "os_user": "your-user-id"}
```

The policy must match the configured relay host and actual process user. Installing
or configuring a receiver alone does not enable automatic enrollment. To stop new
automatic enrollment, set `enabled` to false; this does not revoke existing tasks.
Explicit `cs relay register --disabled` revocation is respected on later resumes,
as is a disabled central enrollment. Re-enable explicitly if desired. Root and
SYSTEM remain excluded. MTG's approved rollout is `codex-remote-01`,
`codex-remote-02`, and `thomas-windows`; `pve-t340` is excluded.

Merge this definition into the owning user's `~/.codex/hooks.json`, preserving
existing hooks. Replace paths with absolute installed files. Use a stable reviewed
binary path, not PATH lookup. The Windows command is a host-specific override;
quote paths according to the installed hook runner's shell when they contain spaces.

```json
{
  "hooks": {
    "SessionStart": [{
      "matcher": "^(startup|resume)$",
      "hooks": [{
        "type": "command",
        "command": "/absolute/cs relay session-start --config /absolute/daemon-relay.json --policy /absolute/enrollment-policy.json",
        "commandWindows": "C:\\absolute\\cs.exe relay session-start --config C:\\absolute\\daemon-relay.json --policy C:\\absolute\\enrollment-policy.json",
        "timeout": 15,
        "statusMessage": "Registering fleet mailbox"
      }]
    }]
  }
}
```

Codex's [hook documentation](https://learn.chatgpt.com/docs/hooks) describes
user hook discovery and trust. Review and trust this exact definition using
`/hooks`; untrusted definitions are skipped. Do not write Codex's trust store or
bypass hook trust to claim installation success. Changed definitions require
review again. Hook installation and actual trusted lifecycle execution are
separate acceptance steps.

The command has a ten-second operation deadline and a 64-KiB input limit. It
retries brief journal lock contention within that deadline, never queue delivery.
Runtime failures produce a generic Codex warning without blocking the user's task
or logging hook input or credentials; correct the configuration/connectivity and
retry on a later start/resume. Successful registration adds concise messaging
context to the task. No enrollment happens for compaction, subagent events, or
historical tasks merely discovered by an inventory. The hook never sends a message.

Validate with fake events/coordinator tests first. After native trust, use an
existing authorized task's natural resume, read back its exact enrollment and
confirm there were no unrelated task registrations. Removing only this matcher
and disabling the policy rolls back automatic enrollment while keeping the relay,
manual enrollment and existing messages available.
