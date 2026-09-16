param(
    [Parameter(Mandatory=$true)][string]$PdfPath,
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '../artifacts/parser-benchmark/mineru-state/smoke'),
    [string]$Endpoint = 'http://127.0.0.1:18081',
    [string]$Language = 'ch'
)
$ErrorActionPreference = 'Stop'
$source = Get-Item -LiteralPath $PdfPath
if ($source.Extension -ne '.pdf') { throw 'A PDF input is required.' }
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$form = @{
    files = $source
    return_md = 'true'
    return_images = 'true'
    table_enable = 'true'
    formula_enable = 'true'
    parse_method = 'auto'
    start_page_id = '0'
    end_page_id = '0'
    backend = 'pipeline'
    response_format_zip = 'false'
    return_middle_json = 'false'
    return_model_output = 'false'
    return_content_list = 'true'
    lang_list = $Language
}
$timer = [Diagnostics.Stopwatch]::StartNew()
$response = Invoke-WebRequest -Uri "$Endpoint/file_parse" -Method Post -Form $form -TimeoutSec 1000 -SkipHttpErrorCheck
$timer.Stop()
$memoryPeak = docker exec weknora-parser-mineru cat /sys/fs/cgroup/memory.peak
$memoryCurrent = docker exec weknora-parser-mineru cat /sys/fs/cgroup/memory.current
$rawPath = Join-Path $OutputDirectory ($source.BaseName + '.response.json')
[IO.File]::WriteAllText($rawPath, $response.Content)
if ($response.StatusCode -ne 200) { throw "MinerU HTTP $($response.StatusCode). Raw response: $rawPath" }
$body = $response.Content | ConvertFrom-Json -AsHashtable
$entry = $body.results[$source.BaseName]
if (-not $entry -or -not $entry.md_content) { throw 'No Markdown found under results.<filename stem>.md_content.' }
[IO.File]::WriteAllText((Join-Path $OutputDirectory ($source.BaseName + '.md')), $entry.md_content)
$result = [ordered]@{
    timestamp_utc = (Get-Date).ToUniversalTime().ToString('o')
    engine = 'mineru'
    version = $body.version
    backend = $body.backend
    http_status = $response.StatusCode
    elapsed_seconds = [Math]::Round($timer.Elapsed.TotalSeconds,3)
    input_sha256 = (Get-FileHash -LiteralPath $source.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    input_filename = $source.Name
    pages_requested = 1
    language = $Language
    markdown_characters = $entry.md_content.Length
    images_count = $entry.images.Count
    content_list_present = [bool]$entry.content_list
    container_memory_peak_bytes = [long]$memoryPeak
    container_memory_current_bytes = [long]$memoryCurrent
    memory_scope = 'Docker cgroup since the current container start'
    compatibility = 'results.<filename stem>.md_content and images accepted by WeKnora MinerUReader'
}
$result | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $OutputDirectory ($source.BaseName + '.metrics.json')) -Encoding utf8
$result | ConvertTo-Json -Depth 5
