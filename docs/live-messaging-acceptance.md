# Live messaging acceptance

Use this runbook to prove that coordination messages reach real Codex tasks and
that the product-visible result agrees with the SQLite ledger. Run it with the
release-candidate `cs` and `csd` binaries against the machine-global state.

## Live path

1. Start real app-server worker A and worker B. Give B a harmless task that
   keeps its turn active long enough to receive steering.
2. Record both worker IDs plus B's thread and turn IDs from `cs show`.
3. Send a unique nonce and explicit request ID:

   ```powershell
   cs message --wait 10s --request-id accept-live-<nonce> `
     <worker-a> <worker-b> "Acknowledge <nonce> and report the sender ID."
   ```

4. Confirm the command reports one message ID, one delivery ID, and `steered`.
5. Inspect B's Codex task. The injected user message must visibly contain the
   nonce, sender worker ID, and message ID; B must acknowledge the nonce and
   perform only the harmless instruction.
6. Compare `cs inbox --json <worker-b>`, `cs transcript <worker-b>`, and the
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

For stale turns, disconnects, or steering errors, the delivery remains queued
with `last_error` and a timestamped transition. A later successful turn may
recover it without replacing its identity. Restarting `csd` must not remove the
message, its delivery, or its transition history.
