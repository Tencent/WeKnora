#!/bin/bash
# 开发环境启动脚本 - 只启动基础设施，app 和 frontend 需要手动在本地运行

# 设置颜色
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # 无颜色

# 获取项目根目录
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# 日志函数
log_info() {
    printf "%b\n" "${BLUE}[INFO]${NC} $1"
}

log_success() {
    printf "%b\n" "${GREEN}[SUCCESS]${NC} $1"
}

log_error() {
    printf "%b\n" "${RED}[ERROR]${NC} $1"
}

log_warning() {
    printf "%b\n" "${YELLOW}[WARNING]${NC} $1"
}

# 选择可用的 Docker Compose 命令
DOCKER_COMPOSE_BIN=""
DOCKER_COMPOSE_SUBCMD=""

detect_compose_cmd() {
    if docker compose version &> /dev/null; then
        DOCKER_COMPOSE_BIN="docker"
        DOCKER_COMPOSE_SUBCMD="compose"
        return 0
    fi
    if command -v docker-compose &> /dev/null; then
        if docker-compose version &> /dev/null; then
            DOCKER_COMPOSE_BIN="docker-compose"
            DOCKER_COMPOSE_SUBCMD=""
            return 0
        fi
    fi
    return 1
}

# 显示帮助信息
show_help() {
    printf "%b\n" "${GREEN}WeKnora 开发环境脚本${NC}"
    echo "用法: $0 [命令] [选项]"
    echo ""
    echo "命令:"
    echo "  start      启动基础设施服务（postgres, redis, docreader, langfuse）"
    echo "  stop       停止所有服务"
    echo "  restart    重启所有服务"
    echo "  logs       查看服务日志"
    echo "  status     查看服务状态"
    echo "  app        启动后端应用（本地运行）"
    echo "  frontend   启动前端开发服务器（本地运行）"
    echo "  help       显示此帮助信息"
    echo ""
    echo "可选 Profile（用于 start 命令）:"
    echo "  --minio       启动 MinIO 对象存储"
    echo "  --qdrant      启动 Qdrant 向量数据库"
    echo "  --neo4j       启动 Neo4j 图数据库"
    echo "  --dex         启动 Dex（OIDC 身份认证）"
    echo "  --langfuse    启动 Langfuse（默认已开启）"
    echo "  --no-langfuse 不启动 Langfuse"
    echo "  --odl-hybrid  启动 OpenDataLoader hybrid（Docling，镜像较大，按需启用）"
    echo "  --full        启动所有可选服务（不含 odl-hybrid，需另加 --odl-hybrid）"
    echo ""
    echo "示例："
    echo "  $0 start                    # 启动基础服务"
    echo "  $0 start --qdrant           # 启动基础服务 + Qdrant"
    echo "  $0 start --dex             # 启动基础服务 + Dex"
    echo "  $0 start --odl-hybrid       # 启动基础服务 + OpenDataLoader hybrid"
    echo "  $0 start --full             # 启动所有服务"
    echo "  make dev-start DEV_ARGS=--odl-hybrid   # 同上（Makefile 传参）"
    echo "  $0 app                      # 在另一个终端启动后端"
    echo "  $0 frontend                 # 在另一个终端启动前端"
}

# 检查 Docker
check_docker() {
    if ! command -v docker &> /dev/null; then
        log_error "未安装Docker，请先安装Docker"
        return 1
    fi
    
    if ! detect_compose_cmd; then
        log_error "未检测到 Docker Compose"
        return 1
    fi
    
    if ! docker info &> /dev/null; then
        log_error "Docker服务未运行"
        return 1
    fi
    
    return 0
}

# 检查 .env 是否启用了 hybrid 模式（用于 --odl-hybrid 启动后重建 docreader）
_should_enable_odl_hybrid_from_env() {
    local hybrid="${DOCREADER_ODL_HYBRID:-off}"
    hybrid=$(printf '%s' "$hybrid" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
    case "$hybrid" in
        off|"") return 1 ;;
        *) return 0 ;;
    esac
}

_enable_odl_hybrid_profile() {
    PROFILES="$PROFILES --profile odl-hybrid"
    ENABLED_SERVICES="$ENABLED_SERVICES odl-hybrid"
}

# 等待 odl-hybrid HTTP 健康检查通过（compose 启动后服务可能仍在拉依赖）
_wait_odl_hybrid_ready() {
    local port="${ODL_HYBRID_PORT:-5002}"
    local max_wait="${ODL_HYBRID_STARTUP_WAIT_SEC:-180}"
    local waited=0
    local interval=5

    if ! command -v curl &> /dev/null; then
        log_warning "未安装 curl，跳过 odl-hybrid 就绪等待；请手动检查 http://localhost:${port}/health"
        return 0
    fi

    log_info "等待 odl-hybrid 就绪（最多 ${max_wait}s，首次需构建镜像: docker compose ... build odl-hybrid）..."
    while [ "$waited" -lt "$max_wait" ]; do
        if curl -sf "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
            log_success "odl-hybrid 已就绪 (http://localhost:${port}/health)"
            return 0
        fi
        sleep "$interval"
        waited=$((waited + interval))
    done
    log_warning "odl-hybrid 在 ${max_wait}s 内未就绪，请查看: docker logs WeKnora-odl-hybrid"
    return 1
}

