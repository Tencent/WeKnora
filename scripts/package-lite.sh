#!/usr/bin/env bash
set -euo pipefail

#
# 本地构建 + 打包 WeKnora Lite 发行包
#
# 用法:
#   ./scripts/package-lite.sh              # 自动检测版本
#   ./scripts/package-lite.sh v0.2.0       # 指定版本号
#   SKIP_FRONTEND=1 ./scripts/package-lite.sh  # 跳过前端构建（使用已有 web/）
#   ALLOW_HEADLESS_PACKAGE=1 permits an explicit headless release.
#

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

# Resolve version
if [ -n "${1:-}" ]; then
    VERSION="$1"
elif command -v git >/dev/null 2>&1; then
    VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")
else
    VERSION="dev"
fi

# VERSION is used in directory names and rm -rf targets: accept a basename only.
if [[ ! "$VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]{0,79}$ ]] || [[ "$VERSION" == *..* ]]; then
    echo "Invalid package version (safe basename required)" >&2
    exit 1
fi
GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)
[[ "$GOOS" =~ ^[a-z0-9]+$ && "$GOARCH" =~ ^[a-z0-9]+$ ]] || { echo "Invalid Go platform" >&2; exit 1; }
ARCHIVE="WeKnora-lite_${VERSION}_${GOOS}_${GOARCH}"
DIST_DIR="dist/${ARCHIVE}"

echo "=== WeKnora Lite Packager ==="
echo "  Version : ${VERSION}"
echo "  Platform: ${GOOS}/${GOARCH}"
echo "  Output  : dist/${ARCHIVE}.tar.gz"
echo ""

# ── Step 1: Build frontend (if not skipped) ──
if [ "${SKIP_FRONTEND:-}" != "1" ]; then
    if [ -f frontend/package.json ]; then
        echo ">> Building frontend..."
        (cd frontend && npm ci --prefer-offline && npm run build)
        rm -rf web
        cp -r frontend/dist web
    else
        echo ">> No frontend/package.json found, skipping frontend build"
    fi
fi

if [ ! -f web/index.html ]; then
    if [[ "${ALLOW_HEADLESS_PACKAGE:-0}" != "1" ]]; then
        echo "Required frontend web/index.html missing; ALLOW_HEADLESS_PACKAGE=1 explicitly allows a headless package" >&2
        exit 1
    fi
    echo "Headless package explicitly allowed"
fi

# ── Step 2: Build Go binary ──
echo ">> Building WeKnora-lite binary..."
export EDITION=lite
eval "$(./scripts/get_version.sh env)"
LDFLAGS="-w -s $(./scripts/get_version.sh ldflags)"
export CGO_CFLAGS="-Wno-deprecated-declarations"
if [ "$(uname)" = "Darwin" ]; then
    export CGO_LDFLAGS="-Wl,-no_warn_duplicate_libraries"
fi
CGO_ENABLED=1 go build -tags "sqlite_fts5" -ldflags="${LDFLAGS}" \
    -o WeKnora-lite ./cmd/server

# ── Step 3: Assemble package ──
echo ">> Assembling package..."
rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}/web"

cp WeKnora-lite "${DIST_DIR}/"
bash ./scripts/copy-licenses.sh "${DIST_DIR}"
if [ -d web ] && [ -f web/index.html ]; then
    cp -r web/* "${DIST_DIR}/web/"
fi
cp .env.lite.example "${DIST_DIR}/"
cp docs/LITE.md "${DIST_DIR}/README.md"
if [ -d config ]; then
    cp -r config "${DIST_DIR}/config"
fi
if [ -d migrations/sqlite ]; then
    mkdir -p "${DIST_DIR}/migrations/sqlite"
    cp -r migrations/sqlite/* "${DIST_DIR}/migrations/sqlite/"
fi
if [ -f deploy/weknora-lite.service ]; then
    cp deploy/weknora-lite.service "${DIST_DIR}/"
fi

# ── Step 4: Create tarball ──
echo ">> Creating tarball..."
(cd dist && COPYFILE_DISABLE=1 tar --no-xattrs -czf "${ARCHIVE}.tar.gz" "${ARCHIVE}")
(cd dist && shasum -a 256 "${ARCHIVE}.tar.gz" > "${ARCHIVE}.tar.gz.sha256")

echo ""
echo "=== Done ==="
echo "  dist/${ARCHIVE}.tar.gz"
echo "  dist/${ARCHIVE}.tar.gz.sha256"
SIZE=$(du -h "dist/${ARCHIVE}.tar.gz" | cut -f1)
echo "  Size: ${SIZE}"
