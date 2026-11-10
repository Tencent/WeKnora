#!/usr/bin/env bash
set -euo pipefail

#
# 本地构建 + 打包 WeKnora Windows 桌面应用（Git Bash / MSYS2 环境）
#
# 用法:
#   ./scripts/package-windows-app.sh
#   SKIP_FRONTEND=1 ./scripts/package-windows-app.sh   # 跳过前端构建（复用已有 web/）
#
# 产物:
#   dist/WeKnora-Lite-App_<版本>_windows_amd64/          便携版目录（免安装，直接运行）
#   dist/WeKnora-Lite-App_<版本>_windows_amd64.zip       便携版压缩包
#   dist/WeKnora-Lite-App_<版本>_windows_amd64_setup.exe NSIS 安装包（需本机安装 NSIS）
#
# 工具链要求:
#   Go 1.24+、Node 22+、Wails CLI（go install github.com/wailsapp/wails/v2/cmd/wails@latest）、
#   MinGW-w64 gcc（CGO 编译 sqlite_fts5）、NSIS（可选，用于安装包）。
#
# 数据目录：安装版/便携版启动后，SQLite 数据库自动落在
#   %APPDATA%\WeKnora Lite\data（见 cmd/desktop/main.go configureWindowsDesktopStorage），
#   与 macOS .app 写 ~/Library/Application Support 的行为一致。
#

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

APP_NAME="WeKnora Lite"
ARCH="amd64"

echo "=== WeKnora Windows App Packager ==="

# ── Step 0: toolchain checks ──
MISSING=0
for tool in go npm; do
    if ! command -v "${tool}" >/dev/null 2>&1; then
        echo "ERROR: ${tool} not found in PATH. 请先安装并加入 PATH。"
        MISSING=1
    fi
done
if ! command -v wails >/dev/null 2>&1; then
    echo "ERROR: Wails CLI not found. Please install it first:"
    echo "  go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    MISSING=1
fi
if ! command -v gcc >/dev/null 2>&1; then
    echo "ERROR: gcc (MinGW-w64) not found. CGO 的 sqlite_fts5 需要本机 C 编译器。"
    echo "  可通过 MSYS2 安装: pacman -S mingw-w64-x86_64-gcc，或使用 winlibs.com 独立包。"
    MISSING=1
fi
if [ "${MISSING}" != "0" ]; then
    exit 1
fi

# NSIS 不在 PATH 时尝试常见安装位置；都找不到则退回只出便携版。
NSIS_FLAG=""
if command -v makensis >/dev/null 2>&1; then
    NSIS_FLAG="-nsis"
else
    for nsis_dir in "/c/Program Files (x86)/NSIS" "/c/Program Files/NSIS"; do
        if [ -f "${nsis_dir}/makensis.exe" ]; then
            export PATH="${nsis_dir}:${PATH}"
            NSIS_FLAG="-nsis"
            break
        fi
    done
fi
if [ "${NSIS_FLAG}" != "-nsis" ]; then
    echo "WARNING: NSIS (makensis) not found — 跳过安装包，仅产出便携版 zip。"
    echo "  安装包支持自动更新并携带许可/source 披露，建议安装: https://nsis.sourceforge.io"
fi

# ── Step 1: Build frontend (if not skipped) ──
if [ "${SKIP_FRONTEND:-}" != "1" ]; then
    if [ -f frontend/package.json ]; then
        echo ">> Building frontend..."
        (cd frontend && npm ci --prefer-offline && npm run build)
        # Lite 后端从 ./web 提供 SPA（internal/router serveFrontendStatic），
        # 与 package-mac-app.sh / release-lite.yml 保持一致：用 dist 更新 web。
        echo ">> Sync frontend/dist -> web/"
        rm -rf web
        cp -r frontend/dist web
    else
        echo ">> No frontend/package.json found, skipping frontend build"
    fi
fi

if [ ! -f web/index.html ]; then
    echo "ERROR: web/index.html not found。桌面端从 exe 旁的 web/ 提供前端，缺失会白屏。"
    echo "  请完整构建前端（勿用 SKIP_FRONTEND），或手动: cp -r frontend/dist web"
    exit 1
fi

# ── Step 2: Build with Wails ──
echo ">> Building Wails Desktop App (windows/${ARCH})..."

export GONOSUMDB="git.sr.ht/*"
export GOPROXY="https://goproxy.cn,direct"
export CGO_ENABLED=1
export CGO_CFLAGS="-Wno-deprecated-declarations"
export EDITION=lite

