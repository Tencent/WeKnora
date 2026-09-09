param(
    [ValidateSet('up', 'eval', 'check', 'cache-bench', 'preflight', 'wiki-bench', 'postcheck', 'all')]
    [string]$Action = 'eval',
    [string]$Current = '',
    [string]$Baseline = '',
    [ValidateSet('default', 'topic3-10')]
    [string]$Dataset = ''
)
$ErrorActionPreference = 'Stop'

if ($Action -in @('eval', 'cache-bench', 'preflight', 'wiki-bench', 'all')) {
    if (-not $env:TOPIC3_TOKEN) {
        $secureToken = Read-Host '请输入本地“课题三评测”API Key（输入内容不会显示）' -AsSecureString
        $tokenPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureToken)
        try {
            $env:TOPIC3_TOKEN = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($tokenPointer)
        }
        finally {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($tokenPointer)
        }
    }
    if (-not $env:TOPIC3_BASE_URL) { $env:TOPIC3_BASE_URL = 'http://localhost:8080' }
    if (-not $env:TOPIC3_KB_ID) { $env:TOPIC3_KB_ID = '22459db3-db22-4682-9e9b-0b06a382f533' }
    if (-not $env:TOPIC3_CHAT_MODEL_ID) { $env:TOPIC3_CHAT_MODEL_ID = '8b9e4baa-ed26-4bd2-a940-fd51d7390a69' }
    if (-not $env:TOPIC3_RERANK_MODEL_ID) { $env:TOPIC3_RERANK_MODEL_ID = '63a4ddf0-f689-42a1-8208-9ca57a234234' }
    if (-not $env:TOPIC3_EMBEDDING_MODEL_ID) { $env:TOPIC3_EMBEDDING_MODEL_ID = '6c09f3b5-9b65-43c9-b67b-7d1576cb1ac6' }
    if ($Dataset) { $env:TOPIC3_DATASET_ID = $Dataset }
    if (-not $env:TOPIC3_DATASET_ID) { $env:TOPIC3_DATASET_ID = 'default' }
    if (-not $env:TOPIC3_CODE_COMMIT) {
        $commit = (git rev-parse HEAD).Trim()
        if (git status --porcelain) { $commit += '-dirty' }
        $env:TOPIC3_CODE_COMMIT = $commit
    }
    if ($Action -eq 'all') {
        $env:TOPIC3_DATASET_ID = 'topic3-10'
        $env:TOPIC3_CACHE_BENCH_CONFIRM = 'YES'
        $env:TOPIC3_ALL_CONFIRM = 'YES'
        $env:TOPIC3_BATCH_COST_LIMIT_CNY = '5'
        $env:TOPIC3_PRICE_VERSION = 'aliyun-beijing-2026-09-08-qwen3.7-flash-lte32k'
        $env:TOPIC3_CHAT_INPUT_PRICE_PER_MILLION_CNY = '0.2'
        $env:TOPIC3_CHAT_CACHED_INPUT_PRICE_PER_MILLION_CNY = '0.04'
        $env:TOPIC3_CHAT_OUTPUT_PRICE_PER_MILLION_CNY = '0.8'
    }
}

$arguments = @('codex/topic3/topic3.py', $Action)
if ($Current) { $arguments += @('--current', $Current) }
if ($Baseline) { $arguments += @('--baseline', $Baseline) }
try {
    python @arguments
    $topic3ExitCode = $LASTEXITCODE
}
finally {
    $env:TOPIC3_TOKEN = $null
    $env:TOPIC3_CACHE_BENCH_CONFIRM = $null
    $env:TOPIC3_ALL_CONFIRM = $null
    $env:TOPIC3_EVAL_DROP_RELEVANT = $null
}
exit $topic3ExitCode
