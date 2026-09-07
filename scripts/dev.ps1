#Requires -Version 5.1

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('check', 'start', 'stop', 'restart', 'logs', 'status', 'app', 'frontend', 'help')]
    [string]$Command = 'help',

    [string]$GccBin = $env:WEKNORA_GCC_BIN,
    [string]$SqliteInclude = $env:WEKNORA_SQLITE_INCLUDE,

    [switch]$Minio,
    [switch]$Qdrant,
    [switch]$OpenSearch,
    [switch]$Milvus,
    [switch]$Neo4j,
    [switch]$Searxng,
    [switch]$Dex,
    [switch]$OdlHybrid,
    [switch]$Full,
    [switch]$NoLangfuse,
    [switch]$NoAir
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ScriptDirectory = Split-Path -Parent $PSCommandPath
$script:ProjectRoot = Split-Path -Parent $script:ScriptDirectory
$script:ComposeExecutable = $null
$script:ComposePrefix = @()
$script:ResolvedGcc = $null
$script:ResolvedGxx = $null
$script:ResolvedSqliteInclude = $null

function Write-Info {
    param([string]$Message)
    Write-Host "[INFO] $Message" -ForegroundColor Cyan
}

function Write-Success {
    param([string]$Message)
    Write-Host "[OK]   $Message" -ForegroundColor Green
}

function Write-WarningMessage {
    param([string]$Message)
    Write-Host "[WARN] $Message" -ForegroundColor Yellow
}

function Write-ErrorMessage {
    param([string]$Message)
    Write-Host "[FAIL] $Message" -ForegroundColor Red
}

