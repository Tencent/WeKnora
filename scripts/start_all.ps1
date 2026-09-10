#Requires -Version 5.1

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("All", "Ollama", "Docker", "Stop", "Check", "Restart", "List", "Pull", "Help", "Version")]
    [string]$Action = "All",

    [Parameter(Position = 1)]
    [string]$Service,

    [switch]$NoPull,
    [switch]$NoLogs
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$script:Version = "1.0.0"
$script:ScriptDirectory = Split-Path -Parent $PSCommandPath
$script:ProjectRoot = Split-Path -Parent $script:ScriptDirectory
$script:EnvPath = Join-Path $script:ProjectRoot ".env"
$script:ComposeExecutable = $null
$script:ComposePrefix = @()
$script:DockerExecutable = $null

function Write-Status {
    param(
        [ValidateSet("INFO", "WARN", "ERROR", "OK")]
        [string]$Level,
        [string]$Message
    )

    $color = switch ($Level) {
        "INFO" { "Cyan" }
        "WARN" { "Yellow" }
        "ERROR" { "Red" }
        "OK" { "Green" }
    }
    Write-Host "[$Level] $Message" -ForegroundColor $color
}

function Show-Usage {
    Write-Host @'
WeKnora Windows launcher

Usage:
  .\scripts\start_all.cmd [action] [service] [-NoPull] [-NoLogs]
  .\scripts\start_all.ps1 [action] [service] [-NoPull] [-NoLogs]

Actions:
  All       Start Ollama when available, then start Docker services (default)
  Ollama    Start only the local Ollama service
  Docker    Start only Docker services
  Stop      Stop local Ollama and Docker services
  Check     Diagnose Docker, Compose, .env, platform, and Ollama
  Restart   Rebuild and restart one Compose service (for example: Restart app)
  List      List Compose containers
  Pull      Pull core and sandbox images
  Help      Show this help
  Version   Show launcher version

Options:
  -NoPull   Reuse local images when starting Docker services
  -NoLogs   Return after startup instead of following service logs

Examples:
  .\scripts\start_all.cmd
  .\scripts\start_all.cmd Docker -NoLogs
  .\scripts\start_all.cmd Check
  .\scripts\start_all.cmd Restart app
  .\scripts\start_all.cmd Stop
'@
}

function Read-DotEnv {
    $values = @{}
    if (-not (Test-Path -LiteralPath $script:EnvPath -PathType Leaf)) {
        return $values
    }

    foreach ($line in Get-Content -LiteralPath $script:EnvPath -Encoding UTF8) {
        if ($line -notmatch '^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$') {
            continue
        }

        $name = $Matches[1]
        $value = $Matches[2].Trim()
        if ($value.Length -ge 2) {
            $first = $value.Substring(0, 1)
            $last = $value.Substring($value.Length - 1, 1)
            if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        $values[$name] = $value
    }

    return $values
}

function Get-ConfiguredValue {
    param(
        [string]$Name,
        [string]$Default = ""
    )

    $processValue = [Environment]::GetEnvironmentVariable($Name)
    if (-not [string]::IsNullOrWhiteSpace($processValue)) {
        return $processValue
    }

    $values = Read-DotEnv
    if ($values.ContainsKey($Name) -and -not [string]::IsNullOrWhiteSpace([string]$values[$Name])) {
        return [string]$values[$Name]
    }
    return $Default
}

function Test-TrueValue {
    param([string]$Value)
    return @("1", "true", "yes", "on") -contains $Value.Trim().ToLowerInvariant()
}

function Initialize-EnvironmentFile {
    if (Test-Path -LiteralPath $script:EnvPath -PathType Leaf) {
        Write-Status "INFO" ".env already exists."
        return
    }

    $examplePath = Join-Path $script:ProjectRoot ".env.example"
    if (-not (Test-Path -LiteralPath $examplePath -PathType Leaf)) {
        throw ".env is missing and .env.example was not found."
    }

    Copy-Item -LiteralPath $examplePath -Destination $script:EnvPath
    Write-Status "WARN" "Created .env from .env.example. Review passwords and secrets before production use."
}

function Invoke-QuietNative {
    param(
        [string]$FilePath,
        [string[]]$Arguments
    )

    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "SilentlyContinue"
    try {
        & $FilePath @Arguments *> $null
        return $LASTEXITCODE
    }
    catch {
        return 1
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
}

function Set-ComposeCommand {
    $dockerCommand = Get-Command "docker" -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $dockerCommand) {
        throw "Docker CLI was not found. Install Docker Desktop and reopen the terminal."
    }

    $script:DockerExecutable = $dockerCommand.Source
    if ((Invoke-QuietNative -FilePath $script:DockerExecutable -Arguments @("compose", "version")) -eq 0) {
        $script:ComposeExecutable = $script:DockerExecutable
        $script:ComposePrefix = @("compose")
        return
    }

    $legacyCommand = Get-Command "docker-compose" -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $legacyCommand) {
        if ((Invoke-QuietNative -FilePath $legacyCommand.Source -Arguments @("version")) -eq 0) {
            $script:ComposeExecutable = $legacyCommand.Source
            $script:ComposePrefix = @()
            return
        }
    }

    throw "Docker Compose was not found. Install the Docker Compose v2 plugin."
}

