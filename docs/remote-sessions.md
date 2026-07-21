# Remote Git Sessions

`cs spawn` can run a Codex app-server worker on an SSH host while retaining the
worker, claim, message, and PR ledger on the operator machine. Each worker gets
its own remote Git branch and checkout from the repository's current `origin`.

This is the Git-native authoring lane:

```text
remote Codex worker -> isolated branch/worktree -> GitHub PR -> merged branch
```

It is deliberately separate from `cs bifrost workspace`, which coordinates the
legacy Bifrost `_repo` changeset compatibility path. Bifrost Git sync consumes
an integration branch after merge; it is not the per-worker branch manager.

## Prerequisites

- The operator can reach the SSH target, optionally through a jump host.
- The remote host has `python3`, Git, and an authenticated Codex executable.
- The remote host can clone and push the repository. Configure a repository-
  scoped deploy key or another host-owned Git credential. `cs` does not copy or
  persist GitHub credentials.
- SSH host keys are already trusted by the operator.

## Start a worker

```powershell
cs spawn `
  --engine appserver `
  --repo C:\src\owner\repo `
  --worktree `
  --remote-host user@remote-host `
  --remote-jump user@jump-host `
  --remote-repo-url git@github.com:owner/repo.git `
  --remote-codex /home/user/.local/bin/codex `
  --remote-base main `
  --prompt "Implement the bounded change, commit it, and push the assigned branch."
```

`--repo` remains the local coordination root and provides the `origin` URL,
Git identity, repo hints, and issue context. The source checkout used by Codex
is remote. Use `--remote-repo-url` when the remote host needs a different
transport, such as an SSH deploy key while the local origin uses HTTPS. The
worker record stores only the host, jump host, remote worktree,
branch, repository URL, base ref, and Codex path.

The provider maintains a bare mirror under
`~/.local/share/codex-swarm/mirrors/` and a unique checkout under
`~/.local/share/codex-swarm/workspaces/<worker-id>`. Repository preparation is
serialized with a remote file lock. Each checkout keeps its own `.git`
directory inside the Codex writable root; this avoids sandbox writes into a
shared bare repository. Branches use `cs/<worker-id>`.

Remote Git workers run Codex with `danger-full-access` on the remote host so the
session can update `.git` and reach the repository remote. Use a dedicated host
and a repository-scoped deploy key or equivalent least-privilege credential.
The checkout remains isolated per worker, but the Codex sandbox is not the
credential boundary for this mode.

`cs send`, `cs resume`, and `cs inspect-thread` reuse the recorded SSH transport
and remote worktree.

## Boundaries

- Spawning does not push, open a PR, merge, sync Bifrost, or activate a
  deployment. Those remain explicit operations.
- Remote worktrees and branches are not deleted automatically. Cleanup is a
  separate destructive lifecycle operation and is intentionally outside this
  first provider slice.
- The SSH host owns authentication. Tokens and private keys must not be passed
  as CLI flags or written into the swarm ledger.
