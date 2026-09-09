---
name: codex-task-handoff
description: Send authorized messages to existing Codex tasks on the same or another host, using native tools or the swarm relay, and verify actual receipt and completion.
---

# Existing-task messaging

Resolve the exact task and owning host through task tools or enrolled relay
metadata. A task URL does not identify its host; desktop `local` may refer to a
different machine than this shell. Discover available native messaging tools
before deciding they are missing.

Prefer native delivery when available. Otherwise, use the configured relay for
an enrolled destination: `cs relay tasks --host <relay-host-id>`, then
`cs relay send --host <relay-host-id> --thread <task-id> --request-id <uuid>
--message-file <utf8-file>`. Persist the UUID and exact file before sending. Check that the file contains
1–16,384 UTF-8 bytes. Persist the returned message ID alongside the host, task
and request IDs; use that message ID for lookup and receipts.
Load host credentials into the command environment privately; never print them.
Use `cs relay get --id <message-id>` to inspect progress. The integrated user-owned
`csd serve` receiver owns queue execution and its retry journal. Never send the
same payload through a second lane after uncertain delivery.

If the destination is unenrolled, enroll it only within operator authorization,
on its actual host as its owning user. Otherwise check `codex queue --help` on
that host and use an existing authorized transport to invoke the supported queue
command there. This fallback proves submission only. Verify actual receipt and
results through destination-host task readback or an explicit destination reply;
without that evidence, report submitted, never acknowledged or completed.
Missing sender-side tools do not prove delivery is impossible.
Never resume a remote task on the wrong host, create a replacement task, copy
credentials/session databases, or run the relay as root/SYSTEM.

Include scope, expected outcome, useful artifact references and receipt protocol.
Sender-local files are not shared artifacts. Pass payloads through argument arrays
or safe quoting, never interpolate them as shell code.

For a relay message actually observed by the destination task:

1. Read the message and verify its sender, target host and exact task ID.
2. Persist a fresh receipt UUID and call `cs relay receipt --id <message-id>
   --request-id <uuid> --status acknowledged --evidence <actual-task-receipt>`.
   Read back the expected identities and acknowledged state.
3. Perform the authorized work. When complete, use a separate persisted UUID
   with `--status completed` and actual result evidence; read back completion.
   A blocked or failed outcome must remain explicitly reported, never described
   as successful completion of the requested work.

Queue submission is not task receipt. Do not invent a turn ID; an observed
message in the exact task is usable evidence. Keep queued, submitted,
acknowledged and completed distinct. After an uncertain mutation, reuse its UUID
and identical content, inspecting current state before retrying. Native delivery
uses native task readback instead of fabricated relay receipts. Prefer compact
status snapshots; do not hold a foreground turn waiting for a callback that can
only be consumed after that turn ends.

The maintained codex-swarm guides are `docs/cross-host-relay.md` and
`docs/fleet-messaging.md`. Ordinary local records and conversation history stay
local; enrollment shares task metadata and explicit relay traffic only.
