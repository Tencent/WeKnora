#!/bin/bash
# 不使用 air 的后端启动脚本
# 适用于长时间运行任务（如坚果云同步），避免 air 热重载中断进程

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# 加载 .env
if [ ! -f ".env" ]; then
    echo "ERROR: .env 文件不存在"
    exit 1
fi
set -a
source .env
set +a

# 本地开发覆盖（与 scripts/dev.sh 中 start_app 一致）
export DB_HOST=${DB_HOST:-localhost}
export DOCREADER_ADDR=localhost:50051
export DOCREADER_TRANSPORT=grpc
export MINIO_ENDPOINT=localhost:9000
export REDIS_ADDR=localhost:6379
export MILVUS_ADDRESS=localhost:19530
export OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
export NEO4J_URI=bolt://localhost:7687
export QDRANT_HOST=localhost
export LOCAL_STORAGE_BASE_DIR="$PROJECT_ROOT/storage/data"

# CGO 编译参数
export CGO_CFLAGS="-Wno-deprecated-declarations -Wno-gnu-folding-constant"
if [[ "$(uname)" == "Darwin" ]]; then
    export CGO_LDFLAGS="-Wl,-no_warn_duplicate_libraries"
fi

LDFLAGS="$(./scripts/get_version.sh ldflags) -X 'google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn'"

echo "[dev-noair] DB_HOST=$DB_HOST DB_DRIVER=$DB_DRIVER"
echo "[dev-noair] Starting backend without air..."
exec go run -ldflags="$LDFLAGS" cmd/server/main.go
