#!/usr/bin/env pwsh
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $PSCommandPath
$RepoRoot = [System.IO.Path]::GetFullPath((Join-Path $ScriptDir ".."))
$TempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("codex-swarm-demo-" + [System.Guid]::NewGuid().ToString("N"))
$StateFile = Join-Path $TempRoot "state.json"
$Issue = "MTG-Thomas/codex-swarm#9"
$DemoWorktrees = New-Object System.Collections.Generic.List[string]
$DemoBranches = New-Object System.Collections.Generic.List[string]

function Test-IsPathUnder {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $fullRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd([char[]]@('\', '/'))
    if ($fullPath.Equals($fullRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $true
    }
    $prefix = $fullRoot + [System.IO.Path]::DirectorySeparatorChar
    return $fullPath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)
}

function Remove-EmptyDirectoryIfSafe {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Root
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        return
    }
    if (-not (Test-IsPathUnder -Path $Path -Root $Root)) {
        return
    }
    $children = @(Get-ChildItem -LiteralPath $Path -Force)
    if ($children.Count -eq 0) {
        Remove-Item -LiteralPath $Path -Force
    }
}

function Invoke-Cs {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$CsArgs)

    $output = & go run ./cmd/cs @CsArgs 2>&1
    if ($LASTEXITCODE -ne 0) {
        $text = ($output | Out-String).TrimEnd()
        throw "go run ./cmd/cs $($CsArgs -join ' ') failed with exit $LASTEXITCODE`n$text"
    }
    return ($output -join [System.Environment]::NewLine)
}

function Get-RegexGroup {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$Pattern,
        [Parameter(Mandatory = $true)][string]$GroupName
    )

    $match = [System.Text.RegularExpressions.Regex]::Match(
        $Text,
        $Pattern,
        [System.Text.RegularExpressions.RegexOptions]::Multiline
    )
    if (-not $match.Success) {
        throw "Could not parse $GroupName from:`n$Text"
    }
    return $match.Groups[$GroupName].Value
}

function Cleanup-Demo {
    $managedRoot = Join-Path $RepoRoot ".codex-swarm\worktrees"
    foreach ($worktree in $DemoWorktrees) {
        if ([string]::IsNullOrWhiteSpace($worktree)) {
            continue
        }
        $leaf = Split-Path -Leaf $worktree
        if ((Test-IsPathUnder -Path $worktree -Root $managedRoot) -and $leaf.StartsWith("w-", [System.StringComparison]::Ordinal)) {
            & git -C $RepoRoot worktree remove --force $worktree *> $null
            if (($LASTEXITCODE -ne 0) -and (Test-Path -LiteralPath $worktree -PathType Container)) {
                Remove-Item -LiteralPath $worktree -Recurse -Force
            }
        }
    }

    foreach ($branch in $DemoBranches) {
        if ($branch -notlike "cs/w-*") {
            continue
        }
        & git -C $RepoRoot show-ref --verify --quiet "refs/heads/$branch"
        if ($LASTEXITCODE -eq 0) {
            & git -C $RepoRoot branch -D $branch *> $null
        }
    }

    if ((Test-Path -LiteralPath $TempRoot) -and (Test-IsPathUnder -Path $TempRoot -Root ([System.IO.Path]::GetTempPath())) -and ((Split-Path -Leaf $TempRoot) -like "codex-swarm-demo-*")) {
        Remove-Item -LiteralPath $TempRoot -Recurse -Force
    }

    $codexSwarmRoot = Join-Path $RepoRoot ".codex-swarm"
    Remove-EmptyDirectoryIfSafe -Path (Join-Path $codexSwarmRoot "worktrees") -Root $codexSwarmRoot
    Remove-EmptyDirectoryIfSafe -Path (Join-Path $codexSwarmRoot "locks") -Root $codexSwarmRoot
    Remove-EmptyDirectoryIfSafe -Path $codexSwarmRoot -Root $RepoRoot
}

try {
    New-Item -ItemType Directory -Path $TempRoot -Force | Out-Null
    $utf8NoBom = New-Object System.Text.UTF8Encoding -ArgumentList $false
    [System.IO.File]::WriteAllText($StateFile, "{}" + [System.Environment]::NewLine, $utf8NoBom)
    Push-Location $RepoRoot

    $agentOutput = Invoke-Cs agent register --state $StateFile --name "friend-demo-coordinator" --role coordinator
    $coordinatorOutput = Invoke-Cs spawn --state $StateFile --repo $RepoRoot --engine mock --role coordinator --prompt "Friend demo coordinator: route the local smoke workflow."
    $coordinatorID = Get-RegexGroup -Text $coordinatorOutput -Pattern "^spawned\s+(?<id>w-\S+)" -GroupName "id"

    $workerOneOutput = Invoke-Cs spawn --state $StateFile --repo $RepoRoot --engine mock --role implementer --parent $coordinatorID --issue $Issue --prompt "Friend demo worker: own the issue-linked claim."
    $workerOneID = Get-RegexGroup -Text $workerOneOutput -Pattern "^spawned\s+(?<id>w-\S+)" -GroupName "id"

    $workerTwoOutput = Invoke-Cs spawn --state $StateFile --repo $RepoRoot --engine mock --role reviewer --parent $coordinatorID --worktree --prompt "Friend demo worker: use a managed worktree sandbox."
    $workerTwoID = Get-RegexGroup -Text $workerTwoOutput -Pattern "^spawned\s+(?<id>w-\S+)" -GroupName "id"
    $worktreePath = Get-RegexGroup -Text $workerTwoOutput -Pattern "^worktree:\s+(?<path>.+)\s+branch=(?<branch>\S+)\s*$" -GroupName "path"
    $worktreeBranch = Get-RegexGroup -Text $workerTwoOutput -Pattern "^worktree:\s+(?<path>.+)\s+branch=(?<branch>\S+)\s*$" -GroupName "branch"
    $DemoWorktrees.Add($worktreePath)
    $DemoBranches.Add($worktreeBranch)

    $claimOutput = Invoke-Cs claim create --state $StateFile --repo $RepoRoot --scope "Task 9 friend demo scripts" --worker $workerOneID --issue $Issue --note "friend-demo issue-linked smoke claim"
    $claimID = Get-RegexGroup -Text $claimOutput -Pattern "^claim\s+(?<id>c-\S+)" -GroupName "id"

    $messageOutput = Invoke-Cs message --state $StateFile $workerOneID $workerTwoID "Please review the friend-demo claim and status output."
    $statusOutput = Invoke-Cs status --state $StateFile

    Write-Output "codex-swarm friend demo"
    Write-Output "state=$StateFile"
    Write-Output "agent=$agentOutput"
    Write-Output "coordinator=$coordinatorID"
    Write-Output "workers=$workerOneID,$workerTwoID"
    Write-Output "claim=$claimID issue=$Issue"
    Write-Output "message=$messageOutput"
    Write-Output "worktree=$worktreePath branch=$worktreeBranch"
    Write-Output ""
    Write-Output "cs status:"
    Write-Output $statusOutput
}
finally {
    Pop-Location -ErrorAction SilentlyContinue
    Cleanup-Demo
}
