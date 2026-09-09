$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$runner = Join-Path $repo 'codex\topic3\topic3.ps1'
if (-not (Test-Path -LiteralPath $runner)) {
    throw "Topic 3 runner not found: $runner"
}

Push-Location $repo
try {
    & powershell -ExecutionPolicy Bypass -File $runner all
    exit $LASTEXITCODE
}
finally {
    Pop-Location
}
