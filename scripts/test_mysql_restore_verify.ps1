[CmdletBinding()]
param(
    [string]$MySQLImage = "mysql:8.4"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$backupDirectory = Join-Path ([System.IO.Path]::GetTempPath()) "weknora-restore-verify-$([guid]::NewGuid().ToString('N'))"
$sourceContainer = "weknora-restore-source-$PID-$([guid]::NewGuid().ToString('N').Substring(0, 8))"
$sourceDatabase = "weknora_restore_test"
$rootPassword = [guid]::NewGuid().ToString("N")
$backupId = "weknora-mysql-20260728T000000Z-0123456789abcdef01234567"

function Invoke-Docker {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)

    & docker @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Docker command failed."
    }
}

try {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Docker is required for the restore-verification test."
    }
    New-Item -ItemType Directory -Path $backupDirectory | Out-Null
    $sourceEnvironmentFile = Join-Path $backupDirectory "source.env"
    "MYSQL_ROOT_PASSWORD=$rootPassword`nMYSQL_DATABASE=$sourceDatabase" | Set-Content -LiteralPath $sourceEnvironmentFile -Encoding ascii -NoNewline

    Invoke-Docker "run" "--name" $sourceContainer "-d" `
        "--env-file" $sourceEnvironmentFile $MySQLImage `
        "--character-set-server=utf8mb4" "--collation-server=utf8mb4_0900_ai_ci"
    Remove-Item -LiteralPath $sourceEnvironmentFile -Force

    $sourceClientConfig = "[client]`nhost=127.0.0.1`nprotocol=TCP`nuser=root`npassword=$rootPassword`n"
    $sourceClientConfig | & docker exec -i $sourceContainer sh -ec "umask 077; cat > /tmp/source.cnf"
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to configure the temporary source MySQL client."
    }

    $deadline = [DateTime]::UtcNow.AddSeconds(120)
    do {
        $null = & docker exec $sourceContainer mysqladmin "--defaults-extra-file=/tmp/source.cnf" "ping" "--silent"
        if ($LASTEXITCODE -eq 0) { break }
        Start-Sleep -Seconds 2
    } while ([DateTime]::UtcNow -lt $deadline)
    if ($LASTEXITCODE -ne 0) {
        throw "The temporary source MySQL container did not become ready."
    }

    Invoke-Docker "cp" (Join-Path $repositoryRoot "migrations/mysql/000074_baseline.up.sql") "$sourceContainer`:/tmp/schema.sql"
    Invoke-Docker "exec" $sourceContainer "sh" "-ec" "mysql --defaults-extra-file=/tmp/source.cnf $sourceDatabase < /tmp/schema.sql"
    Invoke-Docker "exec" $sourceContainer "mysql" "--defaults-extra-file=/tmp/source.cnf" $sourceDatabase "-e" @"
CREATE TABLE schema_migrations (version BIGINT NOT NULL, dirty BOOLEAN NOT NULL);
INSERT INTO schema_migrations (version, dirty) VALUES (74, 0);
INSERT INTO tenants (name, business) VALUES ('Restore test tenant', 'test');
INSERT INTO users (id, username, email, password_hash, tenant_id) VALUES ('restore-test-user', 'restore-test-user', 'restore@example.invalid', 'not-a-real-password', 1);
"@

    $dump = (& docker exec $sourceContainer mysqldump "--defaults-extra-file=/tmp/source.cnf" `
        "--single-transaction" "--routines" "--events" "--triggers" "--no-tablespaces" "--default-character-set=utf8mb4" "--databases" $sourceDatabase) -join "`n"
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to create the temporary source dump."
    }

    $archivePath = Join-Path $backupDirectory "$backupId.sql.gz"
    $archiveStream = [System.IO.File]::Create($archivePath)
    try {
        $gzipStream = New-Object System.IO.Compression.GzipStream($archiveStream, [System.IO.Compression.CompressionMode]::Compress)
        try {
            $dumpBytes = [System.Text.Encoding]::UTF8.GetBytes($dump + "`n")
            $gzipStream.Write($dumpBytes, 0, $dumpBytes.Length)
        }
        finally {
            $gzipStream.Dispose()
        }
    }
    finally {
        $archiveStream.Dispose()
    }

    $archiveInfo = Get-Item -LiteralPath $archivePath
    $manifest = [ordered]@{
        format_version = 1
        backup_id = $backupId
        result = "success"
        trigger = "test"
        reason = "restore verification integration test"
        started_at = "2026-07-28T00:00:00Z"
        completed_at = "2026-07-28T00:00:01Z"
        application_version = "test"
        migration = [ordered]@{ known = $true; version = 74; dirty = $false }
        archive = [ordered]@{
            file = "$backupId.sql.gz"
            size_bytes = $archiveInfo.Length
            sha256 = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
            compression = "gzip"
        }
    }
    $manifest | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $backupDirectory "$backupId.manifest.json") -Encoding utf8

    & (Join-Path $PSScriptRoot "verify_mysql_restore.ps1") -BackupId $backupId -BackupDirectory $backupDirectory -TimeoutSeconds 180
    if ($LASTEXITCODE -ne 0) {
        throw "The restore-verification script failed."
    }
    Write-Host "MySQL isolated restore-verification integration test passed."
}
finally {
    & docker rm -f $sourceContainer 2>$null
    Remove-Item -LiteralPath $backupDirectory -Force -Recurse -ErrorAction SilentlyContinue
}
