# Bifrost remote changesets

`cs bifrost` coordinates concurrent remote-workspace sessions while the
Bifrost platform remains authoritative for authentication, revision checks,
leases, validation, activation, and Git operations. Codex-swarm stores only
the worker/scope linkage and the evidence returned by Bifrost; it does not
store Bifrost credentials or implement a second lock.

```powershell
cs bifrost inspect --target https://dev.bifrost.example.com --scope features/vendor
cs bifrost begin --target https://dev.bifrost.example.com --worker w-123 --scope features/vendor --base-revision <64-character-revision> --title "vendor pagination"
cs bifrost show <changeset-id>
cs bifrost write --path features/vendor/workflows/pagination.py --file .\pagination.py --expected-hash <sha256> <changeset-id>
cs bifrost delete --path features/vendor/workflows/obsolete.py --expected-hash <sha256> <changeset-id>
cs bifrost diff <changeset-id>
cs bifrost validate <changeset-id>
cs bifrost commit --message "fix vendor pagination" <changeset-id>
cs bifrost abort <changeset-id>
```

All commands emit one JSON object to stdout. `--cli` selects the installed
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
