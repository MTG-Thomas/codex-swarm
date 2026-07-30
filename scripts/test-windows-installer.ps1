[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$InstallerPath,
    [string]$ExpectedVersion = '',
    [string]$TestRoot = (Join-Path $(if ($env:RUNNER_TEMP) {
        $env:RUNNER_TEMP
    } else {
        [System.IO.Path]::GetTempPath()
    }) 'codex-swarm-installer-smoke')
)

$ErrorActionPreference = 'Stop'

if ($env:OS -ne 'Windows_NT') {
    throw 'The Windows installer smoke test must run on Windows.'
}

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

namespace CodexSwarmInstallerTest {
    public static class NativeMethods {
        [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
        public static extern IntPtr SendMessageTimeout(
            IntPtr window,
            uint message,
            UIntPtr wParam,
            string lParam,
            uint flags,
            uint timeout,
            out UIntPtr result);
    }
}
'@

$TestRoot = [System.IO.Path]::GetFullPath($TestRoot)
$allowedRoots = @(
    $env:RUNNER_TEMP
    [System.IO.Path]::GetTempPath()
) | Where-Object { $_ } | ForEach-Object {
    [System.IO.Path]::GetFullPath($_).TrimEnd('\', '/') + [System.IO.Path]::DirectorySeparatorChar
}
$isSafeTestRoot = $false
foreach ($allowedRoot in $allowedRoots) {
    if ($TestRoot.StartsWith($allowedRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        $isSafeTestRoot = $true
        break
    }
}
if (-not $isSafeTestRoot) {
    throw "TestRoot must be a child of RUNNER_TEMP or the system temporary directory: $TestRoot"
}

$installer = (Resolve-Path -LiteralPath $InstallerPath).Path
$installDir = Join-Path $TestRoot 'app'
$stateDir = Join-Path $TestRoot 'state'
$stateSentinel = Join-Path $stateDir 'state-preserved.txt'
$appId = 'MTG-Thomas.codex-swarm.ci'
$uninstallKey = "Software\Microsoft\Windows\CurrentVersion\Uninstall\$appId"
$environmentKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
try {
    $originalPath = [string]$environmentKey.GetValue(
        'Path',
        '',
        [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames
    )
    $originalPathKind = $environmentKey.GetValueKind('Path')
} catch {
    $originalPathKind = [Microsoft.Win32.RegistryValueKind]::ExpandString
} finally {
    $environmentKey.Dispose()
}
$uninstaller = Join-Path $installDir 'Uninstall.exe'

function Invoke-Installer {
    $process = Start-Process -FilePath $installer -ArgumentList @(
        '/S',
        "/D=$installDir"
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Installer failed with exit code $($process.ExitCode)."
    }
}

function Test-UserPathEntry {
    param([string]$Entry)

    $path = Get-UserPathValue
    foreach ($segment in $path -split ';') {
        if ($segment.Trim().Trim('"').TrimEnd('\', '/') -ieq $Entry.TrimEnd('\', '/')) {
            return $true
        }
    }
    return $false
}

function Get-UserPathValue {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
    try {
        return [string]$key.GetValue(
            'Path',
            '',
            [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames
        )
    } finally {
        $key.Dispose()
    }
}

function Update-UserPathValue {
    [CmdletBinding(SupportsShouldProcess)]
    param(
        [AllowEmptyString()][string]$Value,
        [Microsoft.Win32.RegistryValueKind]$ValueKind = [Microsoft.Win32.RegistryValueKind]::ExpandString
    )

    if ($PSCmdlet.ShouldProcess('HKCU\Environment\Path', "write $ValueKind value")) {
        $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
        try {
            $key.SetValue('Path', $Value, $ValueKind)
        } finally {
            $key.Dispose()
        }
        $result = [UIntPtr]::Zero
        [void][CodexSwarmInstallerTest.NativeMethods]::SendMessageTimeout(
            [IntPtr]0xffff,
            0x001A,
            [UIntPtr]::Zero,
            'Environment',
            2,
            5000,
            [ref]$result
        )
    }
}

function Confirm-UserPathValueKind {
    param([Microsoft.Win32.RegistryValueKind]$Expected)

    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
    try {
        $actual = $key.GetValueKind('Path')
    } finally {
        $key.Dispose()
    }
    if ($actual -ne $Expected) {
        throw "Expected user PATH registry kind $Expected, got $actual."
    }
}

try {
    if (Test-Path -LiteralPath $TestRoot) {
        Remove-Item -LiteralPath $TestRoot -Recurse -Force
    }
    New-Item -ItemType Directory -Path $stateDir -Force | Out-Null
    Set-Content -LiteralPath $stateSentinel -Value 'installer must not remove state'

    Invoke-Installer
    foreach ($binary in 'cs.exe', 'csd.exe') {
        if (-not (Test-Path -LiteralPath (Join-Path $installDir $binary) -PathType Leaf)) {
            throw "Installer did not create $binary."
        }
    }
    if (-not (Test-UserPathEntry -Entry $installDir)) {
        throw 'Installer did not add its application directory to the user PATH.'
    }
    if (-not (Test-Path -LiteralPath "Registry::HKEY_CURRENT_USER\$uninstallKey")) {
        throw 'Installer did not register an Installed Apps entry.'
    }
    if ($ExpectedVersion) {
        $actualVersion = & (Join-Path $installDir 'cs.exe') version
        if ($LASTEXITCODE -ne 0 -or $actualVersion -notmatch [Regex]::Escape($ExpectedVersion)) {
            throw "Installed cs version did not contain expected version $ExpectedVersion`: $actualVersion"
        }
    }

    Invoke-Installer
    if (-not (Test-Path -LiteralPath $uninstaller -PathType Leaf)) {
        throw 'Upgrade/reinstall did not preserve the uninstaller.'
    }

    $process = Start-Process -FilePath $uninstaller -ArgumentList @(
        '/S'
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Uninstaller failed with exit code $($process.ExitCode)."
    }
    foreach ($binary in 'cs.exe', 'csd.exe') {
        if (Test-Path -LiteralPath (Join-Path $installDir $binary)) {
            throw "Uninstaller left $binary behind."
        }
    }
    if (Test-UserPathEntry -Entry $installDir) {
        throw 'Uninstaller left its managed PATH entry behind.'
    }
    if (Test-Path -LiteralPath "Registry::HKEY_CURRENT_USER\$uninstallKey") {
        throw 'Uninstaller left its Installed Apps entry behind.'
    }
    if (-not (Test-Path -LiteralPath $stateSentinel -PathType Leaf)) {
        throw 'Uninstaller removed state outside the application directory.'
    }

    $currentPath = Get-UserPathValue
    if ($currentPath -and -not $currentPath.EndsWith(';')) {
        $currentPath += ';'
    }
    $preExistingEntry = '"{0}\"' -f $installDir
    Update-UserPathValue -Value ($currentPath + $preExistingEntry) -ValueKind $originalPathKind
    Invoke-Installer
    $process = Start-Process -FilePath $uninstaller -ArgumentList @(
        '/S'
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Ownership test uninstaller failed with exit code $($process.ExitCode)."
    }
    if (-not (Test-UserPathEntry -Entry $installDir)) {
        throw 'Uninstaller removed a PATH entry that existed before installation.'
    }

    $longPathSegments = [Collections.Generic.List[string]]::new()
    $longPathSegments.Add('%LOCALAPPDATA%\Microsoft\WindowsApps')
    if ($originalPath) {
        $longPathSegments.Add($originalPath)
    }
    for ($index = 0; ($longPathSegments -join ';').Length -le 4096; $index++) {
        $longPathSegments.Add((Join-Path $TestRoot (
            'long-path-segment-{0:D3}-{1}' -f $index, ('x' * 80)
        )))
    }
    $longPath = $longPathSegments -join ';'
    Update-UserPathValue -Value $longPath -ValueKind ExpandString
    Confirm-UserPathValueKind -Expected ExpandString

    Invoke-Installer
    $expectedInstalledLongPath = "$longPath;$installDir"
    $actualInstalledLongPath = Get-UserPathValue
    if ($actualInstalledLongPath -cne $expectedInstalledLongPath) {
        throw "Installer did not preserve the long user PATH while adding itself. Expected length $($expectedInstalledLongPath.Length), got $($actualInstalledLongPath.Length)."
    }
    Confirm-UserPathValueKind -Expected ExpandString

    Invoke-Installer
    $actualReinstalledLongPath = Get-UserPathValue
    if ($actualReinstalledLongPath -cne $expectedInstalledLongPath) {
        throw 'Reinstall changed or duplicated the installer entry in the long user PATH.'
    }
    Confirm-UserPathValueKind -Expected ExpandString

    $process = Start-Process -FilePath $uninstaller -ArgumentList @(
        '/S'
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Long PATH test uninstaller failed with exit code $($process.ExitCode)."
    }
    $actualUninstalledLongPath = Get-UserPathValue
    if ($actualUninstalledLongPath -cne $longPath) {
        throw "Uninstaller did not restore the long user PATH exactly. Expected length $($longPath.Length), got $($actualUninstalledLongPath.Length)."
    }
    Confirm-UserPathValueKind -Expected ExpandString

    Invoke-Installer
    Remove-Item -LiteralPath (Join-Path $installDir 'cs.exe') -Force
    $process = Start-Process -FilePath $uninstaller -ArgumentList @(
        '/S'
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Missing PATH helper test uninstaller failed with exit code $($process.ExitCode)."
    }
    if (Test-Path -LiteralPath "Registry::HKEY_CURRENT_USER\$uninstallKey") {
        throw 'Missing PATH helper test left its Installed Apps entry behind.'
    }
    if (Test-Path -LiteralPath (Join-Path $installDir 'csd.exe')) {
        throw 'Missing PATH helper test left csd.exe behind.'
    }
    if ((Get-UserPathValue) -cne $expectedInstalledLongPath) {
        throw 'Missing PATH helper test unexpectedly changed the user PATH.'
    }
    Confirm-UserPathValueKind -Expected ExpandString
    Update-UserPathValue -Value $longPath -ValueKind ExpandString

    Write-Output 'Windows installer smoke test passed.'
} finally {
    if (Test-Path -LiteralPath $uninstaller) {
        Start-Process -FilePath $uninstaller -ArgumentList @(
            '/S'
        ) -Wait | Out-Null
    }
    Update-UserPathValue -Value $originalPath -ValueKind $originalPathKind
    Remove-Item -LiteralPath "Registry::HKEY_CURRENT_USER\$uninstallKey" -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath 'Registry::HKEY_CURRENT_USER\Software\MTG-Thomas\codex-swarm.ci' -Recurse -Force -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $TestRoot) {
        Remove-Item -LiteralPath $TestRoot -Recurse -Force
    }
}
