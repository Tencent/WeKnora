[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidateSet("Deployment", "Config", "Database")]
    [string]$Action,

    [Parameter(Mandatory)]
    [string]$AuditDirectory,

    [string]$EnvFile = ".env.mysql",

    [string]$ImageTag = "",

    [string]$ExpectedAppImageDigest = "",

    [string]$ExpectedFrontendImageDigest = "",

    [string]$ExpectedDocreaderImageDigest = "",

    [UInt64]$TargetMigrationVersion = 0,

    [string]$ApprovedConfigDirectory = "",

    [string]$ConfigFile = "",

    [string]$CurrentConfigBackupPath = "",

    [string]$BackupId = "",

    [string]$BackupDirectory = "",

    [ValidateRange(30, 1800)]
    [int]$TimeoutSeconds = 180,

    [switch]$ConfirmRollback
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-RepositoryPath {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][ValidateSet("Leaf", "Container")][string]$PathType
    )

    $candidate = if ([System.IO.Path]::IsPathRooted($Path)) {
        $Path
    }
    else {
        Join-Path $script:repositoryRoot $Path
    }
    if (-not (Test-Path -LiteralPath $candidate -PathType $PathType)) {
        throw "The required $PathType path was not found."
    }
    return (Resolve-Path -LiteralPath $candidate).Path
}

function Resolve-NewFilePath {
    param([Parameter(Mandatory)][string]$Path)

    $candidate = if ([System.IO.Path]::IsPathRooted($Path)) {
        $Path
    }
    else {
        Join-Path $script:repositoryRoot $Path
    }
    $parent = Split-Path -Parent $candidate
    if ([string]::IsNullOrWhiteSpace($parent) -or -not (Test-Path -LiteralPath $parent -PathType Container)) {
        throw "The parent directory for the current configuration backup does not exist."
    }
    $resolved = [System.IO.Path]::GetFullPath($candidate)
    if (Test-Path -LiteralPath $resolved) {
        throw "The current configuration backup path already exists; refusing to overwrite it."
    }
    return $resolved
}

function Assert-PathInsideDirectory {
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [Parameter(Mandatory)][string]$DirectoryPath
    )

    $directoryPrefix = $DirectoryPath
    if (-not $directoryPrefix.EndsWith([System.IO.Path]::DirectorySeparatorChar)) {
        $directoryPrefix += [System.IO.Path]::DirectorySeparatorChar
    }
    if (-not $FilePath.StartsWith($directoryPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "The selected configuration is outside the approved configuration directory."
    }
}

function Get-EnvironmentValues {
    param([Parameter(Mandatory)][string]$Path)

    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path) {
        $trimmed = $line.Trim()
        if ([string]::IsNullOrWhiteSpace($trimmed) -or $trimmed.StartsWith("#")) {
            continue
        }
        if ($trimmed -notmatch "^([A-Za-z_][A-Za-z0-9_]*)=(.*)$") {
            throw "The environment file contains a line that is not KEY=VALUE syntax."
        }
        $key = $Matches[1]
        $value = $Matches[2].Trim()
        if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        $values[$key] = $value
    }
    return $values
}

function Test-MySQLEnvironment {
    param([Parameter(Mandatory)][hashtable]$Values)

    if ($Values.ContainsKey("DB_DRIVER") -and $Values["DB_DRIVER"] -ine "mysql") {
        throw "The selected configuration is not a MySQL configuration."
    }
    if ($Values.ContainsKey("DB_HOST") -and $Values["DB_HOST"] -ine "mysql") {
        throw "The selected configuration does not target the bundled MySQL service."
    }
    if ($Values.ContainsKey("DB_PORT") -and $Values["DB_PORT"] -ne "3306") {
        throw "The selected configuration does not use the bundled MySQL port."
    }
}

function Get-SecretValues {
    param([Parameter(Mandatory)][hashtable]$Values)

    $secretValues = @()
    foreach ($key in $Values.Keys) {
        if ($key -match "(?i)(PASSWORD|SECRET|TOKEN|API_KEY|DSN)$" -and $Values[$key].Length -gt 3) {
            $secretValues += $Values[$key]
        }
    }
    return $secretValues
}

