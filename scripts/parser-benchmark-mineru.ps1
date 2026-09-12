param(
    [ValidateSet('Install','DownloadModels','Start','Stop','Status')]
    [string]$Action = 'Status',
    [string]$StateDirectory = (Join-Path $PSScriptRoot '../artifacts/parser-benchmark/mineru-state'),
    [string]$Network = 'weknora-parser-benchmark',
    [int]$Port = 18081
)
$ErrorActionPreference = 'Stop'
$deploymentDirectory = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../deploy/parser-benchmark/mineru'))
$statePath = [IO.Path]::GetFullPath($StateDirectory)
$containerName = 'weknora-parser-mineru'
# Reuses the installed Python 3.10 runtime without rebuilding the application image.
$image = 'wechatopenai/weknora-docreader@sha256:b9c4636b65b5d4947d5e09cd311ba6cf37f1f2da37c51d4be2b911d432f12abe'
function Assert-DockerSuccess {
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed ($LASTEXITCODE)." }
}
if ($Action -eq 'Install') {
    New-Item -ItemType Directory -Path $statePath -Force | Out-Null
    $existing = docker ps -a --filter "name=^/$containerName$" --format '{{.Names}}'
    Assert-DockerSuccess
    if (-not $existing) {
        docker run -d --name $containerName --network $Network --memory 8g --memory-swap 10g --cpus 6 `
          -p "127.0.0.1:${Port}:8000" `
          --mount "type=bind,source=$statePath,target=/state" `
          --mount "type=bind,source=$deploymentDirectory,target=/deployment,readonly" `
          -e HOME=/state/home -e XDG_CACHE_HOME=/state/cache -e HF_HOME=/state/huggingface `
          -e MODELSCOPE_CACHE=/state/modelscope -e MINERU_MODEL_SOURCE=modelscope `
          -e MINERU_DEVICE_MODE=cpu -e MINERU_VIRTUAL_VRAM_SIZE=2 `
          -e OMP_NUM_THREADS=4 -e MKL_NUM_THREADS=4 -e OPENBLAS_NUM_THREADS=4 `
          -e TMPDIR=/state/tmp -e MINERU_API_OUTPUT_ROOT=/state/output `
          --entrypoint sh $image -c 'sleep infinity'
        Assert-DockerSuccess
    }
    docker start $containerName | Out-Null
    Assert-DockerSuccess
    docker exec $containerName sh /deployment/bootstrap.sh
    Assert-DockerSuccess
} elseif ($Action -eq 'DownloadModels') {
    docker exec $containerName /opt/mineru-venv/bin/python /deployment/download_models.py
    Assert-DockerSuccess
} elseif ($Action -eq 'Start') {
    try {
        $health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 2
        $ready = $health.status -eq 'healthy' -and $health.version -eq '3.4.5'
    } catch { $ready = $false }
    if ($ready) { Write-Output "MinerU is already ready at http://127.0.0.1:$Port"; exit 0 }
    try { $httpPresent = (Invoke-WebRequest -Uri "http://127.0.0.1:$Port/docs" -TimeoutSec 2).StatusCode -eq 200 }
    catch { $httpPresent = $false }
    if ($httpPresent) { throw 'MinerU HTTP is reachable but health validation failed. Inspect server.log; stop and start this container after active tasks finish.' }
    docker start $containerName | Out-Null
    Assert-DockerSuccess
    docker exec -d $containerName sh -c 'exec sh /deployment/serve.sh >> /state/server.log 2>&1'
    Assert-DockerSuccess
    $deadline = (Get-Date).AddSeconds(45)
    while ((Get-Date) -lt $deadline) {
        try {
            $health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 2
            if ($health.status -eq 'healthy' -and $health.version -eq '3.4.5') {
                Write-Output "MinerU API is ready at http://127.0.0.1:$Port (version $($health.version)); logs: $statePath\server.log"
                exit 0
            }
        } catch { }
        Start-Sleep -Milliseconds 500
    }
    throw "MinerU API did not become healthy in 45 seconds. Inspect $statePath\server.log."
} elseif ($Action -eq 'Stop') {
    docker stop $containerName
    Assert-DockerSuccess
} else {
    docker ps -a --filter "name=^/$containerName$" --format '{{.Names}} {{.Status}} {{.Ports}}'
    Assert-DockerSuccess
    try {
        $health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/health" -TimeoutSec 5
        $health | ConvertTo-Json -Compress
        if ($health.status -ne 'healthy' -or $health.version -ne '3.4.5') { throw 'Unexpected MinerU health response.' }
    }
    catch { throw "MinerU API is not ready at http://127.0.0.1:$Port. Inspect $statePath\server.log." }
}