# Milvus 与 Qdrant 的 gRPC 生成代码均注册名为 "common.proto" 的描述符，同一进程内会冲突。
# 官方做法：见 https://protobuf.dev/reference/go/faq#namespace-conflict
export GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn

# 获取版本号并配置 LDFLAGS
eval "$(./scripts/get_version.sh env)"
LDFLAGS="$(./scripts/get_version.sh ldflags) -X 'google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn'"

# 构建前校验许可清单（与 release-lite.yml 一致；wails 打包会带上 licenses/）
bash ./scripts/check-license-bundle.sh

(cd cmd/desktop && wails build -clean \
    -tags "sqlite_fts5" \
    -platform "windows/${ARCH}" \
    ${NSIS_FLAG} \
    -ldflags "${LDFLAGS}" \
    -o "${APP_NAME}")

# ── Step 3: Assemble portable distribution ──
echo ">> Assembling package..."
DIST_DIR="dist"
STAGE="${DIST_DIR}/WeKnora-Lite-App_${VERSION}_windows_${ARCH}"
BIN_DIR="cmd/desktop/build/bin"

mkdir -p "${DIST_DIR}"
rm -rf "${STAGE}"
mkdir -p "${STAGE}"

cp "${BIN_DIR}/${APP_NAME}.exe" "${STAGE}/"
# .env 来自模板（密钥为空）；桌面端首次启动自动生成签名/JWT 密钥
if [ -f .env.lite.example ]; then
    cp .env.lite.example "${STAGE}/.env"
fi
if [ -d config ]; then
    cp -r config "${STAGE}/config"
fi
if [ -d migrations/sqlite ]; then
    mkdir -p "${STAGE}/migrations/sqlite"
    cp -r migrations/sqlite/* "${STAGE}/migrations/sqlite/"
fi
cp -r web "${STAGE}/web"
cp LICENSE THIRD_PARTY_NOTICES.md "${STAGE}/"
bash ./scripts/copy-licenses.sh "${STAGE}"
cp docs/LITE.md "${STAGE}/README.md"

# ── Step 4: zip（PowerShell 优先：完全支持中文/Unicode 路径；Git Bash 通常无 zip 命令）──
make_zip() {
    local out="$1" dir="$2"
    if command -v zip >/dev/null 2>&1; then
        echo ">> Zipping with zip..."
        (cd "${dir}" && zip -qr "$(cygpath -w "${out}")" .)
        return
    fi
    echo ">> Zipping with PowerShell Compress-Archive..."
    # 路径经环境变量传入（PowerShell 按 data 读取，不拼进命令字符串）
    ZIP_IN="$(cygpath -w "${dir}")" ZIP_OUT="$(cygpath -w "${out}")" \
        powershell.exe -NoProfile -ExecutionPolicy Bypass -Command \
        "Compress-Archive -Force -Path (Get-ChildItem -Force -LiteralPath \$env:ZIP_IN) -DestinationPath \$env:ZIP_OUT"
}

make_zip "${STAGE}.zip" "${STAGE}"
sha256sum "${STAGE}.zip" > "${STAGE}.zip.sha256"

# ── Step 5: NSIS installer (project.nsi 会把 web/config/migrations/.env 一并装入) ──
SETUP=""
INSTALLER_SRC="${BIN_DIR}/${APP_NAME}-${ARCH}-installer.exe"
# 不发布裸 exe：安装包还携带许可/source 披露，且自动更新依赖它（update.go /S 静默安装）
if [ -f "${INSTALLER_SRC}" ]; then
    SETUP="${DIST_DIR}/WeKnora-Lite-App_${VERSION}_windows_${ARCH}_setup.exe"
    cp "${INSTALLER_SRC}" "${SETUP}"
    sha256sum "${SETUP}" > "${SETUP}.sha256"
else
    echo "WARNING: 未找到 NSIS 安装包 ${INSTALLER_SRC}，仅产出便携版。"
fi

echo ""
echo "=== Done ==="
echo "  便携版: ${STAGE}/"
echo "  压缩包: ${STAGE}.zip"
if [ -n "${SETUP}" ]; then
    echo "  安装包: ${SETUP}"
fi
echo "  双击 ${APP_NAME}.exe 即可运行；数据库与上传文件在 %APPDATA%\\WeKnora Lite\\data"