function Protect-Message {
    param(
        [Parameter(Mandatory)][string]$Message,
        [string[]]$Secrets = @()
    )

    $sanitized = $Message
    foreach ($secret in $Secrets) {
        if (-not [string]::IsNullOrWhiteSpace($secret)) {
            $sanitized = $sanitized.Replace($secret, "[redacted]")
        }
    }
    return $sanitized
}

function Get-GitRevision {
    $revision = & git -C $script:repositoryRoot rev-parse HEAD 2>$null
    if ($LASTEXITCODE -ne 0) {
        return "unavailable"
    }
    return (($revision | Select-Object -First 1).Trim())
}

function Assert-DockerCompose {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Docker Desktop with Docker Compose v2 is required."
    }
    & docker compose version *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Compose v2 is required."
    }
}

function Invoke-Compose {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$ComposeArguments)

    & docker compose @script:composeArguments @ComposeArguments
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Compose command failed."
    }
}

function Invoke-Docker {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$DockerArguments)

    & docker @DockerArguments | Out-Host
    if ($LASTEXITCODE -ne 0) {
        throw "Docker command failed."
    }
}

function Assert-MySQLComposeFile {
    if (-not (Select-String -LiteralPath $script:composeFile -Pattern "^\s*DB_DRIVER:\s*mysql\s*$" -Quiet)) {
        throw "The bundled Compose file is not the expected MySQL deployment definition."
    }
}

function Assert-ComposeConfiguration {
    param([Parameter(Mandatory)][string]$EnvironmentFile)

    & docker compose --env-file $EnvironmentFile -f $script:composeFile config --quiet
    if ($LASTEXITCODE -ne 0) {
        throw "The selected environment file does not produce a valid MySQL Compose configuration."
    }
}

function Get-MySQLMigrationState {
    $query = 'mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" -D "$MYSQL_DATABASE" --batch --skip-column-names -e "SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1;"'
    $output = & docker compose @script:composeArguments exec -T mysql sh -ec $query
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to read the current MySQL migration state."
    }
    $rows = @($output | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($rows.Count -ne 1) {
        throw "The current MySQL migration state is missing or ambiguous."
    }
    $parts = $rows[0].Trim() -split "\s+"
    if ($parts.Count -ne 2 -or $parts[1] -ne "0") {
        throw "The current MySQL migration state is dirty or unreadable."
    }
    try {
        return [UInt64]$parts[0]
    }
    catch {
        throw "The current MySQL migration version is invalid."
    }
}

function Get-ServiceImage {
    param([Parameter(Mandatory)][string]$Service)

    $containerId = ((& docker compose @script:composeArguments ps -q $Service) | Select-Object -First 1)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($containerId)) {
        return "not-running"
    }
    $image = & docker inspect $containerId.Trim() --format "{{.Config.Image}}|{{.Image}}"
    if ($LASTEXITCODE -ne 0) {
        return "unavailable"
    }
    return (($image | Select-Object -First 1).Trim())
}

function Assert-ExpectedImageDigest {
    param(
        [Parameter(Mandatory)][string]$Repository,
        [Parameter(Mandatory)][string]$Tag,
        [Parameter(Mandatory)][string]$ExpectedDigest
    )

    if ($ExpectedDigest -notmatch "^sha256:[a-f0-9]{64}$") {
        throw "An expected image digest must be a sha256 value."
    }
    $image = "${Repository}:$Tag"
    Invoke-Docker "pull" $image
    $rawDigests = & docker image inspect $image --format "{{json .RepoDigests}}"
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to inspect the pulled image."
    }
    try {
        $repoDigests = @((($rawDigests | Select-Object -First 1) | ConvertFrom-Json))
    }
    catch {
        throw "The pulled image does not expose a verifiable repository digest."
    }
    $expectedReference = "${Repository}@$ExpectedDigest"
    if ($repoDigests -notcontains $expectedReference) {
        throw "The pulled image digest does not match the approved digest."
    }
    return [ordered]@{ image = $image; digest = $ExpectedDigest }
}

