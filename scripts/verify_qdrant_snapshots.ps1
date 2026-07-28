param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^weknora-mysql-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{24}$')]
    [string]$BackupId,

    [Parameter(Mandatory = $true)]
    [string]$BackupDirectory,

    [switch]$KeepContainer
)

$ErrorActionPreference = 'Stop'
$containerName = "weknora-qdrant-restore-$([Guid]::NewGuid().ToString('N').Substring(0, 12))"
$port = Get-Random -Minimum 20000 -Maximum 40000

function Assert-SafeName([string]$Name) {
    if ([string]::IsNullOrWhiteSpace($Name) -or $Name -notmatch '^[A-Za-z0-9_.-]+$' -or $Name -eq '.' -or $Name -eq '..') {
        throw 'Snapshot manifest contains an unsafe name.'
    }
}

function Invoke-SnapshotUpload([string]$Url, [string]$Collection, [string]$Path) {
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $client = [System.Net.Http.HttpClient]::new($handler)
    try {
        $content = [System.Net.Http.MultipartFormDataContent]::new()
        $stream = [System.IO.File]::OpenRead($Path)
        try {
            $fileContent = [System.Net.Http.StreamContent]::new($stream)
            $fileContent.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::Parse('application/octet-stream')
            $content.Add($fileContent, 'snapshot', [System.IO.Path]::GetFileName($Path))
            $response = $client.PostAsync("$Url/collections/$([Uri]::EscapeDataString($Collection))/snapshots/upload?wait=true", $content).GetAwaiter().GetResult()
            if (-not $response.IsSuccessStatusCode) {
                throw 'Temporary Qdrant rejected a snapshot upload.'
            }
        } finally {
            $stream.Dispose()
            $content.Dispose()
        }
    } finally {
        $client.Dispose()
        $handler.Dispose()
    }
}

$backupRoot = [IO.Path]::GetFullPath($BackupDirectory)
$manifestPath = Join-Path $backupRoot "$BackupId.manifest.json"
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw 'Backup manifest does not exist.'
}
$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
$snapshots = @($manifest.qdrant)
if ($manifest.backup_id -ne $BackupId -or $manifest.result -ne 'success' -or $snapshots.Count -eq 0) {
    throw 'Backup manifest does not describe Qdrant snapshots.'
}
foreach ($snapshot in $snapshots) {
    Assert-SafeName $snapshot.collection
    Assert-SafeName $snapshot.file
    if ($snapshot.file -notmatch "^$([Regex]::Escape($BackupId))\.qdrant\.[0-9a-f]{16}\.snapshot$") {
        throw 'Snapshot manifest contains an unexpected snapshot filename.'
    }
    $snapshotPath = Join-Path $backupRoot $snapshot.file
    if (-not (Test-Path -LiteralPath $snapshotPath -PathType Leaf)) {
        throw 'A Qdrant snapshot file is missing.'
    }
    $item = Get-Item -LiteralPath $snapshotPath
    $hash = (Get-FileHash -LiteralPath $snapshotPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($item.Length -ne [int64]$snapshot.size_bytes -or $hash -ne $snapshot.sha256) {
        throw 'A Qdrant snapshot checksum or size does not match the manifest.'
    }
}

try {
    docker run --detach --name $containerName --publish "127.0.0.1:${port}:6333" qdrant/qdrant:v1.16.2 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not start the temporary Qdrant container.' }
    $baseURL = "http://127.0.0.1:$port"
    $deadline = (Get-Date).AddSeconds(60)
    do {
        try {
            Invoke-WebRequest -UseBasicParsing "$baseURL/readyz" -TimeoutSec 3 | Out-Null
            break
        } catch {
            Start-Sleep -Seconds 2
        }
    } while ((Get-Date) -lt $deadline)
    if ((Get-Date) -ge $deadline) { throw 'Temporary Qdrant did not become ready.' }

    foreach ($snapshot in $snapshots) {
        Invoke-SnapshotUpload $baseURL $snapshot.collection (Join-Path $backupRoot $snapshot.file)
        $collection = Invoke-RestMethod -Method Get -Uri "$baseURL/collections/$([Uri]::EscapeDataString($snapshot.collection))" -TimeoutSec 15
        if ($null -eq $collection.result) { throw 'Temporary Qdrant did not report the restored collection.' }
    }
    Write-Output "Qdrant snapshots restored and verified in isolated container: collections=$($snapshots.Count)"
} finally {
    if (-not $KeepContainer) {
        docker rm --force $containerName 2>$null | Out-Null
    } else {
        Write-Output "Temporary Qdrant container retained: $containerName"
    }
}
