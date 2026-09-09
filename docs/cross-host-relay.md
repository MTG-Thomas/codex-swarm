# Cross-host relay

The opt-in relay gives Windows, Linux and macOS hosts a shared task mailbox and
warning-only resource claims. A Cloudflare Worker owns its D1 records; a
user-owned `csd relay` polls outbound and invokes `codex queue` locally. Existing
`cs message`, native steering, the loopback daemon and the machine-local swarm
ledger keep their current behavior. This is an explicit additional execution
lane, not replication of `state.db` or the Codex session database.

## Configuration and enrollment

Build `cs` and `csd` from the same reviewed source. The host must have a Codex
CLI that supports `codex queue --thread --message` (verified with 0.153.4).
Run the receiver under the same OS user/profile as the destination task. Root
and SYSTEM execution is rejected. `csd relay` has no listener and is not
installed into the existing system service automatically.

Configure these environment variables in the sender/receiver user processes:

- `CODEX_SWARM_RELAY_URL`: authenticated coordinator HTTPS origin.
- `CODEX_SWARM_HOST_ID`: stable lowercase host ID, e.g. `thomas-windows`.
- `CODEX_SWARM_RELAY_TOKEN`: secret for that host (generate at least 32 random
  bytes). Keep it in the user's secret environment, not shell history or Git.

The coordinator's `HOST_KEYS_JSON` Worker secret maps each enrolled host ID to
the SHA-256 hex digest of its token. Tokens authorize participation in one
trusted swarm: members can send to enrolled tasks, list task metadata and see
shared claims. A host can read message bodies only when sender or destination;
only the destination reports delivery. Revoking a host removes its key from the
Worker secret. Credentials and session databases remain local.

From the **destination user session**, enroll each exact existing task:

```text
cs relay register --thread <task-uuid> --title "Existing task"
cs relay tasks --host thomas-windows
csd relay --codex <installed-codex-path>
```

Registration writes central discovery plus an independent local allowlist in
`relay.db`, next to the normal swarm ledger. The journal is bound to its exact
coordinator origin, stable host ID and OS user ID. Do not copy it between hosts
or delete it to clear uncertain deliveries. Concurrent journal owners fail
rather than compete. Register/revoke between polling cycles; a busy journal
returns an explicit error which can be retried.

```text
cs relay register --thread <task-uuid> --title "Existing task" --disabled
```

Revocation disables local execution first, then updates central enrollment.
It cannot cancel a submission already in progress. Enrollment is operator
permission to accept work from the trusted swarm, not a grant to bypass Codex
approval policy. Do not enroll tasks without authorization.

## Send and verify

Save the bounded handoff as a UTF-8 file (maximum 16 KiB). Include the intended
outcome, exact source/artifact references, scope, completed work not to repeat,
and where the receiver should return evidence. A sender-local file path is not
an artifact available on another host.

```text
cs relay send --request-id <new-uuid> --host thomas-windows --thread <task-uuid> --message-file handoff.txt
cs relay get --id <returned-message-id>
```

Reuse the **same request ID and exact content** after an uncertain send
response. Content changes under the same ID are rejected. The CLI does not
silently retry with a new ID. Offline senders retain their file/request ID and
retry explicitly; the receiver automatically retries its durable pending claim
and receipt after connectivity returns.

`csd relay --once` processes one delivery/recovery cycle. The default continuous
poll interval is 30 seconds; `--interval` accepts durations of at least 5 seconds.
Idle polls issue indexed reads without writing empty attempts. The queue adapter
uses argument arrays, a 30-second subprocess deadline, and bounded output. It
passes no model, permission, profile or CODEX_HOME overrides.

States have deliberately different meanings:

| State | Evidence |
| --- | --- |
| `queued` | Coordinator stored the handoff. |
| `dispatching` | One durable attempt owns it; submission may be in progress. |
| `submitted` | Codex CLI returned a matching queue submission ID. |
| `uncertain` | Submission may have happened, or local enrollment refused it; inspect before resending. |
| `acknowledged` | Destination observer supplied actual task receipt evidence. |
| `completed` | Destination observer supplied result evidence after acknowledgment. |

The queue does not promise immediate same-turn steering. Prefer native task
message tools when available and immediate steering is intended. Do not send
through native and relay lanes for the same work without reconciling delivery.

Read the destination with its owning Codex host's task tools. On the destination
host, record observed receipt and result with separate request UUIDs:

