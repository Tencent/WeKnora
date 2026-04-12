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
        # Lite 后端从 ./web 提供 SPA（见 internal/router serveFrontendStatic），与 package-lite.sh 一致须用 dist 更新 web
        echo ">> Sync frontend/dist -> web/"
        rm -rf web
        cp -r frontend/dist web
    else
        echo ">> No frontend/package.json found, skipping frontend build"
    fi
fi

if [ ! -f web/index.html ]; then
    echo "WARNING: web/index.html not found. Lite 桌面会从 Resources/web 提供前端；请先完整构建前端（勿使用 SKIP_FRONTEND），或手动: cp -r frontend/dist web"
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

# Milvus 与 Qdrant 的 gRPC 生成代码均注册名为 "common.proto" 的描述符，同一进程内会冲突。
# -ldflags -X conflictPolicy=warn 只作用于最终链接，无法覆盖 Wails「生成绑定」时单独启动的 go 子进程。
# 官方做法：见 https://protobuf.dev/reference/go/faq#namespace-conflict
export GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn

# 获取版本号并配置 LDFLAGS
eval "$(./scripts/get_version.sh env)"
LDFLAGS="$(./scripts/get_version.sh ldflags) -X 'google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn'"

# 默认生成 Wails 绑定（供 window.go.main.App.* 等）；若加 -skipbindings 则需在 OnDomReady 注入 __WEKNORA_API_BASE__
(cd cmd/desktop && wails build -clean -tags "sqlite_fts5" -ldflags="$LDFLAGS" -o "${APP_NAME}")

# ── Step 3: Copy generated .app to dist ──
echo ">> Assembling package..."
mkdir -p dist
rm -rf "${DIST_DIR}"
cp -R "cmd/desktop/build/bin/${APP_BUNDLE}" "dist/"

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
if [ -d config ]; then
    cp -r config/* "${RESOURCES_DIR}/config/"
fi
if [ -d web ]; then
    cp -r web "${RESOURCES_DIR}/"
fi

# gojieba 字典文件（中文分词）
JIEBA_SRC="$(go env GOMODCACHE)/github.com/yanyiwu/gojieba@v1.4.5/deps/cppjieba/dict"
if [ -d "${JIEBA_SRC}" ]; then
    mkdir -p "${RESOURCES_DIR}/jieba_dict"
    cp "${JIEBA_SRC}/jieba.dict.utf8" "${JIEBA_SRC}/hmm_model.utf8" \
       "${JIEBA_SRC}/user.dict.utf8" "${JIEBA_SRC}/idf.utf8" \
       "${JIEBA_SRC}/stop_words.utf8" "${RESOURCES_DIR}/jieba_dict/"
fi

# 注意：Wails build 生成的二进制文件工作目录默认是 app 的 Contents/MacOS 目录
# 后续可能需要调整代码中对配置文件的路径读取逻辑。

echo ""
echo "=== Done ==="
echo "  App generated at: dist/${APP_BUNDLE}"
echo "  You can double click it to run!"
