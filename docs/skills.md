# Repo-local skills

`codex-swarm` keeps skill curation local and explicit.

Promoted skills live in `.agents/skills/`:

- `codex-swarm-go-modern`
- `codex-swarm-go-cli-daemon`
- `codex-swarm-go-context-errors`
- `codex-swarm-go-testing`
- `codex-swarm-go-dependency-security`

Candidate provenance lives in `skill-bookshelf/`.

Policy:

- Do not install upstream skill packs globally for this repo.
- Do not run unpinned skill installers.
- Prefer compact repo-local skills adapted from reviewed sources.
- Preserve source/license attribution in `skill-bookshelf/manifest.yaml`.
- Promote only skills that match current `codex-swarm` surfaces.
- Future agents should start from `AGENTS.md`, then read only the relevant promoted skill files before editing.