function Invoke-Compose {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,
        [switch]$AllowFailure,
        [switch]$Capture
    )

    if ([string]::IsNullOrWhiteSpace([string]$script:ComposeExecutable)) {
        Set-ComposeCommand
    }

    $nativeArguments = @($script:ComposePrefix) + @($Arguments)
    Push-Location $script:ProjectRoot
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        if ($Capture) {
            $output = @(& $script:ComposeExecutable @nativeArguments 2>&1)
        }
        else {
            & $script:ComposeExecutable @nativeArguments
            $output = @()
        }
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousPreference
        Pop-Location
    }

    if ($exitCode -ne 0 -and -not $AllowFailure) {
        throw "Docker Compose failed with exit code ${exitCode}: $($Arguments -join ' ')"
    }

    if ($Capture) {
        return [PSCustomObject]@{
            ExitCode = $exitCode
            Output = $output
        }
    }
}

function Test-DockerDaemon {
    return (Invoke-QuietNative -FilePath $script:DockerExecutable -Arguments @("info")) -eq 0
}

function Initialize-Docker {
    Set-ComposeCommand
    if (-not (Test-DockerDaemon)) {
        throw "Docker Desktop is not running. Start Docker Desktop and wait until the engine is ready."
    }
    Write-Status "OK" "Docker Desktop and Docker Compose are ready."
}

function Set-DockerPlatform {
    $architecture = $env:PROCESSOR_ARCHITEW6432
    if ([string]::IsNullOrWhiteSpace($architecture)) {
        $architecture = $env:PROCESSOR_ARCHITECTURE
    }

    if ($architecture -match '^(ARM64|AARCH64)$') {
        $platform = "linux/arm64"
    }
    elseif ($architecture -match '^(AMD64|x86_64|x64)$') {
        $platform = "linux/amd64"
    }
    else {
        $platform = "linux/amd64"
        Write-Status "WARN" "Unknown Windows architecture '$architecture'; using $platform."
    }

    $env:PLATFORM = $platform
    Write-Status "INFO" "Docker platform: $platform"
    return $platform
}

function Get-OllamaSettings {
    $baseUrl = Get-ConfiguredValue -Name "OLLAMA_BASE_URL" -Default "http://host.docker.internal:11434"
    try {
        $baseUri = [Uri]$baseUrl
    }
    catch {
        throw "OLLAMA_BASE_URL is not a valid absolute URL: $baseUrl"
    }

    if (-not $baseUri.IsAbsoluteUri -or @("http", "https") -notcontains $baseUri.Scheme) {
        throw "OLLAMA_BASE_URL must be an absolute HTTP(S) URL: $baseUrl"
    }

    $localHosts = @("localhost", "127.0.0.1", "::1", "host.docker.internal")
    $isLocal = $localHosts -contains $baseUri.Host.ToLowerInvariant()
    $probeUrl = $baseUrl.TrimEnd('/') + "/api/tags"

    if ($baseUri.Host -eq "host.docker.internal") {
        $probeBuilder = New-Object System.UriBuilder($probeUrl)
        $probeBuilder.Host = "127.0.0.1"
        $probeUrl = $probeBuilder.Uri.AbsoluteUri
    }

    return [PSCustomObject]@{
        BaseUrl = $baseUrl
        ProbeUrl = $probeUrl
        IsLocal = $isLocal
    }
}

function Test-OllamaEndpoint {
    param([string]$Url)
    try {
        $null = Invoke-RestMethod -Uri $Url -Method Get -TimeoutSec 3 -ErrorAction Stop
        return $true
    }
    catch {
        return $false
    }
}

