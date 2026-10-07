# Compatible with Windows PowerShell 5.1 and PowerShell 7.
[CmdletBinding()]
param()

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

function Invoke-Checked {
    param(
        [string]$Command,
        [string[]]$Arguments
    )

    Write-Host ("> {0} {1}" -f (Split-Path -Leaf $Command), ($Arguments -join ' '))
    # PowerShell 5.1 treats native stderr as error records. Exit codes decide
    # success; npm warnings on stderr must not terminate a successful command.
    $savedErrorAction = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $PSNativeCommandUseErrorActionPreference = $false
        & $Command @Arguments
        $commandExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $savedErrorAction
    }
    if ($commandExitCode -ne 0) {
        throw "$Command failed with exit code $commandExitCode."
    }
}

$repoRoot = Split-Path -Parent $PSScriptRoot
$savedGoWork = $env:GOWORK
Push-Location $repoRoot
try {
    $goCommand = @(Get-Command go -CommandType Application -ErrorAction Stop)[0].Source
    $nodeCommand = @(Get-Command node -CommandType Application -ErrorAction Stop)[0].Source
    $npmName = 'npm'
    if ($env:OS -eq 'Windows_NT') {
        $npmName = 'npm.cmd'
    }
    $npmCommand = @(Get-Command $npmName -CommandType Application -ErrorAction Stop)[0].Source
    $env:GOWORK = Join-Path $repoRoot 'go.work'

    Invoke-Checked $goCommand @('version')
    Invoke-Checked $nodeCommand @('--version')
    Invoke-Checked $npmCommand @('--version')

    $workspaceJson = & $goCommand work edit -json
    if ($LASTEXITCODE -ne 0) {
        throw 'Cannot read go.work.'
    }
    $modules = @(($workspaceJson -join "`n" | ConvertFrom-Json).Use)
    if ($modules.Count -eq 0) {
        throw 'go.work contains no modules.'
    }

    foreach ($frontend in @('aifd-examples/desktool/frontend', 'aifd-proxy/web')) {
        Write-Host "Frontend: $frontend"
        Push-Location (Join-Path $repoRoot $frontend)
        try {
            Invoke-Checked $npmCommand @('ci')
            Invoke-Checked $npmCommand @('test')
            Invoke-Checked $npmCommand @('run', 'build')
        }
        finally {
            Pop-Location
        }
    }

    if ([string]::IsNullOrEmpty($env:AIFD_NATS_URL)) {
        Write-Host 'SKIP: two external NATS integration tests (AIFD_NATS_URL is unset).'
    }
    else {
        Write-Host 'External NATS integration tests enabled by AIFD_NATS_URL.'
    }
    foreach ($module in $modules) {
        Write-Host ("Go module: {0}" -f $module.DiskPath)
        Push-Location (Join-Path $repoRoot $module.DiskPath)
        try {
            Invoke-Checked $goCommand @('test', '-mod=readonly', '-count=1', '-timeout=2m', './...')
        }
        finally {
            Pop-Location
        }
    }

    Write-Host ("Verification succeeded: two frontend builds and test suites, {0} Go module test commands." -f $modules.Count)
    if ([string]::IsNullOrEmpty($env:AIFD_NATS_URL)) {
        Write-Host 'External NATS integration tests: SKIPPED, not verified.'
    }
    Write-Host 'Native desktop execution and Windows installer builds are separate checks.'
}
catch {
    [Console]::Error.WriteLine("Verification failed: {0}", $_.Exception.Message)
    exit 1
}
finally {
    $env:GOWORK = $savedGoWork
    Pop-Location
}