```text
cs relay receipt --id <message-id> --request-id <uuid> --status acknowledged --evidence "task:<uuid>/turn:<observed-turn>"
cs relay receipt --id <message-id> --request-id <another-uuid> --status completed --evidence "task:<uuid>/turn:<result-turn>"
cs relay inbox
```

These are explicit observer attestations; the service does not scrape Codex
sessions or independently validate a supplied evidence string. `cs relay tasks`,
`inbox` and `claims` automatically walk every returned page. Discovery is the
set of explicitly enrolled tasks, not an inventory of every Codex task.

## Recovery contract

Claim and receipt request IDs survive restarts in the local journal. A successful
queue receipt is saved before the HTTP report, so retrying that report never
resubmits the prompt. A process death after the `dispatching` journal commit
becomes `uncertain`, even if it happened just before process launch. This
conservative uncertainty window is intentional: Codex submission and the local
journal cannot commit atomically.

There is no automatic lease expiration/reassignment. A machine lost permanently
can leave a central `dispatching` message stranded; `cs relay inbox` exposes it.
Inspect the destination and local receipt first. If it arrived, attest receipt.
Only after proving it did not arrive may an operator explicitly send a new
message/request ID. Do not restore old journal backups into an active receiver
without reconciling central state. A ready attempt checks current central state
before executing so already-acknowledged work is not submitted again.

## Shared advisory claims

Use the same exact resource key on both machines. Prefer canonical repository
identity plus scope, not different OS filesystem paths:

```text
cs relay claim --request-id <uuid> --resource repo:github.com/MTG-Thomas/codex-swarm:path:internal/relay --note "Implement delivery adapter"
cs relay claims --resource repo:github.com/MTG-Thomas/codex-swarm:path:internal/relay
cs relay release --id <returned-claim-id>
```

Claims warn through `other_active_claims` and never reject overlapping ownership.
Only the creating host may release its claim. Matching is exact and case
sensitive; hierarchical file-scope inference and automatic local-claim export
are not implemented. Existing local claims remain authoritative locally.

## Worker deployment

Use Node 22+ (tested with 24.21.0) and the locked dependencies:

```text
cd coordinator
npm ci
npm test
npm run test:integration
npm run check
npx wrangler login --device
```

The checked-in `wrangler.jsonc` targets MTG account
`b5fc61a1bec847ecc83b3b880b563aaf`, D1 database
`ee70620a-8bf6-477a-a344-fedd79c84036`, and
`codex-swarm.midtowntg.com`. For another installation, create its own D1 database
and replace those bindings before deploying. `workers_dev` is disabled. Set `HOST_KEYS_JSON` with
`wrangler secret put HOST_KEYS_JSON` through its secret-input prompt. Do not
commit tokens, their map, `.dev.vars`, or local D1 state.

Apply the reviewed migration, then deploy the reviewed Worker:

```text
npx wrangler d1 migrations apply codex-swarm --remote
npx wrangler deploy
```

These are explicit account mutations. `npm run check` is only a local build
preview and proves neither account binding nor live deployment. The coordinator
uses D1's primary path without read-replica sessions so delivery decisions do
not rely on stale replicas. State transitions are guarded SQL in transactional
batches; the host never receives direct D1 or Cloudflare account credentials.

Disable receivers to stop automatic delivery; remove the Worker route/host key
to revoke network access. Preserve journals and D1 records during rollback so
old prompts do not become new work. Local-only swarm remains available.

## Storage, tests, and current scope

D1 stores bounded prompts, metadata, claims and receipt references. Large
transcripts, logs, artifacts and credentials do not belong here. Local pending
rows and completed D1 history are retained in this version; automatic retention
and compaction are not yet implemented. Monitor storage and daily row usage
before enabling this for a large fleet. Retention must preserve replay identity
or reject requests older than an explicit horizon before deleting dedupe rows.
Do not silently purge them and permit old work to execute again.

The free D1 cap is 500 MB per database, with 5 million rows read and 100,000 rows
written per day. Empty polling is read-only. Indexes reduce scanned rows but
also count toward writes and storage. See the current Cloudflare
[limits](https://developers.cloudflare.com/d1/platform/limits/) and
[pricing](https://developers.cloudflare.com/d1/platform/pricing/).

Go tests cover process argument boundaries, journal exclusivity, restart
recovery, lost responses and token redirect protection. Node tests use local
D1/workerd for concurrent claim acquisition, replay, authorization and state
transitions. The integration test builds the actual Go CLIs and delivers through
local D1 using a fake Codex binary. Real account deployment and real Windows
Codex receipt are separate acceptance gates; no test opens live tasks.