function Start-OllamaService {
    $settings = Get-OllamaSettings
    Write-Status "INFO" "Checking Ollama at $($settings.BaseUrl)"

    if (Test-OllamaEndpoint -Url $settings.ProbeUrl) {
        Write-Status "OK" "Ollama is reachable."
        return $true
    }

    if (-not $settings.IsLocal) {
        Write-Status "WARN" "The configured remote Ollama endpoint is not reachable."
        return $false
    }

    $ollamaCommand = Get-Command "ollama" -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $ollamaCommand) {
        Write-Status "WARN" "Ollama is not installed. Install it from https://ollama.com/download/windows or configure a remote endpoint."
        return $false
    }

    Write-Status "INFO" "Starting Ollama in the background..."
    Start-Process -FilePath $ollamaCommand.Source -ArgumentList @("serve") -WindowStyle Hidden | Out-Null

    for ($attempt = 1; $attempt -le 30; $attempt++) {
        Start-Sleep -Seconds 1
        if (Test-OllamaEndpoint -Url $settings.ProbeUrl) {
            Write-Status "OK" "Ollama started successfully."
            return $true
        }
    }

    Write-Status "WARN" "Ollama did not become ready within 30 seconds."
    return $false
}

function Stop-OllamaService {
    $settings = Get-OllamaSettings
    if (-not $settings.IsLocal) {
        Write-Status "INFO" "Remote Ollama is configured; nothing to stop locally."
        return $true
    }

    $processes = @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -like "ollama*" })
    if ($processes.Count -eq 0) {
        Write-Status "INFO" "No local Ollama process is running."
        return $true
    }

    $failed = $false
    foreach ($process in $processes) {
        try {
            Stop-Process -Id $process.Id -ErrorAction Stop
        }
        catch {
            $failed = $true
            Write-Status "WARN" "Could not stop Ollama process $($process.Id): $($_.Exception.Message)"
        }
    }

    if ($failed) {
        return $false
    }
    Write-Status "OK" "Local Ollama processes stopped."
    return $true
}

function Test-OllamaOptional {
    $value = Get-ConfiguredValue -Name "OLLAMA_OPTIONAL" -Default "true"
    return Test-TrueValue -Value $value
}

function Initialize-SandboxImage {
    $version = Get-ConfiguredValue -Name "WEKNORA_VERSION" -Default "latest"
    $image = "wechatopenai/weknora-sandbox:$version"
    if ((Invoke-QuietNative -FilePath $script:DockerExecutable -Arguments @("image", "inspect", $image)) -eq 0) {
        Write-Status "OK" "Sandbox image is ready: $image"
        return
    }

    Write-Status "INFO" "Pulling optional Agent Skills sandbox image: $image"
    try {
        Invoke-Compose -Arguments @("--profile", "sandbox", "pull", "sandbox")
    }
    catch {
        Write-Status "WARN" "Sandbox image pull failed; Agent Skills may be unavailable: $($_.Exception.Message)"
    }
}

function Start-DockerServices {
    Initialize-Docker
    Initialize-EnvironmentFile
    $null = Set-DockerPlatform

    if ($NoPull) {
        Write-Status "INFO" "Starting core services with locally available images..."
        Invoke-Compose -Arguments @("up", "-d")
    }
    else {
        Write-Status "INFO" "Pulling current images and starting core services..."
        Invoke-Compose -Arguments @("up", "--pull", "always", "-d")
    }

    Write-Status "OK" "Docker services started."
    Invoke-Compose -Arguments @("ps")
    Initialize-SandboxImage
}

function Stop-DockerServices {
    Initialize-Docker
    Write-Status "INFO" "Stopping Compose services without deleting containers or volumes..."
    Invoke-Compose -Arguments @("stop")
    Write-Status "OK" "Docker services stopped."
}

function Show-DockerServices {
    Initialize-Docker
    Invoke-Compose -Arguments @("ps")
}

function Sync-DockerImages {
    Initialize-Docker
    Initialize-EnvironmentFile
    $null = Set-DockerPlatform
    Invoke-Compose -Arguments @("pull")
    Initialize-SandboxImage
    Write-Status "OK" "Docker images pulled."
}

function Restart-DockerService {
    param([string]$Name)

    if ([string]::IsNullOrWhiteSpace($Name)) {
        throw "Restart requires a Compose service name, for example: Restart app"
    }

    Initialize-Docker
    Initialize-EnvironmentFile
    $null = Set-DockerPlatform

    $result = Invoke-Compose -Arguments @("config", "--services") -Capture
    $services = @($result.Output | ForEach-Object { ([string]$_).Trim() } | Where-Object { $_ })
    if ($services -notcontains $Name) {
        throw "Unknown Compose service '$Name'. Available services: $($services -join ', ')"
    }

    Write-Status "INFO" "Rebuilding Compose service '$Name'..."
    Invoke-Compose -Arguments @("build", $Name)
    Invoke-Compose -Arguments @("up", "-d", "--no-deps", $Name)
    Write-Status "OK" "Compose service '$Name' restarted."
}

