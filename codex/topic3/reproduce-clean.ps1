param(
    [ValidateSet('Prepare', 'Run', 'Status', 'Restore')]
    [string]$Action = 'Status'
)

$ErrorActionPreference = 'Stop'
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$BaseCompose = Join-Path $RepoRoot 'docker-compose.yml'
$ReproCompose = Join-Path $PSScriptRoot 'docker-compose.repro.yml'
$ProjectName = 'weknora-topic3-repro'
$ReproApp = 'WeKnora-topic3-repro-app'
$ReproPostgres = 'WeKnora-topic3-repro-postgres'
$Services = @('frontend', 'app', 'docreader', 'postgres', 'redis')
$BaseUrl = 'http://localhost:8080'
$FrontendUrl = 'http://localhost'

function Invoke-DockerCompose {
    param([string[]]$Arguments)
    & docker compose --project-name $ProjectName -f $BaseCompose -f $ReproCompose @Arguments
    if ($LASTEXITCODE -ne 0) { throw "docker compose failed: $($Arguments -join ' ')" }
}

function Wait-Url {
    param([string]$Url, [int]$Seconds = 240)
    $deadline = (Get-Date).AddSeconds($Seconds)
    do {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 5
            if ($response.StatusCode -eq 200) { return }
        } catch { Start-Sleep -Seconds 3 }
    } while ((Get-Date) -lt $deadline)
    throw "service did not become healthy: $Url"
}

function Get-DotEnvValue {
    param([string]$Name, [string]$Default)
    $envPath = Join-Path $RepoRoot '.env'
    if (Test-Path $envPath) {
        $match = Get-Content -LiteralPath $envPath | Where-Object { $_ -match "^$([regex]::Escape($Name))=" } | Select-Object -Last 1
        if ($match) { return ($match -split '=', 2)[1].Trim() }
    }
    return $Default
}

function Invoke-ReproSql {
    param([string]$Sql)
    $dbUser = Get-DotEnvValue 'DB_USER' 'postgres'
    $dbName = Get-DotEnvValue 'DB_NAME' 'weknora'
    $output = & docker exec $ReproPostgres psql -U $dbUser -d $dbName -At -v ON_ERROR_STOP=1 -c $Sql
    if ($LASTEXITCODE -ne 0) { throw 'reproduction database query failed' }
    return (($output | ForEach-Object { $_.Trim() }) -join "`n").Trim()
}

function Get-DatabaseSnapshot {
    return [ordered]@{
        migration = Invoke-ReproSql 'SELECT version::text || ''|'' || dirty::text FROM schema_migrations LIMIT 1;'
        evaluations = [int](Invoke-ReproSql 'SELECT count(*) FROM evaluation_runs;')
        unfinished = [int](Invoke-ReproSql 'SELECT count(*) FROM evaluation_runs WHERE status IN (0,1);')
        usages = [int](Invoke-ReproSql 'SELECT count(*) FROM model_usages;')
        embedding_cache = [int](Invoke-ReproSql 'SELECT count(*) FROM embedding_cache_entries;')
    }
}

function Get-AuthHeaders {
    param([string]$Token)
    if ($Token.StartsWith('sk-')) { return @{ 'X-API-Key' = $Token } }
    return @{ 'Authorization' = "Bearer $Token" }
}

function Invoke-Topic3Api {
    param([string]$Method, [string]$Path, [hashtable]$Headers, [object]$Body = $null)
    $parameters = @{ Method = $Method; Uri = "$BaseUrl$Path"; Headers = $Headers; TimeoutSec = 300 }
    if ($null -ne $Body) {
        $parameters.ContentType = 'application/json'
        $parameters.Body = ($Body | ConvertTo-Json -Depth 20 -Compress)
    }
    return Invoke-RestMethod @parameters
}

