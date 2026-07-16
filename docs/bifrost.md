# Bifrost remote changesets

`cs bifrost` coordinates concurrent remote-workspace sessions while the
Bifrost platform remains authoritative for authentication, revision checks,
leases, validation, activation, and Git operations. Codex-swarm stores only
the worker/scope linkage and the evidence returned by Bifrost; it does not
store Bifrost credentials or implement a second lock.

```powershell
cs bifrost inspect --target dev --scope features/vendor
cs bifrost begin --target dev --worker w-123 --scope features/vendor --base-revision abc --title "vendor pagination"
cs bifrost show <changeset-id>
cs bifrost validate <changeset-id>
cs bifrost commit --message "fix vendor pagination" <changeset-id>
cs bifrost abort <changeset-id>
```

All commands emit one JSON object to stdout. `--cli` selects the installed
Bifrost executable and `--api-base` overrides the default
`/api/workspace-changesets` route for compatibility testing. `--target` is
passed to the CLI as `BIFROST_TARGET` and recorded with the local changeset.

`commit` calls the platform's `activate` operation. It does not push unless
`--push` is explicit. Conflicts and failed compare-and-swap checks come from
the platform and must not be bypassed with swarm claims; claims remain
warning-only coordination hints.

The platform contract is intentionally narrow:

- `GET /api/workspace-changesets/inspect?scope=...`
- `POST /api/workspace-changesets`
- `GET /api/workspace-changesets/{id}` and `/{id}/diff`
- `POST /api/workspace-changesets/{id}/validate`
- `POST /api/workspace-changesets/{id}/activate`
- `POST /api/workspace-changesets/{id}/abort`

File staging is performed through Bifrost's changeset API/CLI and is not
reimplemented by codex-swarm.