function Test-Environment {
    Write-Status "INFO" "WeKnora Windows environment check"
    $ready = $true
    $dockerReady = $false

    try {
        Set-ComposeCommand
        Write-Status "OK" "Docker CLI and Docker Compose were found."
        $dockerReady = Test-DockerDaemon
        if ($dockerReady) {
            Write-Status "OK" "Docker Desktop engine is running."
        }
        else {
            Write-Status "ERROR" "Docker Desktop engine is not running."
            $ready = $false
        }
    }
    catch {
        Write-Status "ERROR" $_.Exception.Message
        $ready = $false
    }

    $null = Set-DockerPlatform
    if (Test-Path -LiteralPath $script:EnvPath -PathType Leaf) {
        Write-Status "OK" ".env exists."
        $values = Read-DotEnv
        foreach ($requiredName in @("DB_DRIVER", "STORAGE_TYPE")) {
            if (-not $values.ContainsKey($requiredName) -or [string]::IsNullOrWhiteSpace([string]$values[$requiredName])) {
                Write-Status "WARN" ".env does not set $requiredName."
            }
        }

        if ($null -ne $script:ComposeExecutable) {
            try {
                Invoke-Compose -Arguments @("config", "--quiet")
                Write-Status "OK" "docker-compose.yml and .env are valid."
            }
            catch {
                Write-Status "ERROR" $_.Exception.Message
                $ready = $false
            }
        }
    }
    else {
        Write-Status "WARN" ".env is missing; the start action will create it from .env.example."
    }

    try {
        $settings = Get-OllamaSettings
        if (Test-OllamaEndpoint -Url $settings.ProbeUrl) {
            Write-Status "OK" "Ollama is reachable at $($settings.BaseUrl)."
        }
        elseif (Test-OllamaOptional) {
            Write-Status "WARN" "Ollama is not reachable, but OLLAMA_OPTIONAL is enabled."
        }
        else {
            Write-Status "ERROR" "Ollama is not reachable and OLLAMA_OPTIONAL is disabled."
            $ready = $false
        }
    }
    catch {
        Write-Status "ERROR" $_.Exception.Message
        $ready = $false
    }

    if ($dockerReady) {
        $version = Get-ConfiguredValue -Name "WEKNORA_VERSION" -Default "latest"
        $sandboxImage = "wechatopenai/weknora-sandbox:$version"
        if ((Invoke-QuietNative -FilePath $script:DockerExecutable -Arguments @("image", "inspect", $sandboxImage)) -eq 0) {
            Write-Status "OK" "Sandbox image is ready: $sandboxImage"
        }
        else {
            Write-Status "WARN" "Sandbox image is missing: $sandboxImage"
        }
    }

    return $ready
}

function Show-ServiceEndpoints {
    $frontendPort = Get-ConfiguredValue -Name "FRONTEND_PORT" -Default "80"
    $appPort = Get-ConfiguredValue -Name "APP_PORT" -Default "8080"
    Write-Status "OK" "Frontend: http://localhost:$frontendPort"
    Write-Status "OK" "API: http://localhost:$appPort"
}

function Show-ServiceLogs {
    Write-Status "INFO" "Following service logs. Press Ctrl+C to leave logs; containers will keep running."
    Invoke-Compose -Arguments @("logs", "--since", "10s", "--follow", "app", "docreader", "postgres") -AllowFailure
}

try {
    switch ($Action.ToLowerInvariant()) {
        "help" {
            Show-Usage
        }
        "version" {
            Write-Host "WeKnora Windows launcher v$($script:Version)"
        }
        "check" {
            if (-not (Test-Environment)) {
                exit 1
            }
        }
        "list" {
            Show-DockerServices
        }
        "pull" {
            Sync-DockerImages
        }
        "restart" {
            Restart-DockerService -Name $Service
        }
        "stop" {
            $ollamaStopped = Stop-OllamaService
            Stop-DockerServices
            if (-not $ollamaStopped) {
                throw "Docker services stopped, but one or more Ollama processes could not be stopped."
            }
        }
        "ollama" {
            Initialize-EnvironmentFile
            if (-not (Start-OllamaService)) {
                throw "Ollama could not be started."
            }
        }
        "docker" {
            Start-DockerServices
            Show-ServiceEndpoints
            if (-not $NoLogs) {
                Show-ServiceLogs
            }
        }
        "all" {
            Initialize-EnvironmentFile
            $ollamaReady = Start-OllamaService
            if (-not $ollamaReady -and -not (Test-OllamaOptional)) {
                throw "Ollama is required but could not be started."
            }
            if (-not $ollamaReady) {
                Write-Status "WARN" "Continuing because OLLAMA_OPTIONAL is enabled."
            }

            Start-DockerServices
            Show-ServiceEndpoints
            if (-not $NoLogs) {
                Show-ServiceLogs
            }
        }
    }
    exit 0
}
catch {
    Write-Status "ERROR" $_.Exception.Message
    exit 1
}
