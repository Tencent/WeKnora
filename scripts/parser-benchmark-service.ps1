param(
    [ValidateSet('Start','Stop','Status')][string]$Action = 'Status',
    [string]$Python = 'python',
    [ValidateRange(1,65535)][int]$ReportPort = 18090
)
$ErrorActionPreference = 'Stop'
$repository = Split-Path -Parent $PSScriptRoot
$state = Join-Path $repository 'artifacts/parser-benchmark'
$report = Join-Path $state 'report'
$pidFile = Join-Path $state 'report-server.pid'
$reader = 'weknora-parser-docreader'
function Assert-Docker { if ($LASTEXITCODE -ne 0) { throw "Docker failed ($LASTEXITCODE)." } }
function Test-DocReaderHealth {
    $probeOutput = docker exec $reader grpc_health_probe '-addr=localhost:50051' '-connect-timeout=2s' '-rpc-timeout=2s' 2>&1
    return $LASTEXITCODE -eq 0
}
function Test-ReportHealth {
    try {
        $response = Invoke-RestMethod -Uri "http://127.0.0.1:$ReportPort/status-summary.json" -TimeoutSec 2
        foreach ($engine in @('builtin','markitdown','opendataloader','weknoracloud','mineru','mineru_cloud','paddleocr_vl','paddleocr_vl_cloud')) {
            if ($null -eq $response.$engine) { return $false }
        }
        return $true
    } catch { return $false }
}
function Wait-DocReaderHealth {
    $deadline = (Get-Date).AddSeconds(60)
    do {
        if (Test-DocReaderHealth) { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "DocReader gRPC did not become healthy in 60 seconds. Inspect docker logs $reader. Existing services and data are retained."
}
function Wait-ReportHealth($serverProcess) {
    $deadline = (Get-Date).AddSeconds(30)
    do {
        if (Test-ReportHealth) { return }
        if ($serverProcess) {
            $serverProcess.Refresh()
            if ($serverProcess.HasExited) { throw "Report HTTP server exited before becoming healthy. Inspect $state\report-server-error.log." }
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "Report HTTP did not become healthy in 30 seconds at http://127.0.0.1:$ReportPort. Existing services and data are retained."
}
if ($Action -eq 'Start') {
    New-Item -ItemType Directory -Force $state | Out-Null
    if (-not (Test-Path -LiteralPath (Join-Path $report 'index.html'))) { throw 'Build the benchmark report first.' }
    $existing = docker ps -a --filter "name=^/$reader$" --format '{{.Names}}'
    Assert-Docker
    if ($existing) { docker start $reader | Out-Null; Assert-Docker }
    else {
        docker run -d --name $reader --network weknora-parser-benchmark --memory 2g --cpus 2 `
            -e JAVA_TOOL_OPTIONS=-Xmx768m -e DOCREADER_ODL_MAX_WORKERS=1 `
            --mount "type=bind,source=$repository/docreader,target=/app/docreader,readonly" `
            --entrypoint /app/.venv/bin/python `
            wechatopenai/weknora-docreader@sha256:b9c4636b65b5d4947d5e09cd311ba6cf37f1f2da37c51d4be2b911d432f12abe `
            -m docreader.main
        Assert-Docker
    }
    Wait-DocReaderHealth
    $ready = Test-ReportHealth
    $serverProcess = $null
    if (-not $ready -and (Test-Path -LiteralPath $pidFile)) {
        try {
            $existingReportPid = [int](Get-Content -LiteralPath $pidFile -Raw)
            $candidate = Get-CimInstance Win32_Process -Filter "ProcessId = $existingReportPid"
            if ($candidate -and $candidate.CommandLine -and $candidate.CommandLine.Contains('http.server') -and $candidate.CommandLine.Contains($report)) {
                $serverProcess = Get-Process -Id $existingReportPid -ErrorAction Stop
            }
        } catch { $serverProcess = $null }
    }
    if (-not $ready -and -not $serverProcess) {
        $interpreter = (Get-Command $Python -ErrorAction Stop).Source
        $serverProcess = Start-Process -FilePath $interpreter -ArgumentList @('-m','http.server',"$ReportPort",'--bind','127.0.0.1','--directory',('"' + $report + '"')) `
            -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $state 'report-server.log') `
            -RedirectStandardError (Join-Path $state 'report-server-error.log')
        Set-Content -LiteralPath $pidFile -Value $serverProcess.Id
    }
    Wait-ReportHealth $serverProcess
    Write-Output "DocReader gRPC is healthy. Report HTTP is ready: http://127.0.0.1:$ReportPort"
} elseif ($Action -eq 'Stop') {
    docker stop --time 30 $reader; Assert-Docker
    if (Test-Path -LiteralPath $pidFile) {
        $reportProcessId = [int](Get-Content -LiteralPath $pidFile -Raw)
        $process = Get-CimInstance Win32_Process -Filter "ProcessId = $reportProcessId"
        # A stale PID must never stop an unrelated application.
        if ($process -and $process.CommandLine.Contains('http.server') -and $process.CommandLine.Contains($report)) {
            Stop-Process -Id $reportProcessId
        }
    }
    Write-Output 'Benchmark reader and report stopped; evidence and model data retained.'
} else {
    docker ps -a --filter "name=weknora-parser-" --format '{{.Names}} {{.Status}}'
    Assert-Docker
    $readerReady = Test-DocReaderHealth
    $reportReady = Test-ReportHealth
    [pscustomobject]@{DocReaderGRPCHealthy=$readerReady;ReportHTTPHealthy=$reportReady;ReportURL="http://127.0.0.1:$ReportPort"} | ConvertTo-Json -Compress
    if (-not $readerReady -or -not $reportReady) { exit 1 }
}
