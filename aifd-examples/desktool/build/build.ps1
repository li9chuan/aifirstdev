# Windows build entry, compatible with PowerShell 5.1 and 7.
[CmdletBinding()]
param(
    [string]$Version = '0.1.0',
    [switch]$Dev,
    [switch]$Package,
    [switch]$BindingsOnly
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    $savedErrorAction = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $PSNativeCommandUseErrorActionPreference = $false
        & $Command @Arguments
        $nativeExitCode = $LASTEXITCODE
    }
    finally { $ErrorActionPreference = $savedErrorAction }
    if ($nativeExitCode -ne 0) { throw "$Command failed with exit code $nativeExitCode" }
}

$desktopRoot = Split-Path -Parent $PSScriptRoot
$repoRoot = [IO.Path]::GetFullPath((Join-Path $desktopRoot '../..'))
$savedGoWork = $env:GOWORK
$syso = Join-Path $desktopRoot 'wails_windows_amd64.syso'
$placeholder = Join-Path $desktopRoot 'frontend/dist/wails-build-placeholder.txt'
$createdPlaceholder = $false
Push-Location $desktopRoot
try {
    if ($env:OS -ne 'Windows_NT') { throw 'This packaging entry supports Windows amd64.' }
    if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw 'Version must contain three numeric components, e.g. 0.1.0.' }
    $go = @(Get-Command go -CommandType Application -ErrorAction Stop)[0].Source
    $env:GOWORK = Join-Path $repoRoot 'go.work'
    $target = @(& $go env GOOS GOARCH)
    if ($LASTEXITCODE -ne 0 -or $target[0] -ne 'windows' -or $target[1] -ne 'amd64') {
        throw 'This packaging entry requires GOOS=windows and GOARCH=amd64.'
    }
    $pin = @(Get-Content (Join-Path $repoRoot 'aifd.lock') | Where-Object { $_ -match '^tools\.wails:' })
    if ($pin.Count -ne 1) { throw 'Expected one tools.wails entry in aifd.lock.' }
    $expected = ($pin[0] -split ':', 2)[1].Trim()
    $wails = $env:AIFD_WAILS
    if ([string]::IsNullOrWhiteSpace($wails)) {
        $wails = Join-Path $repoRoot ".aifd/tools/windows-amd64/wails/$expected/bin/wails3.exe"
        if (!(Test-Path -LiteralPath $wails)) {
            $wails = @(Get-Command wails3 -CommandType Application -ErrorAction Stop)[0].Source
        }
    }
    $identity = (& $go version -m $wails | Out-String)
    if ($LASTEXITCODE -ne 0 -or $identity -notmatch ('mod\s+github.com/wailsapp/wails/v3\s+' + [regex]::Escape($expected) + '\s')) {
        throw "Wails CLI must match $expected from aifd.lock. Install with aifd tools install --tool wails."
    }
    $moduleVersion = & $go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3
    if ($LASTEXITCODE -ne 0 -or $moduleVersion -ne $expected) { throw 'Wails Go dependency does not match aifd.lock.' }
    $frontend = Get-Content frontend/package.json -Raw | ConvertFrom-Json
    if ($frontend.dependencies.'@wailsio/runtime' -ne $expected.TrimStart('v')) { throw 'Wails frontend runtime does not match aifd.lock.' }

    # Go embed must resolve before bindings can be generated on a fresh checkout.
    if (!(Test-Path frontend/dist) -or @(Get-ChildItem frontend/dist -File -Recurse).Count -eq 0) {
        New-Item -ItemType Directory -Path frontend/dist -Force | Out-Null
        [IO.File]::WriteAllText($placeholder, 'Binding generation only; not a runnable frontend.')
        $createdPlaceholder = $true
    }
    try { Invoke-Checked $wails @('generate', 'bindings', '-ts', '-i', '-names') }
    finally {
        if ($createdPlaceholder -and (Test-Path -LiteralPath $placeholder)) { Remove-Item -LiteralPath $placeholder }
    }
    if ($BindingsOnly) { return }

    $npm = @(Get-Command npm.cmd -CommandType Application -ErrorAction Stop)[0].Source
    Push-Location frontend
    try {
        Invoke-Checked $npm @('ci')
        Invoke-Checked $npm @('test')
        Invoke-Checked $npm @('run', 'build')
    }
    finally { Pop-Location }
    if (!(Test-Path frontend/dist/index.html)) { throw 'Compiled frontend/dist/index.html is required.' }

    New-Item -ItemType Directory -Path build/tmp, build/bin -Force | Out-Null
    $info = Get-Content build/windows/info.json -Raw | ConvertFrom-Json
    $info.fixed.file_version = "$Version.0"
    $info.fixed.product_version = "$Version.0"
    $info.info.'0409'.FileVersion = "$Version.0"
    $info.info.'0409'.ProductVersion = $Version
    [IO.File]::WriteAllText((Join-Path $desktopRoot 'build/tmp/info.json'), ($info | ConvertTo-Json -Depth 10))
    [xml]$manifest = Get-Content build/windows/wails.exe.manifest -Raw
    $manifest.assembly.assemblyIdentity.version = "$Version.0"
    $manifest.Save((Join-Path $desktopRoot 'build/tmp/wails.exe.manifest'))
    try {
        Invoke-Checked $wails @('generate', 'syso', '-arch', 'amd64', '-icon', 'build/windows/icon.ico', '-manifest', 'build/tmp/wails.exe.manifest', '-info', 'build/tmp/info.json', '-out', $syso)
        $flags = '-X github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/desktop.currentVersion=' + $Version
        $buildArgs = @('build', '-mod=readonly', '-buildvcs=false', '-trimpath', '-o', 'build/bin/desktool.exe')
        if (!$Dev) { $buildArgs += @('-tags', 'production'); $flags = '-w -s -H windowsgui ' + $flags }
        $buildArgs += @('-ldflags', $flags, '.')
        Invoke-Checked $go $buildArgs
    }
    finally { if (Test-Path -LiteralPath $syso) { Remove-Item -LiteralPath $syso } }

    $binaryVersion = (Get-Item -LiteralPath 'build/bin/desktool.exe').VersionInfo
    if ($binaryVersion.FileVersion -ne "$Version.0" -or $binaryVersion.ProductVersion -ne $Version) {
        throw 'Built executable version resources do not match the requested version.'
    }

    if ($Package) {
        $makensis = $env:AIFD_MAKENSIS
        if ([string]::IsNullOrWhiteSpace($makensis)) { $makensis = @(Get-Command makensis -CommandType Application -ErrorAction Stop)[0].Source }
        Invoke-Checked $wails @('generate', 'webview2bootstrapper', '-dir', 'build/windows/nsis')
        Push-Location build/windows/nsis
        try {
            Invoke-Checked $makensis @("/DINFO_PRODUCTVERSION=$Version", "/DARG_WAILS_AMD64_BINARY=$desktopRoot\build\bin\desktool.exe", 'project.nsi')
        }
        finally { Pop-Location }
    }
}
finally {
    $env:GOWORK = $savedGoWork
    Pop-Location
}