function Wait-ForApplicationHealth {
    param([Parameter(Mandatory)][int]$Timeout)

    $containerId = ((& docker compose @script:composeArguments ps -q app) | Select-Object -First 1)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($containerId)) {
        throw "The application container was not created during rollback."
    }
    $deadline = [DateTime]::UtcNow.AddSeconds($Timeout)
    while ([DateTime]::UtcNow -lt $deadline) {
        $status = & docker inspect $containerId.Trim() --format "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}"
        if ($LASTEXITCODE -ne 0) {
            throw "Unable to inspect the rolled-back application container."
        }
        $status = ($status | Select-Object -First 1).Trim()
        if ($status -eq "healthy") {
            return
        }
        if ($status -eq "unhealthy" -or $status -eq "exited" -or $status -eq "dead") {
            throw "The rolled-back application container became $status."
        }
        Start-Sleep -Seconds 2
    }
    throw "The rolled-back application container did not become healthy before the timeout."
}

function Write-AuditRecord {
    param(
        [Parameter(Mandatory)][System.Collections.IDictionary]$Record,
        [Parameter(Mandatory)][string]$Path
    )

    $temporaryPath = "$Path.$PID.tmp"
    $Record | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $temporaryPath -Encoding utf8 -NoNewline
    Move-Item -LiteralPath $temporaryPath -Destination $Path -ErrorAction Stop
}

function Assert-Confirmed {
    if (-not $ConfirmRollback) {
        throw "This action changes deployment state. Re-run with -ConfirmRollback after reviewing the preflight inputs."
    }
}

function Assert-DeploymentParameters {
    if ($ImageTag -notmatch "^(?!latest$)[A-Za-z0-9][A-Za-z0-9._-]{0,127}$") {
        throw "ImageTag must be a fixed non-latest image tag."
    }
    if ($TargetMigrationVersion -eq 0) {
        throw "TargetMigrationVersion is required for a deployment rollback."
    }
    foreach ($digest in @($ExpectedAppImageDigest, $ExpectedFrontendImageDigest, $ExpectedDocreaderImageDigest)) {
        if ([string]::IsNullOrWhiteSpace($digest)) {
            throw "All three approved image digests are required for a deployment rollback."
        }
    }
}

function Assert-ConfigParameters {
    if ([string]::IsNullOrWhiteSpace($ApprovedConfigDirectory) -or [string]::IsNullOrWhiteSpace($ConfigFile) -or [string]::IsNullOrWhiteSpace($CurrentConfigBackupPath)) {
        throw "ApprovedConfigDirectory, ConfigFile, and CurrentConfigBackupPath are required for a configuration rollback."
    }
}

function Assert-DatabaseParameters {
    if ($BackupId -notmatch "^weknora-mysql-\d{8}T\d{6}Z-[a-f0-9]{24}$") {
        throw "BackupId must identify a manifest-compatible MySQL backup."
    }
    if ([string]::IsNullOrWhiteSpace($BackupDirectory)) {
        throw "BackupDirectory is required for break-glass recovery instructions."
    }
}

