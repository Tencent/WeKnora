param(
    [ValidateSet('build', 'prepare', 'start', 'stop', 'status')]
    [string]$Action = 'status'
)
$ErrorActionPreference = 'Stop'
$repositoryRoot = Split-Path $PSScriptRoot -Parent
$deploymentPath = Join-Path $repositoryRoot 'deploy/parser-benchmark/paddle'
$runtimePath = Join-Path $deploymentPath '.runtime'
$imageName = 'weknora-parser-paddle:ocr3.7.0-pdx3.7.2-cpu'
$containerName = 'weknora-parser-paddle'
New-Item -ItemType Directory -Force -Path $runtimePath | Out-Null
switch ($Action) {
    'build' {
        docker build -t $imageName $deploymentPath
    }
    'prepare' {
        docker run --rm --name weknora-parser-paddle-prepare --memory 2g --cpus 2 --mount "type=bind,source=$runtimePath,target=/runtime" $imageName python /opt/paddle/prepare.py
    }
    'start' {
        $existing = docker ps -a --filter "name=^/$containerName$" --format '{{.Names}}'
        if ($existing -eq $containerName) {
            docker start $containerName
        } else {
            docker run -d --name $containerName --network weknora-parser-benchmark --memory 9g --cpus 6 --shm-size 1g -p 127.0.0.1:18082:8080 --mount "type=bind,source=$runtimePath,target=/runtime" $imageName
        }
    }
    'stop' { docker stop --timeout 30 $containerName }
    'status' {
        docker ps -a --filter "name=^/$containerName$" --format '{{.Names}} {{.Status}} {{.Ports}}'
    }
}
if ($LASTEXITCODE -ne 0) { throw "Docker operation failed with code $LASTEXITCODE" }