function Get-OnlyDatabaseValue {
    param([string]$Sql, [string]$Description)
    $raw = Invoke-ReproSql $Sql
    $values = @($raw -split "`n" | Where-Object { $_ })
    if ($values.Count -ne 1) { throw "expected exactly one $Description, found $($values.Count)" }
    return $values[0]
}

function Restore-OriginalService {
    Write-Host '[restore] stopping isolated reproduction containers'
    try { Invoke-DockerCompose @('stop') } catch { Write-Warning $_ }
    Write-Host '[restore] starting original WeKnora containers'
    & docker compose -f $BaseCompose up -d @Services
    if ($LASTEXITCODE -ne 0) { throw 'failed to restore original WeKnora' }
    Wait-Url "$BaseUrl/health" 180
}

function Prepare-Reproduction {
    Set-Location $RepoRoot
    & docker version | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Docker engine is not available' }

    $commit = (& git rev-parse HEAD).Trim()
    $branch = (& git branch --show-current).Trim()
    Write-Host "[prepare] branch=$branch commit=$commit"
    & python codex/topic3/verify_evidence.py
    if ($LASTEXITCODE -ne 0) { throw 'frozen evidence verification failed' }
    & python -m unittest codex.topic3.test_topic3 -v
    if ($LASTEXITCODE -ne 0) { throw 'Topic 3 Python tests failed' }

    Write-Host '[prepare] stopping original containers without deleting data'
    & docker compose -f $BaseCompose stop @Services
    if ($LASTEXITCODE -ne 0) { throw 'failed to stop original WeKnora' }

    try {
        Write-Host '[prepare] removing only a previous isolated reproduction project and its volumes'
        Invoke-DockerCompose @('down', '--volumes', '--remove-orphans')
        $env:TOPIC3_CODE_COMMIT = $commit
        $env:TOPIC3_PROMPT_VERSION = 'weknora-v0.7.2-topic3'
        $env:TOPIC3_PRICE_VERSION = 'aliyun-beijing-2026-09-08'
        $env:TOPIC3_EVAL_CONCURRENCY = '1'
        $env:TOPIC3_EMBEDDING_CACHE_ENABLED = 'true'
        $env:TOPIC3_MODEL_USAGE_ENABLED = 'true'
        $env:TOPIC3_EVAL_DROP_RELEVANT = 'false'
        Write-Host '[prepare] building and starting a clean isolated environment'
        Invoke-DockerCompose (@('up', '-d', '--build') + $Services)
        Wait-Url "$BaseUrl/health" 300
        Wait-Url $FrontendUrl 180

        $snapshot = Get-DatabaseSnapshot
        if ($snapshot.migration -notmatch '^83\|f(alse)?$') { throw "unexpected migration state: $($snapshot.migration)" }
        if ($snapshot.evaluations -ne 0 -or $snapshot.usages -ne 0 -or $snapshot.embedding_cache -ne 0) {
            throw 'isolated database is not empty'
        }
        $volumes = @(& docker volume ls --filter "label=com.docker.compose.project=$ProjectName" --format '{{.Name}}')
        if ($volumes.Count -lt 3) { throw 'isolated Compose volumes were not created as expected' }

        Write-Host ''
        Write-Host 'REPRO_PREPARED'
        Write-Host 'Open http://localhost, register, and configure the three DashScope models.'
        Write-Host 'Create an empty knowledge base named topic3-reproduction and a local evaluation API key scoped to it.'
        Write-Host 'Then run: powershell -ExecutionPolicy Bypass -File .\codex\topic3\reproduce-clean.ps1 -Action Run'
    } catch {
        Restore-OriginalService
        throw
    } finally {
        Remove-Item Env:TOPIC3_CODE_COMMIT,Env:TOPIC3_PROMPT_VERSION,Env:TOPIC3_PRICE_VERSION,Env:TOPIC3_EVAL_CONCURRENCY,Env:TOPIC3_EMBEDDING_CACHE_ENABLED,Env:TOPIC3_MODEL_USAGE_ENABLED,Env:TOPIC3_EVAL_DROP_RELEVANT -ErrorAction SilentlyContinue
    }
}

