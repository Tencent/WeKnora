[CmdletBinding()]
param(
    [string]$GccBin = $env:WEKNORA_GCC_BIN,
    [string]$SqliteInclude = $env:WEKNORA_SQLITE_INCLUDE,
    [string]$Output = 'WeKnora-lite.exe',
    [switch]$SkipFrontend
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$rootDir = Split-Path -Parent $PSScriptRoot
Set-Location $rootDir

function Resolve-GccExecutable {
    param([string]$Hint)

    $candidates = New-Object System.Collections.Generic.List[string]
    if (-not [string]::IsNullOrWhiteSpace($Hint)) {
        if ([IO.Path]::GetFileName($Hint) -ieq 'gcc.exe') {
            $candidates.Add($Hint)
        }
        else {
            $candidates.Add((Join-Path $Hint 'gcc.exe'))
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
        if ([IO.Path]::GetFileName($Hint) -ieq 'sqlite3.h') {
            $candidates.Add((Split-Path -Parent $Hint))
        }
        else {
            $candidates.Add($Hint)
        }
    }
    if (-not [string]::IsNullOrWhiteSpace($env:MSYSTEM_PREFIX)) {
        $candidates.Add((Join-Path $env:MSYSTEM_PREFIX 'include'))
    }
    $candidates.Add((Join-Path (Split-Path -Parent $CompilerDirectory) 'include'))
    $candidates.Add('C:\msys64\ucrt64\include')

    foreach ($candidate in $candidates) {
        $header = Join-Path $candidate 'sqlite3.h'
        if (Test-Path -LiteralPath $header -PathType Leaf) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    return $null
}

$goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
if ($null -eq $goCommand) {
    throw '未找到 go.exe，请先安装 Go 并加入 PATH。'
}

$platform = @(& $goCommand.Source env GOOS GOARCH)
if ($LASTEXITCODE -ne 0 -or $platform.Count -lt 2) {
    throw '无法读取 Go 目标平台。'
}
if ($platform[0].Trim() -ne 'windows' -or $platform[1].Trim() -ne 'amd64') {
    throw "当前脚本仅支持 windows/amd64，检测到 $($platform[0])/$($platform[1])。"
}

$gcc = Resolve-GccExecutable -Hint $GccBin
if ([string]::IsNullOrWhiteSpace($gcc)) {
    throw '未找到 gcc.exe。请设置 WEKNORA_GCC_BIN，指向兼容 DuckDB 的 UCRT GCC 目录。'
}

$compilerDirectory = Split-Path -Parent $gcc
$gxx = Join-Path $compilerDirectory 'g++.exe'
if (-not (Test-Path -LiteralPath $gxx -PathType Leaf)) {
    throw "未找到 $gxx。DuckDB 静态库需要 C++ 链接器。"
}

$target = (& $gcc -dumpmachine 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $target -ne 'x86_64-w64-mingw32') {
    throw "GCC 目标必须是 x86_64-w64-mingw32，当前为 '$target'。"
}

$versionOutput = (& $gcc -dumpfullversion -dumpversion 2>&1 | Out-String).Trim()
$versionMatch = [regex]::Match($versionOutput, '[0-9]+\.[0-9]+(?:\.[0-9]+)?')
if (-not $versionMatch.Success) {
    throw "无法识别 GCC 版本：$versionOutput"
}
$gccVersion = [version]$versionMatch.Value
if ($gccVersion.Major -ge 16) {
    throw "GCC $gccVersion 使用 native TLS，无法链接当前 DuckDB GCC 14.2 预编译静态库。请使用文档中的 GCC 14.2 UCRT 工具链。"
}
if ($gccVersion.Major -ne 14 -or $gccVersion.Minor -ne 2) {
    Write-Warning "仅验证过 GCC 14.2 UCRT；当前版本为 $gccVersion。"
}

$compilerDetails = (& $gcc -v 2>&1 | Out-String)
if ($compilerDetails -notmatch '(?i)ucrt') {
    throw '检测到的 GCC 不是 UCRT 工具链。DuckDB Windows 预编译静态库不能与 MSVCRT 工具链混用。'
}

$sqliteDirectory = Resolve-SqliteIncludeDirectory -Hint $SqliteInclude -CompilerDirectory $compilerDirectory
if ([string]::IsNullOrWhiteSpace($sqliteDirectory)) {
    throw '未找到 sqlite3.h。请安装 mingw-w64-ucrt-x86_64-sqlite3，并设置 WEKNORA_SQLITE_INCLUDE。'
}

$env:Path = "$compilerDirectory;$env:Path"
$env:CGO_ENABLED = '1'
$env:CC = $gcc
$env:CXX = $gxx
$includePath = $sqliteDirectory.Replace('\', '/')
$cgoFlags = @()
if (-not [string]::IsNullOrWhiteSpace($env:CGO_CFLAGS)) {
    $cgoFlags += $env:CGO_CFLAGS.Trim()
}
$cgoFlags += '-Wno-deprecated-declarations'
$cgoFlags += "-idirafter `"$includePath`""
$env:CGO_CFLAGS = $cgoFlags -join ' '
$env:EDITION = 'lite'

if (-not $SkipFrontend) {
    $npmCommand = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if ($null -eq $npmCommand) {
        throw '未找到 npm.cmd。请安装 Node.js，或在已有 web/index.html 时使用 -SkipFrontend。'
    }

    Push-Location 'frontend'
    try {
        & $npmCommand.Source ci --prefer-offline
        if ($LASTEXITCODE -ne 0) {
            throw "npm ci 失败，退出码 $LASTEXITCODE。"
        }
        & $npmCommand.Source run build
        if ($LASTEXITCODE -ne 0) {
            throw "前端构建失败，退出码 $LASTEXITCODE。"
        }
    }
    finally {
        Pop-Location
    }

    if (Test-Path -LiteralPath 'web') {
        Remove-Item -LiteralPath 'web' -Recurse -Force
    }
    Copy-Item -LiteralPath 'frontend\dist' -Destination 'web' -Recurse
}
elseif (-not (Test-Path -LiteralPath 'web\index.html' -PathType Leaf)) {
    Write-Warning '未找到 web/index.html，生成的服务端不会提供前端页面。'
}

$version = 'unknown'
if (Test-Path -LiteralPath 'VERSION' -PathType Leaf) {
    $version = (Get-Content -LiteralPath 'VERSION' -Raw).Trim()
}
$commitId = 'unknown'
$gitCommand = Get-Command git.exe -ErrorAction SilentlyContinue
if ($null -ne $gitCommand) {
    $candidateCommit = (& $gitCommand.Source rev-parse --short HEAD 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($candidateCommit)) {
        $commitId = $candidateCommit
    }
}

$ldflags = "-w -s -X github.com/Tencent/WeKnora/internal/handler.Version=$version -X github.com/Tencent/WeKnora/internal/handler.Edition=lite -X github.com/Tencent/WeKnora/internal/handler.CommitID=$commitId -X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn"

Write-Host "使用 GCC $gccVersion (UCRT)"
Write-Host "使用 SQLite 头文件 $(Join-Path $sqliteDirectory 'sqlite3.h')"
Write-Host "构建 $Output"

& $goCommand.Source build -tags sqlite_fts5 -ldflags $ldflags -o $Output ./cmd/server
if ($LASTEXITCODE -ne 0) {
    throw "Go 构建失败，退出码 $LASTEXITCODE。"
}

Write-Host "构建完成：$Output"
