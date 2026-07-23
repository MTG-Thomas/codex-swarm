[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $(if ($env:RUNNER_TEMP) {
        $env:RUNNER_TEMP
    } else {
        [System.IO.Path]::GetTempPath()
    }) 'nsis'),
    [string]$Version = '3.12',
    [string]$Sha256 = '56581f90db321581c5381193d796fffcf2d24b2f8fed2160a6c6a3baa67f2c4f',
    [string]$Sha1 = '364fd795b0cafc1fbff3e966f103a8f8fc8fb7f1'
)

$ErrorActionPreference = 'Stop'

if ($env:OS -ne 'Windows_NT') {
    throw 'NSIS can only be bootstrapped on Windows.'
}

$curl = Get-Command curl.exe -ErrorAction SilentlyContinue
if (-not $curl) {
    throw 'curl.exe is required to download NSIS.'
}

$downloadDir = Join-Path ([System.IO.Path]::GetTempPath()) (
    'codex-swarm-nsis-{0}' -f [Guid]::NewGuid().ToString('N')
)
$archivePath = Join-Path $downloadDir "nsis-$Version.zip"
$downloadUrl = "https://downloads.sourceforge.net/project/nsis/NSIS%203/$Version/nsis-$Version.zip"
$compiler = Join-Path $InstallDir "nsis-$Version\makensis.exe"

try {
    New-Item -ItemType Directory -Path $downloadDir -Force | Out-Null
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

    & $curl.Source -L --fail --silent --show-error --retry 3 --retry-delay 2 `
        --output $archivePath $downloadUrl
    if ($LASTEXITCODE -ne 0) {
        throw "NSIS download failed with exit code $LASTEXITCODE."
    }

    $actualSha256 = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualSha256 -ne $Sha256.ToLowerInvariant()) {
        throw "NSIS SHA-256 mismatch: expected $Sha256, got $actualSha256."
    }

    $actualSha1 = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA1).Hash.ToLowerInvariant()
    if ($actualSha1 -ne $Sha1.ToLowerInvariant()) {
        throw "NSIS SHA-1 mismatch: expected $Sha1, got $actualSha1."
    }

    Expand-Archive -LiteralPath $archivePath -DestinationPath $InstallDir -Force
    if (-not (Test-Path -LiteralPath $compiler -PathType Leaf)) {
        throw "NSIS archive did not contain $compiler."
    }

    (Resolve-Path -LiteralPath $compiler).Path
} finally {
    if (Test-Path -LiteralPath $downloadDir) {
        Remove-Item -LiteralPath $downloadDir -Recurse -Force
    }
}