function Run-Reproduction {
    Set-Location $RepoRoot
    Wait-Url "$BaseUrl/health" 60
    $before = Get-DatabaseSnapshot
    if ($before.evaluations -ne 0 -or $before.unfinished -ne 0) { throw 'reproduction database already contains an evaluation' }

    $chatId = Get-OnlyDatabaseValue "SELECT id FROM models WHERE name='qwen3.7-flash-2026-07-15' AND deleted_at IS NULL ORDER BY created_at DESC;" 'chat model'
    $embeddingId = Get-OnlyDatabaseValue "SELECT id FROM models WHERE name='text-embedding-v4' AND deleted_at IS NULL ORDER BY created_at DESC;" 'embedding model'
    $rerankId = Get-OnlyDatabaseValue "SELECT id FROM models WHERE name='qwen3-rerank' AND deleted_at IS NULL ORDER BY created_at DESC;" 'rerank model'
    $kbId = Get-OnlyDatabaseValue "SELECT id FROM knowledge_bases WHERE name='topic3-reproduction' AND is_temporary=false AND deleted_at IS NULL ORDER BY created_at DESC;" 'knowledge base named topic3-reproduction'
    $kbEmbedding = Invoke-ReproSql "SELECT embedding_model_id FROM knowledge_bases WHERE id='$kbId';"
    if ($kbEmbedding -ne $embeddingId) { throw 'topic3-reproduction knowledge base is not bound to text-embedding-v4' }

    $secure = Read-Host 'Paste the local WeKnora API Key for the clean environment (hidden)' -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    $token = $null
    $resultDir = Join-Path $PSScriptRoot ('results\clean-reproduction-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
    New-Item -ItemType Directory -Path $resultDir -Force | Out-Null
    try {
        $token = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
        $headers = Get-AuthHeaders $token
        Write-Host '[run] validating local API Key'
        Invoke-Topic3Api 'GET' '/api/v1/evaluation/history?limit=1' $headers | Out-Null

        Write-Host '[run] starting exactly one real one-question evaluation'
        $created = Invoke-Topic3Api 'POST' '/api/v1/evaluation' $headers @{
            dataset_id = 'default'; knowledge_base_id = $kbId; chat_id = $chatId; rerank_id = $rerankId
        }
        $taskId = $created.data.task.id
        $deadline = (Get-Date).AddMinutes(20)
        do {
            Start-Sleep -Seconds 3
            $current = Invoke-Topic3Api 'GET' ("/api/v1/evaluation?task_id=" + [uri]::EscapeDataString($taskId)) $headers
            $task = $current.data.task
            Write-Host "[run] evaluation $($task.finished)/$($task.total) status=$($task.status)"
            if ($task.status -eq 3) { throw "evaluation failed: $($task.err_msg)" }
        } while ($task.status -ne 2 -and (Get-Date) -lt $deadline)
        if ($task.status -ne 2 -or $task.finished -ne 1 -or $task.total -ne 1) { throw 'one-question evaluation did not complete 1/1' }
        $current.data | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath (Join-Path $resultDir 'evaluation.json') -Encoding UTF8

        Write-Host '[run] running exactly one optimized Wiki probe'
        $wiki = Invoke-Topic3Api 'POST' '/api/v1/evaluation/wiki-cache-probe' $headers @{
            chat_id = $chatId; layout = 'optimized'; sample = 0
        }
        $wiki.data | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath (Join-Path $resultDir 'wiki-prefix-probe.json') -Encoding UTF8
        if (-not $wiki.data.output_valid -or -not $wiki.data.expected_found) { throw 'Wiki probe output validation failed' }

        $afterCalls = Get-DatabaseSnapshot
        if ($afterCalls.evaluations -ne 1 -or $afterCalls.unfinished -ne 0) { throw 'evaluation persistence check failed before restart' }

        Write-Host '[run] restarting isolated app and verifying persistence'
        Invoke-DockerCompose @('restart', 'app')
        Wait-Url "$BaseUrl/health" 180
        $persisted = Invoke-Topic3Api 'GET' ("/api/v1/evaluation?task_id=" + [uri]::EscapeDataString($taskId)) $headers
        if ($persisted.data.task.status -ne 2 -or $persisted.data.task.finished -ne 1) { throw 'evaluation was not readable after app restart' }
        $afterRestart = Get-DatabaseSnapshot

        $commit = (& git rev-parse HEAD).Trim()
        $report = [ordered]@{
            status = 'REPRO_COMPLETE'
            completed_at = (Get-Date).ToUniversalTime().ToString('o')
            commit = $commit
            compose_project = $ProjectName
            isolated_volumes = @(& docker volume ls --filter "label=com.docker.compose.project=$ProjectName" --format '{{.Name}}')
            database_before = $before
            database_after = $afterRestart
            task_id = $taskId
            models = @{ chat = $chatId; embedding = $embeddingId; rerank = $rerankId }
            source_knowledge_base_id = $kbId
            evaluation = @{ status = $task.status; finished = $task.finished; total = $task.total; persisted_after_restart = $true }
            wiki = @{ output_valid = $wiki.data.output_valid; expected_found = $wiki.data.expected_found; prompt_tokens = $wiki.data.usage.prompt_tokens; prefix_fingerprint = $wiki.data.prefix_fingerprint; cache_status = $wiki.data.usage.cache_status }
            limitation = 'Provider usage reports total prompt tokens, not a separate exact token count for the stable prefix.'
        }
        $reportPath = Join-Path $resultDir 'clean-reproduction-report.json'
        $report | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $reportPath -Encoding UTF8
        $markdown = @(
            '# Topic 3 clean database reproduction report',
            '',
            '- Status: REPRO_COMPLETE',
            "- Commit: $commit",
            "- Compose project: $ProjectName",
            '- Evaluation: 1/1 completed and readable after restart',
            "- Wiki probe: output_valid=$($wiki.data.output_valid), expected_found=$($wiki.data.expected_found)",
            "- Wiki total prompt_tokens: $($wiki.data.usage.prompt_tokens)",
            "- Wiki prefix fingerprint: $($wiki.data.prefix_fingerprint)",
            "- Database: migration=$($afterRestart.migration), evaluations=$($afterRestart.evaluations), unfinished=$($afterRestart.unfinished)",
            '- Safety: API key is not stored in output files',
            '',
            'Limitation: provider usage reports total prompt tokens, not a separate exact token count for the stable prefix.'
        ) -join "`r`n"
        $markdown | Set-Content -LiteralPath (Join-Path $resultDir 'clean-reproduction-report.md') -Encoding UTF8

        $secretPattern = 'sk-[A-Za-z0-9_.-]{12,}'
        # In Windows PowerShell, Select-String -Quiet returns one Boolean per
        # input file.  An array containing only $false values still converts to
        # $true in an if expression, so inspect actual MatchInfo objects here.
        $secretHits = @(Get-ChildItem -LiteralPath $resultDir -File |
            Select-String -Pattern $secretPattern)
        if ($secretHits.Count -gt 0) { throw 'secret-like text found in reproduction output' }
        Write-Host 'REPRO_COMPLETE'
        Write-Host $resultDir
    } finally {
        if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
        $token = $null
        $secure = $null
        Restore-OriginalService
    }
}

switch ($Action) {
    'Prepare' { Prepare-Reproduction }
    'Run' { Run-Reproduction }
    'Restore' { Restore-OriginalService }
    'Status' {
        & docker compose --project-name $ProjectName -f $BaseCompose -f $ReproCompose ps
        Write-Host 'Original project:'
        & docker compose -f $BaseCompose ps
    }
}
