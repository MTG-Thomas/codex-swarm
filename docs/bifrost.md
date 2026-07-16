# Bifrost remote development

## Solution deployments (experimental)

`cs bifrost deployment` follows the immutable `SolutionDeployment` contract.
The target is always a full instance URL. PR #454 does not yet expose source or
runtime artifact upload and its activation hooks may intentionally return 503,
so the write commands are capability-gated and must not be treated as a working
end-to-end deployment transport.

```powershell
cs bifrost deployment capabilities --target https://dev.bifrost.example.com --solution <solution-uuid>
```

Registration checks `registration`; activation and rollback check
`activation_configured` and stop after the capability preflight when it is
false. `artifact_upload`, `server_side_compilation`, and
`safe_for_end_to_end_cs_deploy` remain false in PR #454.

`prepare` deterministically packages the workspace and anchors an existing
Bifrost-compiled manifest and resolution map to a deployment UUID. Codex-swarm
does not invent workflow, agent, form, event, or application definitions.

```powershell
cs bifrost deployment prepare `
  --workspace . `
  --solution <solution-uuid> `
  --deployment <draft-uuid> `
  --base <active-deployment-uuid> `
  --compiled-manifest .\compiled-manifest.json `
  --resolution-map .\resolution-map.json `
  --out .\.artifacts\deployment-a
```

The output explicitly reports `upload_required: true`. Until an authenticated
upload API exists, upload `source.zip` and every referenced runtime object by an
approved external mechanism. Registration refuses to run without both an
explicit artifact assertion and the experimental capability acknowledgement:

```powershell
cs bifrost deployment register `
  --target https://dev.bifrost.example.com `
  --solution <solution-uuid> `
  --prepared .\.artifacts\deployment-a\prepared-deployment.json `
  --artifacts-uploaded `
  --experimental-solution-deployments

cs bifrost deployment inspect --target https://dev.bifrost.example.com --solution <solution-uuid> --deployment <draft-uuid>
cs bifrost deployment activate --target https://dev.bifrost.example.com --solution <solution-uuid> --deployment <draft-uuid> --expected-active <base-uuid> --experimental-solution-deployments
cs bifrost deployment rollback --target https://dev.bifrost.example.com --solution <solution-uuid> --deployment <prior-uuid> --expected-active <current-uuid> --experimental-solution-deployments
```

Two concurrent sessions prepare distinct deployment UUIDs with the same
`--base`. Activation sends that base as `expected_active_deployment_id`; the
first successful CAS wins and the second receives the platform's structured
409 conflict. Local swarm claims never override it. Authentication uses the
process environment's `BIFROST_ACCESS_TOKEN`; the token is not persisted.

Registration is idempotent only when the same deployment UUID describes the
same immutable closure. A divergent replay returns structured 409 evidence;
the client follows the advertised reconciliation GET once and reports the
existing bundle, manifest, and resolution hashes. It never overwrites or
silently treats a divergent closure as success.

## Workspace `_repo` compatibility

`cs bifrost workspace` coordinates loose `_repo` compatibility sessions while the
Bifrost platform remains authoritative for authentication, revision checks,
leases, validation, activation, and Git operations. Codex-swarm stores only
the worker/scope linkage and the evidence returned by Bifrost; it does not
store Bifrost credentials or implement a second lock.

```powershell
cs bifrost workspace inspect --target https://dev.bifrost.example.com --scope features/vendor
cs bifrost workspace begin --target https://dev.bifrost.example.com --worker w-123 --scope features/vendor --base-revision <64-character-revision> --title "vendor pagination"
cs bifrost workspace show <changeset-id>
cs bifrost workspace write --path features/vendor/workflows/pagination.py --file .\pagination.py --expected-hash <sha256> <changeset-id>
cs bifrost workspace delete --path features/vendor/workflows/obsolete.py --expected-hash <sha256> <changeset-id>
cs bifrost workspace diff <changeset-id>
cs bifrost workspace validate <changeset-id>
cs bifrost workspace commit --message "fix vendor pagination" <changeset-id>
cs bifrost workspace abort <changeset-id>
```

The pre-existing unqualified commands remain backward-compatible aliases. All
commands emit one JSON object to stdout. `--cli` selects the installed
Bifrost executable and `--api-base` overrides the default
`/api/workspace-changesets` route for compatibility testing. `--target` is the
Bifrost instance URL, passed to the CLI as `BIFROST_API_URL` and recorded with
the local changeset.

`commit` calls the platform's `activate` operation. It does not push unless
`--push` is explicit. Conflicts and failed compare-and-swap checks come from
the platform and must not be bypassed with swarm claims; claims remain
warning-only coordination hints.

The platform contract is intentionally narrow:

- `GET /api/workspace-changesets/state?scope=...`
- `POST /api/workspace-changesets`
- `GET /api/workspace-changesets/{id}` and `/{id}/diff`
- `POST /api/workspace-changesets/{id}/files`
- `POST /api/workspace-changesets/{id}/validate`
- `POST /api/workspace-changesets/{id}/activate`
- `POST /api/workspace-changesets/{id}/abort`

`write` uploads the exact bytes from `--file`; `delete` stages removal. Both
support `--expected-hash` for path-level compare-and-swap, and deletion only
sets `force_deactivation` when explicitly requested.
