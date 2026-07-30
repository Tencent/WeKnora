[CmdletBinding()]
param(
    [switch]$IncludeConfigStaging
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$rollbackScript = Join-Path $PSScriptRoot "rollback_mysql_deployment.ps1"
$testDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("weknora-rollback-cli-$([guid]::NewGuid().ToString('N'))")

try {
    $tokens = $null
    $parseErrors = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($rollbackScript, [ref]$tokens, [ref]$parseErrors)
    if ($parseErrors.Count -ne 0) {
        throw "The rollback CLI contains PowerShell syntax errors."
    }

    New-Item -ItemType Directory -Path $testDirectory | Out-Null
    $backupId = "weknora-mysql-20260728T000000Z-0123456789abcdef01234567"
    & $rollbackScript -Action Database -AuditDirectory $testDirectory -BackupId $backupId -BackupDirectory "D:\\WeKnoraBackups"
    if ($LASTEXITCODE -ne 0) {
        throw "The database break-glass route failed."
    }
    if ((Get-Content -LiteralPath $rollbackScript -Raw) -notmatch "never stops, connects to, or overwrites") {
        throw "The database route did not state its live-database safety boundary."
    }
    $auditFiles = @(Get-ChildItem -LiteralPath $testDirectory -Filter "rollback-*.json" -File)
    if ($auditFiles.Count -ne 1) {
        throw "The database break-glass route did not create exactly one audit record."
    }
    $audit = Get-Content -LiteralPath $auditFiles[0].FullName -Raw | ConvertFrom-Json
    if ($audit.action -cne "database" -or $audit.result -cne "manual_recovery_required" -or $audit.backup_id -cne $backupId) {
        throw "The database break-glass audit record is incomplete."
    }

    if ($IncludeConfigStaging) {
        if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
            throw "Docker Compose is required for the optional configuration-staging test."
        }
        $activeConfig = Join-Path $testDirectory "active.env"
        $approvedDirectory = Join-Path $testDirectory "approved"
        $candidateConfig = Join-Path $approvedDirectory "reviewed.env"
        $currentConfigBackup = Join-Path $approvedDirectory "active-before-rollback.env"
        New-Item -ItemType Directory -Path $approvedDirectory | Out-Null
        Copy-Item -LiteralPath (Join-Path $repositoryRoot ".env.mysql.example") -Destination $activeConfig
        Copy-Item -LiteralPath $activeConfig -Destination $candidateConfig

        & $rollbackScript `
            -Action Config `
            -AuditDirectory $testDirectory `
            -EnvFile $activeConfig `
            -ApprovedConfigDirectory $approvedDirectory `
            -ConfigFile $candidateConfig `
            -CurrentConfigBackupPath $currentConfigBackup `
            -ConfirmRollback
        if ($LASTEXITCODE -ne 0) {
            throw "The configuration staging route failed."
        }
        if (-not (Test-Path -LiteralPath $currentConfigBackup -PathType Leaf)) {
            throw "The configuration staging route did not preserve the active environment file."
        }
        $activeHash = (Get-FileHash -LiteralPath $activeConfig -Algorithm SHA256).Hash
        $candidateHash = (Get-FileHash -LiteralPath $candidateConfig -Algorithm SHA256).Hash
        if ($activeHash -cne $candidateHash) {
            throw "The staged configuration differs from the reviewed candidate."
        }
        $configAudit = @(Get-ChildItem -LiteralPath $testDirectory -Filter "rollback-*.json" -File | ForEach-Object {
            Get-Content -LiteralPath $_.FullName -Raw | ConvertFrom-Json
        } | Where-Object { $_.action -ceq "config" })
        if ($configAudit.Count -ne 1 -or $configAudit[0].result -cne "staged") {
            throw "The configuration staging audit record is incomplete."
        }
    }
    Write-Host "MySQL rollback CLI safety test passed."
}
finally {
    Remove-Item -LiteralPath $testDirectory -Force -Recurse -ErrorAction SilentlyContinue
}
