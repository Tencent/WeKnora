#!/usr/bin/env bash
set -euo pipefail

#
# 本地构建 + 打包 WeKnora macOS 桌面应用 (.app)
#
# 用法:
#   ./scripts/package-mac-app.sh
#   SKIP_FRONTEND=1 ./scripts/package-mac-app.sh  # 跳过前端构建
#

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

APP_NAME="WeKnora Lite"
APP_BUNDLE="${APP_NAME}.app"
DIST_DIR="dist/${APP_BUNDLE}"

echo "=== WeKnora Mac App Packager ==="
echo "  Output: dist/${APP_BUNDLE}"
echo ""

# ── Step 1: Build frontend (if not skipped) ──
if [ "${SKIP_FRONTEND:-}" != "1" ]; then
    if [ -f frontend/package.json ]; then
        echo ">> Building frontend..."
        (cd frontend && npm ci --prefer-offline && npm run build)
    else
        echo ">> No frontend/package.json found, skipping frontend build"
    fi
fi

# ── Step 2: Build with Wails ──
echo ">> Building Wails Desktop App..."

# 如果没有 wails 命令行工具，提醒安装
if ! command -v wails >/dev/null 2>&1; then
    echo "Wails CLI not found. Please install it first:"
    echo "go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
fi

# 使用 Wails 打包 (需要先处理依赖代理问题)
export GONOSUMDB="git.sr.ht/*"
export GOPROXY="https://goproxy.cn,direct"
export CGO_CFLAGS="-Wno-deprecated-declarations"
export CGO_LDFLAGS="-Wl,-no_warn_duplicate_libraries"
export EDITION=lite

# 我们实际上只用 Wails 构建外壳，前端仍然由后台服务提供
wails build -clean -buildmode default -tags "sqlite_fts5" -o "${APP_NAME}"

# ── Step 3: Copy generated .app to dist ──
echo ">> Assembling package..."
mkdir -p dist
rm -rf "${DIST_DIR}"
cp -R "build/bin/${APP_BUNDLE}" "dist/"

# 将配置文件和初始数据库迁移脚本塞进 .app 内部资源里
RESOURCES_DIR="${DIST_DIR}/Contents/Resources"
mkdir -p "${RESOURCES_DIR}/config"
mkdir -p "${RESOURCES_DIR}/migrations/sqlite"

if [ -f .env.lite.example ]; then
    cp .env.lite.example "${RESOURCES_DIR}/.env"
fi
if [ -d migrations/sqlite ]; then
    cp -r migrations/sqlite/* "${RESOURCES_DIR}/migrations/sqlite/"
fi

# 注意：Wails build 生成的二进制文件工作目录默认是 app 的 Contents/MacOS 目录
# 后续可能需要调整代码中对配置文件的路径读取逻辑。

echo ""
echo "=== Done ==="
echo "  App generated at: dist/${APP_BUNDLE}"
echo "  You can double click it to run!"
