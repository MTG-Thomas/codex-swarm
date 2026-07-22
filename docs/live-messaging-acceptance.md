# Live messaging acceptance

Use this runbook to prove that coordination messages reach real Codex tasks and
that the product-visible result agrees with the SQLite ledger. Run it with the
release-candidate `cs` and `csd` binaries against the machine-global state.

## Live path

1. Start or identify real Codex task B. Give B a harmless task that keeps its
   turn active long enough to receive steering.
2. Attach B's real host, thread, and active turn IDs to an app-server worker.
   Create or attach worker A as the sender:

   ```powershell
   cs attach --worker <worker-b> --engine appserver `
     --repo <repo-b> --host-id <host-b> --thread <thread-b> --turn <turn-b>
   ```

3. Record both worker IDs plus B's thread and turn IDs from `cs show`.
4. Send a unique nonce and explicit request ID:

   ```powershell
   cs message --json --request-id accept-live-<nonce> `
     <worker-a> <worker-b> "Acknowledge <nonce> and report the sender ID."
   ```

5. Confirm the response reports one queued delivery and one `native_steering`
   request with matching state path, host, thread, turn, message, and delivery
   IDs.
6. From the Codex host that owns B, send the returned prompt to the returned
   task with Codex's native task-message tool. Only after that call succeeds,
   record readback with the exact runtime identity:

   ```powershell
   cs message confirm-steered --state <state-path> --worker <worker-b> `
     --thread <thread-b> --turn <turn-b> <delivery-id>
   ```

7. Inspect B's existing Codex task. The injected user message must appear in the
   same in-progress turn and visibly contain the
   nonce, sender worker ID, and message ID; B must acknowledge the nonce and
   perform only the harmless instruction.
8. Compare `cs inbox --json <worker-b>`, `cs transcript <worker-b>`, and the
   Codex task. Message/request/delivery IDs and timestamps must agree, and the
   transcript must retain B's final response.

## Queued path and replay

1. Send a second unique nonce while B has no active turn. Omit `--wait` and
   confirm `cs inbox --json <worker-b>` reports `queued`.
2. Run `cs send <worker-b> "Process the queued coordination message."`.
3. Confirm the same delivery becomes `delivered`, B visibly receives the nonce
   once, and B's final acknowledgement is present in `cs transcript`.
4. Repeat the original `cs message` with the same request ID and identical
   payload. It must report `replayed=true`, preserve the original message and
   delivery IDs, and add no delivery transition or second injected message.

## Failure evidence

For stale turns, disconnects, or steering errors, record the failed native call
with `cs message steering-failed` and the same worker/thread/turn identity. The
delivery remains queued with `last_error` and a timestamped transition. A later
successful replay may recover it without replacing its identity. Restarting
`csd` must not remove the message, its delivery, or its transition history.