function Invoke-CapturedCommand {
    param(
        [string]$FilePath,
        [string[]]$ArgumentList = @()
    )

    # Windows PowerShell 5.1 wraps native stderr as ErrorRecord objects. With
    # ErrorActionPreference=Stop, otherwise-successful tools such as `gcc -v`
    # can terminate the whole script. Capture both streams without promoting
    # native stderr to a terminating PowerShell error.
    $previousErrorActionPreference = $ErrorActionPreference
    $output = @()
    $exitCode = 1
    try {
        $ErrorActionPreference = 'Continue'
        $output = & $FilePath @ArgumentList 2>&1
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    return @{
        ExitCode = $exitCode
        Output = (($output | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine)
    }
}

function Import-DotEnvFile {
    param([string]$Path)

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return
    }

    foreach ($rawLine in [IO.File]::ReadAllLines($Path)) {
        $line = $rawLine.Trim()
        if ([string]::IsNullOrWhiteSpace($line) -or $line.StartsWith('#')) {
            continue
        }
        if ($line.StartsWith('export ')) {
            $line = $line.Substring(7).TrimStart()
        }

        $separator = $line.IndexOf('=')
        if ($separator -lt 1) {
            Write-WarningMessage "Ignoring malformed environment entry in $Path"
            continue
        }

        $name = $line.Substring(0, $separator).Trim()
        if ($name -notmatch '^[A-Za-z_][A-Za-z0-9_]*$') {
            Write-WarningMessage "Ignoring invalid environment variable '$name' in $Path"
            continue
        }

        $value = $line.Substring($separator + 1).Trim()
        if ($value.Length -ge 2) {
            $first = $value[0]
            $last = $value[$value.Length - 1]
            if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }

        [Environment]::SetEnvironmentVariable($name, $value, 'Process')
    }
}

function Import-ProjectEnvironment {
    param([switch]$Required)

    $envPath = Join-Path $script:ProjectRoot '.env'
    $localEnvPath = Join-Path $script:ProjectRoot '.env.local'
    if (-not (Test-Path -LiteralPath $envPath -PathType Leaf)) {
        if ($Required) {
            Write-ErrorMessage '.env is missing. Run: Copy-Item .env.example .env'
            return $false
        }
    }
    else {
        Import-DotEnvFile -Path $envPath
    }

    if (Test-Path -LiteralPath $localEnvPath -PathType Leaf) {
        Write-Info 'Loading .env.local overrides'
        Import-DotEnvFile -Path $localEnvPath
    }
    return $true
}

function Resolve-GccExecutable {
    param([string]$Hint)

    $candidates = New-Object System.Collections.Generic.List[string]
    if (-not [string]::IsNullOrWhiteSpace($Hint)) {
        if (Test-Path -LiteralPath $Hint -PathType Container) {
            $candidates.Add((Join-Path $Hint 'gcc.exe'))
        }
        else {
            $candidates.Add($Hint)
        }
    }

    $gccCommand = Get-Command gcc.exe -ErrorAction SilentlyContinue
    if ($null -ne $gccCommand) {
        $candidates.Add($gccCommand.Source)
    }
    $candidates.Add('C:\msys64\ucrt64\bin\gcc.exe')

    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    return $null
}

function Resolve-SqliteIncludeDirectory {
    param(
        [string]$Hint,
        [string]$CompilerDirectory
    )

    $candidates = New-Object System.Collections.Generic.List[string]
    if (-not [string]::IsNullOrWhiteSpace($Hint)) {
        if ((Test-Path -LiteralPath $Hint -PathType Leaf) -and
            ([IO.Path]::GetFileName($Hint) -ieq 'sqlite3.h')) {
            $candidates.Add((Split-Path -Parent $Hint))
        }
        else {
            $candidates.Add($Hint)
        }
    }

    if (-not [string]::IsNullOrWhiteSpace($CompilerDirectory)) {
        $candidates.Add((Join-Path (Split-Path -Parent $CompilerDirectory) 'include'))
    }
    if (-not [string]::IsNullOrWhiteSpace($env:MSYSTEM_PREFIX)) {
        $candidates.Add((Join-Path $env:MSYSTEM_PREFIX 'include'))
    }
    $candidates.Add('C:\msys64\ucrt64\include')

    foreach ($candidate in $candidates) {
        $header = Join-Path $candidate 'sqlite3.h'
        if (Test-Path -LiteralPath $header -PathType Leaf) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    return $null
}

function Test-PlatformAndGo {
    $ok = $true
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        Write-ErrorMessage 'This script supports native Windows only. Use scripts/dev.sh on Unix or WSL.'
        $ok = $false
    }
    else {
        Write-Success "Windows detected (PowerShell $($PSVersionTable.PSVersion))"
    }

    $goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($null -eq $goCommand) {
        Write-ErrorMessage 'Go is not installed or go.exe is not on PATH.'
        return $false
    }

    $versionResult = Invoke-CapturedCommand -FilePath $goCommand.Source -ArgumentList @('version')
    $versionMatch = [regex]::Match($versionResult.Output, 'go version go([0-9]+(?:\.[0-9]+){1,2})')
    if (-not $versionMatch.Success) {
        Write-ErrorMessage "Unable to parse Go version: $($versionResult.Output)"
        $ok = $false
    }
    else {
        $installedVersion = [version]$versionMatch.Groups[1].Value
        $requiredLine = Select-String -LiteralPath (Join-Path $script:ProjectRoot 'go.mod') -Pattern '^go\s+([0-9.]+)' | Select-Object -First 1
        if ($null -eq $requiredLine -or $requiredLine.Matches.Count -eq 0) {
            Write-ErrorMessage 'Unable to read the required Go version from go.mod.'
            $ok = $false
        }
        else {
            $requiredVersion = [version]$requiredLine.Matches[0].Groups[1].Value
            if ($installedVersion -lt $requiredVersion) {
                Write-ErrorMessage "Go $installedVersion is too old; WeKnora requires Go $requiredVersion or newer."
                $ok = $false
            }
            else {
                Write-Success "Go $installedVersion (required: $requiredVersion)"
            }
        }
    }

    $goEnvResult = Invoke-CapturedCommand -FilePath $goCommand.Source -ArgumentList @('env', 'GOOS', 'GOARCH')
    $goEnvValues = @($goEnvResult.Output -split '\r?\n' | ForEach-Object { $_.Trim() })
    if ($goEnvResult.ExitCode -ne 0 -or
        $goEnvValues -notcontains 'windows' -or $goEnvValues -notcontains 'amd64') {
        Write-ErrorMessage 'The DuckDB prebuilt library requires native windows/amd64 Go. Windows ARM64 is not supported.'
        $ok = $false
    }
    else {
        Write-Success 'Go target is windows/amd64'
    }
    return $ok
}

function Test-WindowsToolchain {
    $ok = $true
    $gcc = Resolve-GccExecutable -Hint $GccBin
    if ([string]::IsNullOrWhiteSpace($gcc)) {
        Write-ErrorMessage 'gcc.exe was not found. Set WEKNORA_GCC_BIN or pass -GccBin.'
        return $false
    }

    $compilerDirectory = Split-Path -Parent $gcc
    $gxx = Join-Path $compilerDirectory 'g++.exe'
    if (-not (Test-Path -LiteralPath $gxx -PathType Leaf)) {
        Write-ErrorMessage "g++.exe is missing next to $gcc"
        return $false
    }

    $targetResult = Invoke-CapturedCommand -FilePath $gcc -ArgumentList @('-dumpmachine')
    if ($targetResult.ExitCode -ne 0 -or $targetResult.Output.Trim() -ne 'x86_64-w64-mingw32') {
        Write-ErrorMessage "GCC target must be x86_64-w64-mingw32; found '$($targetResult.Output.Trim())'."
        $ok = $false
    }

    $versionResult = Invoke-CapturedCommand -FilePath $gcc -ArgumentList @('-dumpfullversion', '-dumpversion')
    $versionMatch = [regex]::Match($versionResult.Output, '[0-9]+\.[0-9]+(?:\.[0-9]+)?')
    if (-not $versionMatch.Success) {
        Write-ErrorMessage "Unable to parse GCC version: $($versionResult.Output)"
        $ok = $false
    }
    else {
        $gccVersion = [version]$versionMatch.Value
        if ($gccVersion.Major -ge 16) {
            Write-ErrorMessage "GCC $gccVersion uses native TLS and cannot link DuckDB's GCC 14.2 prebuilt static library."
            Write-ErrorMessage 'Use the DuckDB-compatible GCC 14.2 UCRT toolchain documented in the development guide, or use WSL2.'
            $ok = $false
        }
        elseif ($gccVersion.Major -eq 14 -and $gccVersion.Minor -eq 2) {
            Write-Success "GCC $gccVersion matches the DuckDB Windows build toolchain"
        }
        else {
            Write-WarningMessage "GCC $gccVersion is below the known GCC 16 incompatibility, but only GCC 14.2 is validated."
        }
    }

    $detailsResult = Invoke-CapturedCommand -FilePath $gcc -ArgumentList @('-v')
    if ($detailsResult.Output -notmatch '(?i)ucrt') {
        Write-ErrorMessage 'The compiler is not an UCRT toolchain. DuckDB Windows artifacts require the UCRT variant.'
        $ok = $false
    }
    else {
        Write-Success 'UCRT compiler runtime detected'
    }

    $sqliteDirectory = Resolve-SqliteIncludeDirectory -Hint $SqliteInclude -CompilerDirectory $compilerDirectory
    if ([string]::IsNullOrWhiteSpace($sqliteDirectory)) {
        Write-ErrorMessage 'sqlite3.h was not found. Install mingw-w64-ucrt-x86_64-sqlite3 and set WEKNORA_SQLITE_INCLUDE.'
        $ok = $false
    }
    else {
        Write-Success "SQLite header: $(Join-Path $sqliteDirectory 'sqlite3.h')"
    }

    if ($ok) {
        $script:ResolvedGcc = $gcc
        $script:ResolvedGxx = $gxx
        $script:ResolvedSqliteInclude = $sqliteDirectory
    }
    return $ok
}

function Set-WindowsCgoEnvironment {
    $compilerDirectory = Split-Path -Parent $script:ResolvedGcc
    $env:Path = "$compilerDirectory;$env:Path"
    $env:CGO_ENABLED = '1'
    $env:CC = $script:ResolvedGcc
    $env:CXX = $script:ResolvedGxx

    # -idirafter supplies sqlite3.h without shadowing the selected compiler's
    # own UCRT system headers with headers from a second toolchain.
    $includePath = $script:ResolvedSqliteInclude.Replace('\', '/')
    $cgoFlags = @()
    if (-not [string]::IsNullOrWhiteSpace($env:CGO_CFLAGS)) {
        $cgoFlags += $env:CGO_CFLAGS.Trim()
    }
    $cgoFlags += '-Wno-deprecated-declarations'
    $cgoFlags += "-idirafter `"$includePath`""
    $env:CGO_CFLAGS = $cgoFlags -join ' '

    if ([string]::IsNullOrWhiteSpace($env:GOLANG_PROTOBUF_REGISTRATION_CONFLICT)) {
        $env:GOLANG_PROTOBUF_REGISTRATION_CONFLICT = 'warn'
    }

    Write-Info "CC=$($env:CC)"
    Write-Info "CGO_CFLAGS=$($env:CGO_CFLAGS)"
}

function Find-DockerCompose {
    $script:ComposeExecutable = $null
    $script:ComposePrefix = @()

    $dockerCommand = Get-Command docker.exe -ErrorAction SilentlyContinue
    if ($null -ne $dockerCommand) {
        $composeResult = Invoke-CapturedCommand -FilePath $dockerCommand.Source -ArgumentList @('compose', 'version')
        if ($composeResult.ExitCode -eq 0) {
            $script:ComposeExecutable = $dockerCommand.Source
            $script:ComposePrefix = @('compose')
            return $true
        }
    }

    $legacyCommand = Get-Command docker-compose.exe -ErrorAction SilentlyContinue
    if ($null -ne $legacyCommand) {
        $legacyResult = Invoke-CapturedCommand -FilePath $legacyCommand.Source -ArgumentList @('version')
        if ($legacyResult.ExitCode -eq 0) {
            $script:ComposeExecutable = $legacyCommand.Source
            return $true
        }
    }
    return $false
}

function Test-DockerEngine {
    if (-not (Find-DockerCompose)) {
        Write-ErrorMessage 'Docker Compose was not found.'
        return $false
    }

    $dockerCommand = Get-Command docker.exe -ErrorAction SilentlyContinue
    if ($null -eq $dockerCommand) {
        Write-ErrorMessage 'docker.exe was not found.'
        return $false
    }
    $infoResult = Invoke-CapturedCommand -FilePath $dockerCommand.Source -ArgumentList @('info')
    if ($infoResult.ExitCode -ne 0) {
        Write-ErrorMessage 'Docker Desktop is installed but its engine is not running.'
        return $false
    }
    return $true
}

function Invoke-Compose {
    param([string[]]$ComposeArguments)

    $allArguments = @($script:ComposePrefix) + @('-f', (Join-Path $script:ProjectRoot 'docker-compose.dev.yml')) + $ComposeArguments
    & $script:ComposeExecutable @allArguments | Out-Host
    return [int]$LASTEXITCODE
}

function Get-ProfileArguments {
    $profiles = New-Object System.Collections.Generic.List[string]
    if ($Full) {
        if ($NoLangfuse) {
            Write-WarningMessage '-NoLangfuse is ignored when -Full is used because the full profile includes Langfuse.'
        }
        $profiles.Add('full')
    }
    else {
        if (-not $NoLangfuse) { $profiles.Add('langfuse') }
        if ($Minio) { $profiles.Add('minio') }
        if ($Qdrant) { $profiles.Add('qdrant') }
        if ($OpenSearch) { $profiles.Add('opensearch') }
        if ($Milvus) { $profiles.Add('milvus') }
        if ($Neo4j) { $profiles.Add('neo4j') }
        if ($Searxng) { $profiles.Add('searxng') }
        if ($Dex) { $profiles.Add('dex') }
    }
    if ($OdlHybrid) { $profiles.Add('odl-hybrid') }

    $arguments = @()
    foreach ($profile in $profiles) {
        $arguments += @('--profile', $profile)
    }
    return $arguments
}

function Start-Infrastructure {
    if (-not (Import-ProjectEnvironment -Required)) { return 1 }
    if (-not [string]::IsNullOrWhiteSpace($env:DEV_REMOTE_HOST)) {
        Write-WarningMessage "DEV_REMOTE_HOST=$($env:DEV_REMOTE_HOST); local Docker infrastructure was not started."
        return 0
    }
    if (-not (Test-DockerEngine)) { return 1 }

    $profileArguments = @(Get-ProfileArguments)
    Write-Info 'Starting development infrastructure'
    $exitCode = Invoke-Compose -ComposeArguments ($profileArguments + @('up', '-d'))
    if ($exitCode -ne 0) { return $exitCode }

    if ($OdlHybrid) {
        Write-Info 'Building and starting odl-hybrid'
        $exitCode = Invoke-Compose -ComposeArguments ($profileArguments + @('up', '-d', '--build', 'odl-hybrid'))
        if ($exitCode -ne 0) { return $exitCode }
    }

    Write-Success 'Development infrastructure is running'
    Write-Info 'Start the backend and frontend in separate terminals:'
    Write-Host '  .\scripts\dev.ps1 app'
    Write-Host '  .\scripts\dev.ps1 frontend'
    return 0
}

function Stop-Infrastructure {
    if (-not (Find-DockerCompose)) {
        Write-ErrorMessage 'Docker Compose was not found.'
        return 1
    }
    return (Invoke-Compose -ComposeArguments @('down'))
}

function Show-InfrastructureLogs {
    if (-not (Find-DockerCompose)) {
        Write-ErrorMessage 'Docker Compose was not found.'
        return 1
    }
    return (Invoke-Compose -ComposeArguments @('logs', '-f'))
}

function Show-InfrastructureStatus {
    if (-not (Find-DockerCompose)) {
        Write-ErrorMessage 'Docker Compose was not found.'
        return 1
    }
    return (Invoke-Compose -ComposeArguments @('ps'))
}

function Set-LocalDevelopmentAddresses {
    if (-not [string]::IsNullOrWhiteSpace($env:DEV_REMOTE_HOST)) {
        $remoteHost = $env:DEV_REMOTE_HOST
        Write-Info "Using remote infrastructure at $remoteHost"
        if ([string]::IsNullOrWhiteSpace($env:DB_HOST)) { $env:DB_HOST = $remoteHost }
        if ([string]::IsNullOrWhiteSpace($env:REDIS_ADDR)) { $env:REDIS_ADDR = "${remoteHost}:6379" }
        if ([string]::IsNullOrWhiteSpace($env:DOCREADER_ADDR)) { $env:DOCREADER_ADDR = "${remoteHost}:50051" }
        if ([string]::IsNullOrWhiteSpace($env:MINIO_ENDPOINT)) { $env:MINIO_ENDPOINT = "${remoteHost}:9000" }
        if ([string]::IsNullOrWhiteSpace($env:MILVUS_ADDRESS)) { $env:MILVUS_ADDRESS = "${remoteHost}:19530" }
        if ([string]::IsNullOrWhiteSpace($env:NEO4J_URI)) { $env:NEO4J_URI = "bolt://${remoteHost}:7687" }
        if ([string]::IsNullOrWhiteSpace($env:QDRANT_HOST)) { $env:QDRANT_HOST = $remoteHost }
        if ([string]::IsNullOrWhiteSpace($env:LANGFUSE_HOST) -or $env:LANGFUSE_HOST -eq 'http://langfuse-web:3000') {
            $env:LANGFUSE_HOST = "http://${remoteHost}:3000"
        }
    }
    else {
        $env:DB_HOST = '127.0.0.1'
        $env:REDIS_ADDR = '127.0.0.1:6379'
        $env:DOCREADER_ADDR = '127.0.0.1:50051'
        $env:MINIO_ENDPOINT = '127.0.0.1:9000'
        $env:MILVUS_ADDRESS = '127.0.0.1:19530'
        $env:NEO4J_URI = 'bolt://127.0.0.1:7687'
        $env:QDRANT_HOST = '127.0.0.1'
    }

    if ([string]::IsNullOrWhiteSpace($env:DOCREADER_TRANSPORT)) {
        $env:DOCREADER_TRANSPORT = 'grpc'
    }
    if ([string]::IsNullOrWhiteSpace($env:LOCAL_STORAGE_BASE_DIR) -or $env:LOCAL_STORAGE_BASE_DIR -eq '/data/files') {
        $env:LOCAL_STORAGE_BASE_DIR = Join-Path $script:ProjectRoot '.local-data\files'
    }
    New-Item -ItemType Directory -Path $env:LOCAL_STORAGE_BASE_DIR -Force | Out-Null
}

function Get-DevelopmentLdflags {
    $version = $env:VERSION
    if ([string]::IsNullOrWhiteSpace($version)) {
        $result = Invoke-CapturedCommand -FilePath 'git.exe' -ArgumentList @('describe', '--tags', '--abbrev=0')
        $version = if ($result.ExitCode -eq 0 -and -not [string]::IsNullOrWhiteSpace($result.Output)) { $result.Output.Trim() } else { 'unknown' }
    }

    $commit = $env:COMMIT_ID
    if ([string]::IsNullOrWhiteSpace($commit)) {
        $result = Invoke-CapturedCommand -FilePath 'git.exe' -ArgumentList @('rev-parse', '--short', 'HEAD')
        $commit = if ($result.ExitCode -eq 0) { $result.Output.Trim() } else { 'unknown' }
    }

    $goResult = Invoke-CapturedCommand -FilePath 'go.exe' -ArgumentList @('version')
    $goVersionMatch = [regex]::Match($goResult.Output, 'go version (go[^\s]+)')
    $goVersion = if ($goVersionMatch.Success) { $goVersionMatch.Groups[1].Value } else { 'unknown' }
    $buildTime = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $handlerPackage = 'github.com/Tencent/WeKnora/internal/handler'

    return @(
        "-X ${handlerPackage}.Version=$version",
        "-X ${handlerPackage}.Edition=standard",
        "-X ${handlerPackage}.CommitID=$commit",
        "-X ${handlerPackage}.BuildTime=$buildTime",
        "-X ${handlerPackage}.GoVersion=$goVersion",
        '-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn'
    ) -join ' '
}

function Start-Backend {
    Set-Location $script:ProjectRoot
    if (-not (Import-ProjectEnvironment -Required)) { return 1 }
    if (-not (Test-PlatformAndGo)) { return 1 }
    if (-not (Test-WindowsToolchain)) { return 1 }
    if ([string]::IsNullOrWhiteSpace($env:DB_DRIVER)) {
        Write-ErrorMessage 'DB_DRIVER is not set in .env.'
        return 1
    }

    Set-LocalDevelopmentAddresses
    Set-WindowsCgoEnvironment
    Write-Info "Database: $($env:DB_HOST):$($env:DB_PORT)"
    Write-Info "Local storage: $($env:LOCAL_STORAGE_BASE_DIR)"

    $airCommand = Get-Command air.exe -ErrorAction SilentlyContinue
    if (-not $NoAir -and $null -ne $airCommand) {
        Write-Success 'Air detected; starting Windows hot reload mode'
        & $airCommand.Source '-c' (Join-Path $script:ProjectRoot 'air.windows.toml') | Out-Host
        return [int]$LASTEXITCODE
    }

    if (-not $NoAir) {
        Write-WarningMessage 'Air is not installed; starting without hot reload.'
        Write-Info 'Optional install: go install github.com/air-verse/air@latest'
    }

    $goArguments = @('run')
    if (-not [string]::IsNullOrWhiteSpace($env:GO_BUILD_TAGS)) {
        $goArguments += @('-tags', $env:GO_BUILD_TAGS)
    }
    $goArguments += @('-ldflags', (Get-DevelopmentLdflags), './cmd/server')
    & 'go.exe' @goArguments | Out-Host
    return [int]$LASTEXITCODE
}

function Start-Frontend {
    if (-not (Import-ProjectEnvironment)) { return 1 }
    $npmCommand = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if ($null -eq $npmCommand) {
        Write-ErrorMessage 'npm.cmd was not found. Install Node.js 22 or newer.'
        return 1
    }

    $frontendDirectory = Join-Path $script:ProjectRoot 'frontend'
    Set-Location $frontendDirectory
    if (-not (Test-Path -LiteralPath (Join-Path $frontendDirectory 'node_modules') -PathType Container)) {
        Write-WarningMessage 'frontend/node_modules is missing; running npm install.'
        & $npmCommand.Source 'install' | Out-Host
        if ($LASTEXITCODE -ne 0) { return [int]$LASTEXITCODE }
    }

    $proxyTarget = $env:VITE_DEV_PROXY_TARGET
    if ([string]::IsNullOrWhiteSpace($proxyTarget)) { $proxyTarget = $env:FRONTEND_BACKEND_URL }
    if ([string]::IsNullOrWhiteSpace($proxyTarget)) { $proxyTarget = 'http://localhost:8080' }
    Write-Info 'Starting Vite at http://localhost:5173'
    Write-Info "API proxy target: $proxyTarget"
    & $npmCommand.Source 'run' 'dev' | Out-Host
    return [int]$LASTEXITCODE
}

function Show-OptionalReadiness {
    $npmCommand = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if ($null -eq $npmCommand) {
        Write-WarningMessage 'Node.js/npm is missing; the frontend command will not run.'
    }
    else {
        $npmResult = Invoke-CapturedCommand -FilePath $npmCommand.Source -ArgumentList @('--version')
        Write-Success "npm $($npmResult.Output.Trim())"
    }

    if (Find-DockerCompose) {
        $dockerCommand = Get-Command docker.exe -ErrorAction SilentlyContinue
        if ($null -eq $dockerCommand) {
            Write-WarningMessage 'Docker Compose is installed, but docker.exe is unavailable; run start to verify the engine.'
            return
        }
        $dockerResult = Invoke-CapturedCommand -FilePath $dockerCommand.Source -ArgumentList @('info')
        if ($dockerResult.ExitCode -eq 0) {
            Write-Success 'Docker Desktop engine is running'
        }
        else {
            Write-WarningMessage 'Docker Compose is installed, but Docker Desktop is not running.'
        }
    }
    else {
        Write-WarningMessage 'Docker Compose is missing; infrastructure commands will not run.'
    }
}

function Invoke-Preflight {
    $ok = $true
    Set-Location $script:ProjectRoot
    if (-not (Import-ProjectEnvironment -Required)) { $ok = $false }
    if (-not (Test-PlatformAndGo)) { $ok = $false }
    if (-not (Test-WindowsToolchain)) { $ok = $false }
    if ($ok) {
        Set-WindowsCgoEnvironment
        Write-Success 'Windows backend preflight passed'
    }
    Show-OptionalReadiness
    if ($ok) { return 0 }
    return 1
}

function Show-Help {
    Write-Host @'
WeKnora native Windows development helper

Usage:
  .\scripts\dev.ps1 check [-GccBin <dir>] [-SqliteInclude <dir>]
  .\scripts\dev.ps1 start [-Minio] [-Qdrant] [-OpenSearch] [-Milvus]
                          [-Neo4j] [-Searxng] [-Dex] [-OdlHybrid]
                          [-Full] [-NoLangfuse]
  .\scripts\dev.ps1 app [-GccBin <dir>] [-SqliteInclude <dir>] [-NoAir]
  .\scripts\dev.ps1 frontend
  .\scripts\dev.ps1 status | logs | stop | restart

Persistent toolchain settings:
  WEKNORA_GCC_BIN        Directory containing the DuckDB-compatible gcc.exe/g++.exe
  WEKNORA_SQLITE_INCLUDE Directory containing sqlite3.h

Run `check` before `app`. See website-docs/06-development/01-dev-guide.md
for the validated GCC 14.2 UCRT setup and the GCC 16 incompatibility note.
'@
}

try {
    $exitCode = switch ($Command) {
        'check' { Invoke-Preflight }
        'start' { Start-Infrastructure }
        'stop' { Stop-Infrastructure }
        'restart' {
            $stopCode = Stop-Infrastructure
            if ($stopCode -ne 0) { $stopCode } else { Start-Infrastructure }
        }
        'logs' { Show-InfrastructureLogs }
        'status' { Show-InfrastructureStatus }
        'app' { Start-Backend }
        'frontend' { Start-Frontend }
        default { Show-Help; 0 }
    }
}
catch {
    Write-ErrorMessage $_.Exception.Message
    $exitCode = 1
}
finally {
    Set-Location $script:ProjectRoot
}

exit [int]$exitCode
