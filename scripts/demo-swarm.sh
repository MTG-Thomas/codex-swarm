#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"

if ! command -v go >/dev/null 2>&1 && { [[ -n "${WSL_DISTRO_NAME:-}" ]] || uname -r 2>/dev/null | grep -qi microsoft; }; then
  if command -v powershell.exe >/dev/null 2>&1; then
    ps_script="$script_dir/demo-swarm.ps1"
    if command -v wslpath >/dev/null 2>&1; then
      ps_script="$(wslpath -w "$ps_script" | tr -d '\r')"
    fi
    exec powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$ps_script"
  fi
fi

temp_parent="${TMPDIR:-/tmp}"
temp_dir="$(mktemp -d "$temp_parent/codex-swarm-demo.XXXXXX")"
state_file="$temp_dir/state.json"
issue="MTG-Thomas/codex-swarm#9"
demo_worktrees=()
demo_branches=()
go_cmd=()

find_go() {
  if command -v go >/dev/null 2>&1; then
    printf '%s\n' "go"
    return 0
  fi
  if command -v go.exe >/dev/null 2>&1; then
    command -v go.exe
    return 0
  fi
  if [[ -x "/c/Program Files/Go/bin/go.exe" ]]; then
    printf '%s\n' "/c/Program Files/Go/bin/go.exe"
    return 0
  fi
  return 1
}

cleanup() {
  local managed_root="$repo_root/.codex-swarm/worktrees"
  local worktree branch leaf

  for worktree in "${demo_worktrees[@]:-}"; do
    [[ -n "$worktree" ]] || continue
    leaf="$(basename -- "$worktree")"
    if [[ "$worktree" == "$managed_root"/* && "$leaf" == w-* ]]; then
      git -C "$repo_root" worktree remove --force "$worktree" >/dev/null 2>&1 || true
      if [[ -d "$worktree" ]]; then
        rm -rf -- "$worktree"
      fi
    fi
  done

  for branch in "${demo_branches[@]:-}"; do
    [[ "$branch" == cs/w-* ]] || continue
    if git -C "$repo_root" show-ref --verify --quiet "refs/heads/$branch"; then
      git -C "$repo_root" branch -D "$branch" >/dev/null 2>&1 || true
    fi
  done

  case "$temp_dir" in
    "$temp_parent"/codex-swarm-demo.*) rm -rf -- "$temp_dir" ;;
  esac

  rmdir "$repo_root/.codex-swarm/worktrees" 2>/dev/null || true
  rmdir "$repo_root/.codex-swarm/locks" 2>/dev/null || true
  rmdir "$repo_root/.codex-swarm" 2>/dev/null || true
}
trap cleanup EXIT

run_cs() {
  local output
  if ! output="$("${go_cmd[@]}" run ./cmd/cs "$@" 2>&1)"; then
    printf '%s run ./cmd/cs %s failed\n%s\n' "${go_cmd[*]}" "$*" "$output" >&2
    return 1
  fi
  printf '%s\n' "$output"
}

spawned_id() {
  sed -nE 's/^spawned (w-[^[:space:]]+).*/\1/p' | head -n 1
}

claim_id() {
  sed -nE 's/^claim (c-[^[:space:]]+).*/\1/p' | head -n 1
}

printf '{}\n' > "$state_file"
cd "$repo_root"
if ! go_path="$(find_go)"; then
  printf 'go was not found on PATH; install Go or run scripts/demo-swarm.ps1 on Windows.\n' >&2
  exit 1
fi
go_cmd=("$go_path")

agent_output="$(run_cs agent register --state "$state_file" --name "friend-demo-coordinator" --role coordinator)"

coordinator_output="$(run_cs spawn --state "$state_file" --repo "$repo_root" --engine mock --role coordinator --prompt "Friend demo coordinator: route the local smoke workflow.")"
coordinator_id="$(printf '%s\n' "$coordinator_output" | spawned_id)"

worker_one_output="$(run_cs spawn --state "$state_file" --repo "$repo_root" --engine mock --role implementer --parent "$coordinator_id" --issue "$issue" --prompt "Friend demo worker: own the issue-linked claim.")"
worker_one_id="$(printf '%s\n' "$worker_one_output" | spawned_id)"

worker_two_output="$(run_cs spawn --state "$state_file" --repo "$repo_root" --engine mock --role reviewer --parent "$coordinator_id" --worktree --prompt "Friend demo worker: use a managed worktree sandbox.")"
worker_two_id="$(printf '%s\n' "$worker_two_output" | spawned_id)"
worktree_line="$(printf '%s\n' "$worker_two_output" | sed -nE 's/^worktree: (.*) branch=([^[:space:]]+)$/\1	\2/p' | head -n 1)"
worktree_path="${worktree_line%	*}"
worktree_branch="${worktree_line##*	}"
demo_worktrees+=("$worktree_path")
demo_branches+=("$worktree_branch")

claim_output="$(run_cs claim create --state "$state_file" --repo "$repo_root" --scope "Task 9 friend demo scripts" --worker "$worker_one_id" --issue "$issue" --note "friend-demo issue-linked smoke claim")"
claim_id_value="$(printf '%s\n' "$claim_output" | claim_id)"

message_output="$(run_cs message --state "$state_file" "$worker_one_id" "$worker_two_id" "Please review the friend-demo claim and status output.")"
status_output="$(run_cs status --state "$state_file")"

printf 'codex-swarm friend demo\n'
printf 'state=%s\n' "$state_file"
printf 'agent=%s\n' "$agent_output"
printf 'coordinator=%s\n' "$coordinator_id"
printf 'workers=%s,%s\n' "$worker_one_id" "$worker_two_id"
printf 'claim=%s issue=%s\n' "$claim_id_value" "$issue"
printf 'message=%s\n' "$message_output"
printf 'worktree=%s branch=%s\n' "$worktree_path" "$worktree_branch"
printf '\ncs status:\n%s\n' "$status_output"