# 启动基础设施服务
start_services() {
    log_info "启动开发环境基础设施服务..."
    
    check_docker
    if [ $? -ne 0 ]; then
        return 1
    fi
    
    cd "$PROJECT_ROOT"
    
    # 检查 .env 文件
    if [ ! -f ".env" ]; then
        log_error ".env 文件不存在，请先创建"
        return 1
    fi

    set -a
    # shellcheck source=/dev/null
    source .env
    set +a
    
    # 解析 profile 参数
    shift  # 移除 "start" 命令本身
    # 默认启动基础设施（postgres / redis / docreader）+ langfuse，
    # 其余可选服务通过 --minio / --qdrant / --neo4j / --dex / --full 按需开启。
    PROFILES="--profile langfuse"
    ENABLED_SERVICES="langfuse"
    while [ $# -gt 0 ]; do
        case "$1" in
            --minio)
                PROFILES="$PROFILES --profile minio"
                ENABLED_SERVICES="$ENABLED_SERVICES minio"
                ;;
            --qdrant)
                PROFILES="$PROFILES --profile qdrant"
                ENABLED_SERVICES="$ENABLED_SERVICES qdrant"
                ;;
            --neo4j)
                PROFILES="$PROFILES --profile neo4j"
                ENABLED_SERVICES="$ENABLED_SERVICES neo4j"
                ;;
            --dex)
                PROFILES="$PROFILES --profile dex"
                ENABLED_SERVICES="$ENABLED_SERVICES dex"
                ;;
            --langfuse)
                PROFILES="$PROFILES --profile langfuse"
                ENABLED_SERVICES="$ENABLED_SERVICES langfuse"
                ;;
            --no-langfuse)
                PROFILES="${PROFILES//--profile langfuse/}"
                ENABLED_SERVICES="${ENABLED_SERVICES//langfuse/}"
                ;;
            --odl-hybrid)
                if [[ "$ENABLED_SERVICES" != *"odl-hybrid"* ]]; then
                    _enable_odl_hybrid_profile
                fi
                ;;
            --full)
                PROFILES="--profile full"
                ENABLED_SERVICES="minio qdrant neo4j dex"
                break
                ;;
            *)
                log_warning "未知参数: $1"
                ;;
        esac
        shift
    done

    # 启动服务（odl-hybrid 单独 --build，避免每次重建 docreader）
    "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD -f docker-compose.dev.yml $PROFILES up -d
    local compose_rc=$?
    if [ "$compose_rc" -eq 0 ] && [[ "$ENABLED_SERVICES" == *"odl-hybrid"* ]]; then
        log_info "构建/更新 odl-hybrid 镜像..."
        "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD -f docker-compose.dev.yml $PROFILES up -d --build odl-hybrid
        _wait_odl_hybrid_ready || true
        # docreader 需读取 DOCREADER_ODL_HYBRID；若刚改 .env，强制重建以注入环境变量
        if _should_enable_odl_hybrid_from_env; then
            log_info "重建 docreader 以应用 DOCREADER_ODL_HYBRID=${DOCREADER_ODL_HYBRID} ..."
            "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD -f docker-compose.dev.yml up -d --force-recreate docreader
        fi
    fi

    if [ "$compose_rc" -eq 0 ]; then
        log_success "基础设施服务已启动"
        echo ""
        log_info "服务访问地址:"
        echo "  - PostgreSQL:    localhost:5432"
        echo "  - Redis:         localhost:6379"
        echo "  - DocReader:     localhost:50051"
        
        # 根据启用的 profile 显示额外服务
        if [[ "$ENABLED_SERVICES" == *"minio"* ]]; then
            echo "  - MinIO:         localhost:9000 (Console: localhost:9001)"
        fi
        if [[ "$ENABLED_SERVICES" == *"qdrant"* ]]; then
            echo "  - Qdrant:        localhost:6333 (gRPC: localhost:6334)"
        fi
        if [[ "$ENABLED_SERVICES" == *"neo4j"* ]]; then
            echo "  - Neo4j:         localhost:7474 (Bolt: localhost:7687)"
        fi
        if [[ "$ENABLED_SERVICES" == *"dex"* ]]; then
            echo "  - Dex:           localhost:5556"
        fi
        if [[ "$ENABLED_SERVICES" == *"langfuse"* ]]; then
            echo "  - Langfuse:      http://localhost:${LANGFUSE_WEB_PORT:-3000}"
        fi
        if [[ "$ENABLED_SERVICES" == *"odl-hybrid"* ]]; then
            echo "  - ODL Hybrid:    http://localhost:${ODL_HYBRID_PORT:-5002} (health: /health)"
            echo "                   docreader 需 DOCREADER_ODL_HYBRID=docling-fast"
        fi
        
        echo ""
        log_info "接下来的步骤:"
        printf "%b\n" "${YELLOW}1. 在新终端运行后端:${NC} make dev-app"
        printf "%b\n" "${YELLOW}2. 在新终端运行前端:${NC} make dev-frontend"
        return 0
    else
        log_error "服务启动失败"
        return 1
    fi
}