$script:repositoryRoot = Split-Path -Parent $PSScriptRoot
$script:composeFile = Join-Path $script:repositoryRoot "docker-compose.mysql.yml"
$resolvedAuditDirectory = [System.IO.Path]::GetFullPath($AuditDirectory)
New-Item -ItemType Directory -Path $resolvedAuditDirectory -Force | Out-Null
$auditPath = Join-Path $resolvedAuditDirectory ("rollback-{0}-{1}.json" -f [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ"), [guid]::NewGuid().ToString("N"))
$audit = [ordered]@{
    format_version = 1
    action = $Action.ToLowerInvariant()
    started_at = [DateTime]::UtcNow.ToString("o")
    completed_at = $null
    result = "failed"
    repository_git_commit = Get-GitRevision
    audit_file = Split-Path -Leaf $auditPath
}
$secretValues = @()
$previousWeKnoraVersion = $null
$previousAutoMigrate = $null
$stagedConfigTemporaryPath = ""

try {
    if (-not (Test-Path -LiteralPath $script:composeFile -PathType Leaf)) {
        throw "MySQL Compose file was not found."
    }
    Assert-MySQLComposeFile

    if ($Action -eq "Database") {
        Assert-DatabaseParameters
        $audit["backup_id"] = $BackupId
        $audit["backup_directory_name"] = Split-Path -Leaf $BackupDirectory.TrimEnd([char[]]@('\', '/'))
        $audit["result"] = "manual_recovery_required"
        Write-Host "No live database restore is available from this command."
        Write-Host "1. Verify the selected archive in an isolated MySQL instance:"
        Write-Host "   .\\scripts\\verify_mysql_restore.ps1 -BackupId $BackupId -BackupDirectory <protected-backup-directory> -EnvFile $EnvFile"
        Write-Host "2. Review the verified manifest, migration version, table counts, and restore duration."
        Write-Host "3. Enter a maintenance window, take a fresh backup, restore to a new MySQL instance, validate it, then perform a reviewed connection cutover."
        Write-Host "This command never stops, connects to, or overwrites the running MySQL service."
        return
    }

    Assert-DockerCompose
    $resolvedEnvFile = Resolve-RepositoryPath -Path $EnvFile -PathType Leaf
    $activeEnvironment = Get-EnvironmentValues -Path $resolvedEnvFile
    Test-MySQLEnvironment -Values $activeEnvironment
    $secretValues = Get-SecretValues -Values $activeEnvironment
    $audit["active_config_sha256"] = (Get-FileHash -LiteralPath $resolvedEnvFile -Algorithm SHA256).Hash.ToLowerInvariant()
    $audit["active_config_file"] = Split-Path -Leaf $resolvedEnvFile
    $script:composeArguments = @("--env-file", $resolvedEnvFile, "-f", $script:composeFile)
    Assert-ComposeConfiguration -EnvironmentFile $resolvedEnvFile

    if ($Action -eq "Config") {
        Assert-ConfigParameters
        Assert-Confirmed
        $resolvedApprovedDirectory = Resolve-RepositoryPath -Path $ApprovedConfigDirectory -PathType Container
        $resolvedCandidateConfig = Resolve-RepositoryPath -Path $ConfigFile -PathType Leaf
        Assert-PathInsideDirectory -FilePath $resolvedCandidateConfig -DirectoryPath $resolvedApprovedDirectory
        if ($resolvedCandidateConfig -ieq $resolvedEnvFile) {
            throw "The candidate configuration is already active; refusing a no-op rollback."
        }
        $candidateEnvironment = Get-EnvironmentValues -Path $resolvedCandidateConfig
        $secretValues += Get-SecretValues -Values $candidateEnvironment
        Test-MySQLEnvironment -Values $candidateEnvironment
        Assert-ComposeConfiguration -EnvironmentFile $resolvedCandidateConfig
        $resolvedCurrentConfigBackup = Resolve-NewFilePath -Path $CurrentConfigBackupPath
        Copy-Item -LiteralPath $resolvedEnvFile -Destination $resolvedCurrentConfigBackup -ErrorAction Stop
        $stagedConfigTemporaryPath = "{0}.rollback-{1}.tmp" -f $resolvedEnvFile, [guid]::NewGuid().ToString("N")
        Copy-Item -LiteralPath $resolvedCandidateConfig -Destination $stagedConfigTemporaryPath -ErrorAction Stop
        $stagedConfigHash = (Get-FileHash -LiteralPath $stagedConfigTemporaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
        $candidateConfigHash = (Get-FileHash -LiteralPath $resolvedCandidateConfig -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($stagedConfigHash -cne $candidateConfigHash) {
            throw "The staged configuration hash does not match the reviewed candidate."
        }
        Move-Item -LiteralPath $stagedConfigTemporaryPath -Destination $resolvedEnvFile -Force -ErrorAction Stop
        $stagedConfigTemporaryPath = ""
        $audit["candidate_config_sha256"] = $candidateConfigHash
        $audit["previous_config_backup_file"] = Split-Path -Leaf $resolvedCurrentConfigBackup
        $audit["result"] = "staged"
        Write-Host "The reviewed MySQL configuration has been staged without restarting containers."
        Write-Host "Review the changed Compose configuration, then recreate only the affected services in a maintenance window."
        return
    }

    Assert-DeploymentParameters
    Assert-Confirmed
    $currentMigrationVersion = Get-MySQLMigrationState
    $audit["current_migration_version"] = $currentMigrationVersion
    $audit["target_migration_version"] = $TargetMigrationVersion
    if ($TargetMigrationVersion -ne $currentMigrationVersion) {
        throw "The requested image is not proven schema-compatible with the running MySQL deployment. Use verified database recovery instead."
    }

    $audit["current_images"] = [ordered]@{
        app = Get-ServiceImage -Service "app"
        frontend = Get-ServiceImage -Service "frontend"
        docreader = Get-ServiceImage -Service "docreader"
    }
    $targetImages = [ordered]@{
        app = Assert-ExpectedImageDigest -Repository "wechatopenai/weknora-app" -Tag $ImageTag -ExpectedDigest $ExpectedAppImageDigest
        frontend = Assert-ExpectedImageDigest -Repository "wechatopenai/weknora-ui" -Tag $ImageTag -ExpectedDigest $ExpectedFrontendImageDigest
        docreader = Assert-ExpectedImageDigest -Repository "wechatopenai/weknora-docreader" -Tag $ImageTag -ExpectedDigest $ExpectedDocreaderImageDigest
    }
    $audit["target_images"] = $targetImages

    $previousWeKnoraVersion = [Environment]::GetEnvironmentVariable("WEKNORA_VERSION", "Process")
    $previousAutoMigrate = [Environment]::GetEnvironmentVariable("AUTO_MIGRATE", "Process")
    [Environment]::SetEnvironmentVariable("WEKNORA_VERSION", $ImageTag, "Process")
    # Do not let a rollback start a forward migration after compatibility has been checked.
    [Environment]::SetEnvironmentVariable("AUTO_MIGRATE", "false", "Process")
    Invoke-Compose "up" "-d" "--no-build" "--no-deps" "--force-recreate" "app" "frontend" "docreader"
    Wait-ForApplicationHealth -Timeout $TimeoutSeconds
    $audit["auto_migrate_disabled_for_rollback"] = $true
    $audit["result"] = "success"
    Write-Host "The approved MySQL deployment image set is running and the application health check passed."
}
catch {
    $audit["result"] = "failed"
    $audit["failure"] = Protect-Message -Message $_.Exception.Message -Secrets $secretValues
    throw
}
finally {
    if (-not [string]::IsNullOrWhiteSpace($stagedConfigTemporaryPath) -and (Test-Path -LiteralPath $stagedConfigTemporaryPath -PathType Leaf)) {
        Remove-Item -LiteralPath $stagedConfigTemporaryPath -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $previousWeKnoraVersion) {
        [Environment]::SetEnvironmentVariable("WEKNORA_VERSION", $previousWeKnoraVersion, "Process")
    }
    else {
        [Environment]::SetEnvironmentVariable("WEKNORA_VERSION", $null, "Process")
    }
    if ($null -ne $previousAutoMigrate) {
        [Environment]::SetEnvironmentVariable("AUTO_MIGRATE", $previousAutoMigrate, "Process")
    }
    else {
        [Environment]::SetEnvironmentVariable("AUTO_MIGRATE", $null, "Process")
    }
    $audit["completed_at"] = [DateTime]::UtcNow.ToString("o")
    Write-AuditRecord -Record $audit -Path $auditPath
    Write-Host "Rollback audit record: $auditPath"
}
