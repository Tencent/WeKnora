param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^weknora-mysql-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{24}$')]
    [string]$BackupId,

    [Parameter(Mandatory = $true)]
    [string]$BackupDirectory,

    [Parameter(Mandatory = $true)]
    [string]$DestinationDirectory
)

$ErrorActionPreference = 'Stop'

function Assert-SafeRelativePath([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path) -or [IO.Path]::IsPathRooted($Path)) {
        throw 'File inventory contains an unsafe path.'
    }
    foreach ($part in ($Path -replace '\\', '/' -split '/')) {
        if ($part -eq '' -or $part -eq '.' -or $part -eq '..') {
            throw 'File inventory contains an unsafe path.'
        }
    }
}

$backupRoot = [IO.Path]::GetFullPath($BackupDirectory)
if (-not (Test-Path -LiteralPath $backupRoot -PathType Container)) {
    throw 'Backup directory does not exist.'
}
$manifestPath = Join-Path $backupRoot "$BackupId.manifest.json"
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw 'Backup manifest does not exist.'
}
$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
if ($manifest.backup_id -ne $BackupId -or $manifest.result -ne 'success' -or $null -eq $manifest.files) {
    throw 'Backup manifest does not describe a successful local-file archive.'
}
$files = $manifest.files
if ($files.file -ne "$BackupId.files.tar.gz" -or $files.inventory_file -ne "$BackupId.files.json") {
    throw 'Backup manifest contains unexpected file archive names.'
}
$archivePath = Join-Path $backupRoot $files.file
$inventoryPath = Join-Path $backupRoot $files.inventory_file
if (-not (Test-Path -LiteralPath $archivePath -PathType Leaf) -or -not (Test-Path -LiteralPath $inventoryPath -PathType Leaf)) {
    throw 'File archive or inventory does not exist.'
}
$archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($archiveHash -ne $files.sha256 -or (Get-Item -LiteralPath $archivePath).Length -ne [int64]$files.size_bytes) {
    throw 'File archive checksum or size does not match the manifest.'
}

$destination = [IO.Path]::GetFullPath($DestinationDirectory)
if (Test-Path -LiteralPath $destination) {
    if (-not (Test-Path -LiteralPath $destination -PathType Container) -or (Get-ChildItem -LiteralPath $destination -Force | Select-Object -First 1)) {
        throw 'Destination directory must be empty. Never use the live LOCAL_STORAGE_BASE_DIR.'
    }
} else {
    New-Item -ItemType Directory -Path $destination -Force | Out-Null
}

& tar.exe -xzf $archivePath -C $destination
if ($LASTEXITCODE -ne 0) {
    throw 'File archive extraction failed.'
}
$inventory = Get-Content -LiteralPath $inventoryPath -Raw | ConvertFrom-Json
if ($inventory.backup_id -ne $BackupId -or $inventory.format_version -ne 1 -or $inventory.files.Count -ne [int]$files.file_count) {
    throw 'File inventory does not match the manifest.'
}

$destinationPrefix = $destination.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
$totalBytes = [int64]0
foreach ($entry in $inventory.files) {
    Assert-SafeRelativePath $entry.path
    $candidate = [IO.Path]::GetFullPath((Join-Path $destination ($entry.path -replace '/', [IO.Path]::DirectorySeparatorChar)))
    if (-not $candidate.StartsWith($destinationPrefix, [StringComparison]::OrdinalIgnoreCase) -or -not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        throw 'A file listed in the inventory is missing after extraction.'
    }
    $item = Get-Item -LiteralPath $candidate
    $hash = (Get-FileHash -LiteralPath $candidate -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($item.Length -ne [int64]$entry.size_bytes -or $hash -ne $entry.sha256) {
        throw 'An extracted file does not match its inventory checksum.'
    }
    $totalBytes += $item.Length
}
if ($totalBytes -ne [int64]$files.content_bytes) {
    throw 'Extracted content size does not match the manifest.'
}

Write-Output "Local file backup verified: files=$($files.file_count) content_bytes=$totalBytes"
