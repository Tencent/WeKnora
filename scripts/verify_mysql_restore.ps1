[CmdletBinding()]
param(
    [Parameter(Mandatory, Position = 0)]
    [ValidatePattern('^weknora-mysql-\d{8}T\d{6}Z-[a-f0-9]{24}$')]
    [string]$BackupId,

    [Parameter(Mandatory)]
    [string]$BackupDirectory,

    [string]$EnvFile = "",

    [ValidateRange(30, 1800)]
    [int]$TimeoutSeconds = 300,

    [switch]$KeepContainer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Invoke-Docker {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)

    & docker @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Docker command failed."
    }
}

function Invoke-RestoreQuery {
    param([Parameter(Mandatory)][string]$Query)

    $output = & docker exec $script:containerId mysql `
        "--defaults-extra-file=/tmp/restore-verify.cnf" `
        "--protocol=TCP" "-h" "127.0.0.1" `
        "--batch" "--skip-column-names" "-e" $Query
    if ($LASTEXITCODE -ne 0) {
        throw "Restore verification query failed."
    }
    return @($output | Where-Object { $_ -ne $null })
}

function Quote-MySQLIdentifier {
    param([Parameter(Mandatory)][string]$Identifier)

    return '`' + $Identifier.Replace('`', '``') + '`'
}

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "Docker is required for restore verification."
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$composeFile = Join-Path $repositoryRoot "docker-compose.mysql.yml"
$restoreComposeFile = Join-Path $repositoryRoot "docker-compose.mysql.restore-verify.yml"
if (-not (Test-Path -LiteralPath $composeFile -PathType Leaf)) {
    throw "MySQL Compose file was not found."
}
if (-not (Test-Path -LiteralPath $restoreComposeFile -PathType Leaf)) {
    throw "MySQL restore-verification Compose file was not found."
}

if (-not (Test-Path -LiteralPath $BackupDirectory -PathType Container)) {
    throw "The supplied backup directory does not exist."
}
$resolvedBackupDirectory = (Resolve-Path -LiteralPath $BackupDirectory).Path

$manifestFile = "$BackupId.manifest.json"
$archiveFile = "$BackupId.sql.gz"
$manifestPath = Join-Path $resolvedBackupDirectory $manifestFile
$archivePath = Join-Path $resolvedBackupDirectory $archiveFile
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf) -or -not (Test-Path -LiteralPath $archivePath -PathType Leaf)) {
    throw "The selected backup archive or its manifest is missing."
}

try {
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
}
catch {
    throw "The selected backup manifest is not valid JSON."
}

if ($manifest.format_version -ne 1 -or $manifest.backup_id -cne $BackupId -or $manifest.result -cne "success" -or $null -eq $manifest.archive) {
    throw "The selected manifest is not a successful supported MySQL backup."
}
if ($manifest.archive.file -cne $archiveFile -or $manifest.archive.compression -cne "gzip") {
    throw "The selected manifest does not match the expected gzip archive."
}
$expectedHash = [string]$manifest.archive.sha256
if (-not [regex]::IsMatch($expectedHash, '\A[0-9a-f]{64}\z')) {
    throw "The selected manifest has an invalid SHA-256 checksum."
}
$archiveInfo = Get-Item -LiteralPath $archivePath
if ([int64]$manifest.archive.size_bytes -ne $archiveInfo.Length) {
    throw "The backup archive size does not match its manifest."
}
$actualHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualHash -cne $expectedHash) {
    throw "The backup archive SHA-256 does not match its manifest."
}

$resolvedEnvFile = ""
if (-not [string]::IsNullOrWhiteSpace($EnvFile)) {
    $envFilePath = if ([System.IO.Path]::IsPathRooted($EnvFile)) { $EnvFile } else { Join-Path $repositoryRoot $EnvFile }
    if (-not (Test-Path -LiteralPath $envFilePath -PathType Leaf)) {
        throw "The supplied Compose environment file does not exist."
    }
    $resolvedEnvFile = (Resolve-Path -LiteralPath $envFilePath).Path
}

$projectName = "weknora-restore-verify-$PID-$([guid]::NewGuid().ToString('N').Substring(0, 8))"
$restorePassword = [guid]::NewGuid().ToString("N") + [guid]::NewGuid().ToString("N")
$composeArguments = @("compose")
if ($resolvedEnvFile) {
    $composeArguments += @("--env-file", $resolvedEnvFile)
}
$composeArguments += @("-f", $composeFile, "-f", $restoreComposeFile, "--project-name", $projectName, "--profile", "restore-verify")

$previousBackupHostDirectory = [Environment]::GetEnvironmentVariable("BACKUP_HOST_DIR", "Process")
$previousRestorePassword = [Environment]::GetEnvironmentVariable("RESTORE_VERIFY_ROOT_PASSWORD", "Process")
$containerStarted = $false
$stopwatch = [System.Diagnostics.Stopwatch]::StartNew()

