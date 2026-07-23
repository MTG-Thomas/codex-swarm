[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $(if ($env:RUNNER_TEMP) {
        $env:RUNNER_TEMP
    } else {
        [System.IO.Path]::GetTempPath()
    }) 'inno-setup'),
    [string]$Version = '7.0.2',
    [string]$Sha256 = '5ad54ca3def786f8f4212552e54cc6d8d61329e2d24a1cfee0571d42c2684ff1'
)

$ErrorActionPreference = 'Stop'

if ($env:OS -ne 'Windows_NT') {
    throw 'Inno Setup can only be bootstrapped on Windows.'
}
if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    throw 'GitHub CLI (gh) is required to download and attest Inno Setup.'
}

$iscc = Join-Path $InstallDir 'ISCC.exe'
$downloadDir = Join-Path ([System.IO.Path]::GetTempPath()) (
    'codex-swarm-inno-{0}' -f [Guid]::NewGuid().ToString('N')
)
$assetName = "innosetup-$Version-x64.exe"
$tag = 'is-{0}' -f $Version.Replace('.', '_')
$assetPath = Join-Path $downloadDir $assetName

try {
    New-Item -ItemType Directory -Path $downloadDir -Force | Out-Null
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

    & gh release download $tag --repo jrsoftware/issrc --pattern $assetName --dir $downloadDir
    if ($LASTEXITCODE -ne 0) {
        throw "gh release download failed with exit code $LASTEXITCODE."
    }

    & gh release verify-asset $assetPath --repo jrsoftware/issrc
    if ($LASTEXITCODE -ne 0) {
        throw "GitHub attestation verification failed with exit code $LASTEXITCODE."
    }

    $actualSha256 = (Get-FileHash -LiteralPath $assetPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualSha256 -ne $Sha256.ToLowerInvariant()) {
        throw "Inno Setup SHA-256 mismatch: expected $Sha256, got $actualSha256."
    }

    $process = Start-Process -FilePath $assetPath -ArgumentList @(
        '/VERYSILENT',
        '/SUPPRESSMSGBOXES',
        '/NORESTART',
        '/PORTABLE=1',
        "/DIR=`"$InstallDir`""
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Inno Setup bootstrap failed with exit code $($process.ExitCode)."
    }
    if (-not (Test-Path -LiteralPath $iscc -PathType Leaf)) {
        throw "Inno Setup completed without creating $iscc."
    }

    (Resolve-Path -LiteralPath $iscc).Path
} finally {
    if (Test-Path -LiteralPath $downloadDir) {
        Remove-Item -LiteralPath $downloadDir -Recurse -Force
    }
}