# 停止服务
stop_services() {
    log_info "停止开发环境服务..."
    
    check_docker
    if [ $? -ne 0 ]; then
        return 1
    fi
    
    cd "$PROJECT_ROOT"
    "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD -f docker-compose.dev.yml down
    
    if [ $? -eq 0 ]; then
        log_success "所有服务已停止"
        return 0
    else
        log_error "服务停止失败"
        return 1
    fi
}

# 重启服务
restart_services() {
    stop_services
    sleep 2
    start_services
}

# 查看日志
show_logs() {
    cd "$PROJECT_ROOT"
    "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD -f docker-compose.dev.yml logs -f
}

# 查看状态
show_status() {
    cd "$PROJECT_ROOT"
    "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD -f docker-compose.dev.yml ps
}

# 启动后端应用（本地）
start_app() {
    log_info "启动后端应用（本地开发模式）..."
    
    cd "$PROJECT_ROOT"
    
    # 检查 Go 是否安装
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装"
        return 1
    fi
    
    # 加载环境变量（使用 set -a 确保所有变量都被导出）
    if [ -f ".env" ]; then
        log_info "加载 .env 文件..."
        set -a
        source .env
        set +a
    else
        log_error ".env 文件不存在，请先创建配置文件"
        return 1
    fi
    
    # 设置本地开发环境变量（覆盖 Docker 容器地址）
    export DB_HOST=localhost
    export DOCREADER_ADDR=localhost:50051
    export DOCREADER_TRANSPORT=grpc
    export MINIO_ENDPOINT=localhost:9000
    export REDIS_ADDR=localhost:6379
    export MILVUS_ADDRESS=localhost:19530
    export NEO4J_URI=bolt://localhost:7687
    export QDRANT_HOST=localhost

    # .env.example uses /data/files for the Docker app container, where a
    # volume is mounted at that path. When the backend runs directly on the
    # host via dev-app, /data is often read-only or missing, so use a repo-local
    # writable directory unless the developer explicitly configured another
    # local storage path.
    if [ -z "${LOCAL_STORAGE_BASE_DIR:-}" ] || [ "$LOCAL_STORAGE_BASE_DIR" = "/data/files" ]; then
        export LOCAL_STORAGE_BASE_DIR="$PROJECT_ROOT/.local-data/files"
    fi
    mkdir -p "$LOCAL_STORAGE_BASE_DIR"
    
    # 确保必要的环境变量已设置
    if [ -z "$DB_DRIVER" ]; then
        log_error "DB_DRIVER 环境变量未设置，请检查 .env 文件"
        return 1
    fi
    
    log_info "环境变量已设置，启动应用..."
    log_info "数据库地址: $DB_HOST:${DB_PORT:-5432}"
    
    export CGO_CFLAGS="-Wno-deprecated-declarations -Wno-gnu-folding-constant"
    if [[ "$(uname)" == "Darwin" ]]; then
      export CGO_LDFLAGS="-Wl,-no_warn_duplicate_libraries"
    fi

    # 检查是否安装了 Air（热重载工具）
    if command -v air &> /dev/null; then
        log_success "检测到 Air，使用热重载模式启动..."
        log_info "修改 Go 代码后将自动重新编译和重启"
        air
    else
        log_info "未检测到 Air，使用普通模式启动"
        log_warning "提示: 安装 Air 可以实现代码修改后自动重启"
        log_info "安装命令: go install github.com/air-verse/air@latest"
        LDFLAGS="$(./scripts/get_version.sh ldflags) -X 'google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn'"
        go run -ldflags="$LDFLAGS" ./cmd/server
    fi
}

# 启动前端（本地）
start_frontend() {
    log_info "启动前端开发服务器..."
    
    cd "$PROJECT_ROOT/frontend"
    
    # 检查 npm 是否安装
    if ! command -v npm &> /dev/null; then
        log_error "npm 未安装"
        return 1
    fi
    
    # 检查依赖是否已安装
    if [ ! -d "node_modules" ]; then
        log_warning "node_modules 不存在，正在安装依赖..."
        npm install
    fi
    
    log_info "启动 Vite 开发服务器..."
    log_info "前端将运行在 http://localhost:5173"
    log_info "前端 API 代理目标: ${VITE_DEV_PROXY_TARGET:-${FRONTEND_BACKEND_URL:-http://localhost:8080}}"
    
    # 运行开发服务器
    npm run dev
}

# 解析命令
CMD="${1:-help}"
case "$CMD" in
    start)
        start_services "$@"
        ;;
    stop)
        stop_services
        ;;
    restart)
        restart_services
        ;;
    logs)
        show_logs
        ;;
    status)
        show_status
        ;;
    app)
        start_app
        ;;
    frontend)
        start_frontend
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        log_error "未知命令: $CMD"
        show_help
        exit 1
        ;;
esac

exit 0