try {
    # Override the mount for this process only; do not edit the operator's .env file.
    [Environment]::SetEnvironmentVariable("BACKUP_HOST_DIR", $resolvedBackupDirectory, "Process")
    [Environment]::SetEnvironmentVariable("RESTORE_VERIFY_ROOT_PASSWORD", $restorePassword, "Process")

    Write-Host "Starting an isolated MySQL restore-verification instance..."
    Invoke-Docker @composeArguments "up" "-d" "mysql-restore-verify"
    $containerStarted = $true
    $script:containerId = ((& docker @composeArguments "ps" "-q" "mysql-restore-verify") | Select-Object -First 1).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($script:containerId)) {
        throw "The isolated MySQL restore-verification container was not created."
    }

    # Send the password through standard input instead of placing it in a host command line.
    $clientConfig = "[client]`nhost=127.0.0.1`nprotocol=TCP`nuser=root`npassword=$restorePassword`n"
    $clientConfig | & docker exec -i $script:containerId sh -ec "umask 077; cat > /tmp/restore-verify.cnf"
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to configure the isolated MySQL client."
    }

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    $ready = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        $null = & docker exec $script:containerId mysqladmin "--defaults-extra-file=/tmp/restore-verify.cnf" "ping" "--silent"
        if ($LASTEXITCODE -eq 0) {
            $ready = $true
            break
        }
        Start-Sleep -Seconds 2
    }
    if (-not $ready) {
        & docker logs $script:containerId
        throw "The isolated MySQL instance did not become ready before the timeout."
    }

    Write-Host "Checking gzip archive and importing into the isolated instance..."
    Invoke-Docker "exec" $script:containerId "sh" "-ec" "gzip -t /data/backups/$archiveFile"
    Invoke-Docker "exec" $script:containerId "sh" "-ec" "zcat /data/backups/$archiveFile | mysql --defaults-extra-file=/tmp/restore-verify.cnf --protocol=TCP"

    $schemaRows = @(Invoke-RestoreQuery @"
SELECT DISTINCT table_schema
FROM information_schema.tables
WHERE table_name = 'schema_migrations'
  AND table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys');
"@)
    if ($schemaRows.Count -ne 1) {
        throw "The restored archive did not contain exactly one application migration table."
    }
    $restoredDatabase = [string]$schemaRows[0]
    $databaseIdentifier = Quote-MySQLIdentifier $restoredDatabase

    $migrationRow = @(Invoke-RestoreQuery ("SELECT version, dirty FROM ${databaseIdentifier}." + (Quote-MySQLIdentifier "schema_migrations") + " ORDER BY version DESC LIMIT 1;"))
    if ($migrationRow.Count -ne 1) {
        throw "The restored migration table has no current migration state."
    }
    $migrationParts = $migrationRow[0] -split "`t"
    if ($migrationParts.Count -ne 2 -or $migrationParts[1] -ne "0") {
        throw "The restored migration state is dirty or unreadable."
    }
    if ([bool]$manifest.migration.known -and [uint64]$migrationParts[0] -ne [uint64]$manifest.migration.version) {
        throw "The restored migration version does not match the backup manifest."
    }

    $keyTables = @("tenants", "users", "knowledge_bases", "knowledges", "sessions", "messages")
    $countQueries = foreach ($table in $keyTables) {
        "SELECT '$table' AS table_name, COUNT(*) AS row_count FROM ${databaseIdentifier}." + (Quote-MySQLIdentifier $table)
    }
    $counts = @(Invoke-RestoreQuery ($countQueries -join " UNION ALL "))
    if ($counts.Count -ne $keyTables.Count) {
        throw "The restored database is missing one or more key application tables."
    }

    Write-Host "Key table counts:"
    foreach ($count in $counts) {
        Write-Host "  $count"
    }
    Write-Host "Sample record IDs (no business content is displayed):"
    foreach ($table in @("tenants", "users", "knowledge_bases", "knowledges")) {
        $sampleRows = @(Invoke-RestoreQuery ("SELECT CAST(id AS CHAR), DATE_FORMAT(created_at, '%Y-%m-%dT%H:%i:%sZ') FROM ${databaseIdentifier}." + (Quote-MySQLIdentifier $table) + " ORDER BY created_at DESC, id DESC LIMIT 3;"))
        if ($sampleRows.Count -eq 0) {
            Write-Host "  ${table}: no records"
        }
        else {
            Write-Host "  ${table}: $($sampleRows -join ', ')"
        }
    }

    $stopwatch.Stop()
    Write-Host "Restore verification passed."
    Write-Host "  Backup ID: $BackupId"
    Write-Host "  Restored database: $restoredDatabase"
    Write-Host "  Migration version: $($migrationParts[0]) (clean)"
    Write-Host "  Elapsed: $([Math]::Round($stopwatch.Elapsed.TotalSeconds, 1)) seconds"
}
finally {
    $stopwatch.Stop()
    if ($containerStarted -and -not $KeepContainer) {
        Write-Host "Removing the isolated restore-verification instance..."
        & docker @composeArguments "down" "--volumes"
    }
    elseif ($containerStarted) {
        Write-Warning "The isolated restore-verification instance was kept under project $projectName."
    }
    [Environment]::SetEnvironmentVariable("BACKUP_HOST_DIR", $previousBackupHostDirectory, "Process")
    [Environment]::SetEnvironmentVariable("RESTORE_VERIFY_ROOT_PASSWORD", $previousRestorePassword, "Process")
}
