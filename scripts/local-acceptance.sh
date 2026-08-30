#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="$PROJECT_ROOT/docker-compose.dev.yml"
STATE_DIR="$PROJECT_ROOT/tmp/local-acceptance"
FRONTEND_PID_FILE="$STATE_DIR/frontend.pid"
FRONTEND_LOG="$PROJECT_ROOT/logs/local-acceptance-frontend.log"
FRONTEND_PORT="${LOCAL_ACCEPTANCE_FRONTEND_PORT:-18091}"
BACKEND_PORT="${LOCAL_ACCEPTANCE_BACKEND_PORT:-8090}"
FRONTEND_HOST="${LOCAL_ACCEPTANCE_FRONTEND_HOST:-127.0.0.1}"
ACCEPTANCE_URL="${LOCAL_ACCEPTANCE_URL:-http://127.0.0.1/platform/videos}"
BACKEND_CONTAINER="${LOCAL_ACCEPTANCE_BACKEND_CONTAINER:-vidsage-custom-backend}"
FRONTEND_CONTAINER="${LOCAL_ACCEPTANCE_FRONTEND_CONTAINER:-WeKnora-frontend}"
FRONTEND_IMAGE="${LOCAL_ACCEPTANCE_FRONTEND_IMAGE:-weknora/vidsage-ui:local-acceptance}"
BACKEND_TARGET="${VITE_CUSTOM_BACKEND_TARGET:-http://127.0.0.1:${BACKEND_PORT}}"
OFFICIAL_BACKEND_TARGET="${VITE_DEV_PROXY_TARGET:-${FRONTEND_BACKEND_URL:-http://127.0.0.1:8080}}"

cd "$PROJECT_ROOT"

compose() {
    docker compose -f "$COMPOSE_FILE" "$@"
}

die() {
    printf '[ERROR] %s\n' "$1" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "未找到命令: $1"
}

require_docker() {
    require_command docker
    require_command curl
    docker info >/dev/null 2>&1 || die 'Docker Desktop 未运行，先启动 Docker Desktop 后重试'
    compose version >/dev/null 2>&1 || die '当前 Docker 不支持 Compose'
}

container_exists() {
    docker container inspect "$1" >/dev/null 2>&1
}

container_is_running() {
    [ "$(docker inspect -f '{{.State.Status}}' "$1" 2>/dev/null || true)" = running ]
}

container_is_healthy() {
    [ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$1" 2>/dev/null || true)" = healthy ]
}

start_backend() {
    if container_exists "$BACKEND_CONTAINER"; then
        printf '[INFO] 移除可能来自旧验收项目的 custom-backend 容器: %s\n' "$BACKEND_CONTAINER"
        docker rm -f "$BACKEND_CONTAINER" >/dev/null
    fi
    compose up -d --build postgres minio custom-backend
}

pid_is_running() {
    [ -f "$1" ] && kill -0 "$(cat "$1")" >/dev/null 2>&1
}

wait_for_http() {
    local url="$1"
    local timeout_seconds="$2"
    local elapsed=0
    while [ "$elapsed" -lt "$timeout_seconds" ]; do
        if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    return 1
}

start_frontend() {
    mkdir -p "$STATE_DIR" "$PROJECT_ROOT/logs"
    if pid_is_running "$FRONTEND_PID_FILE"; then
        printf '[INFO] 前端已运行，PID=%s\n' "$(cat "$FRONTEND_PID_FILE")"
        return 0
    fi
    rm -f "$FRONTEND_PID_FILE"
    require_command npm
    [ -d "$PROJECT_ROOT/frontend/node_modules" ] || die '前端依赖不存在，请先在 frontend 目录执行 npm install'
    (
        cd "$PROJECT_ROOT/frontend"
        exec env \
            VITE_CUSTOM_BACKEND_TARGET="$BACKEND_TARGET" \
            VITE_DEV_PROXY_TARGET="$OFFICIAL_BACKEND_TARGET" \
            npm run dev -- --host "$FRONTEND_HOST" --port "$FRONTEND_PORT"
    ) >"$FRONTEND_LOG" 2>&1 &
    echo "$!" > "$FRONTEND_PID_FILE"
    printf '[INFO] 前端启动中，PID=%s\n' "$(cat "$FRONTEND_PID_FILE")"
}

start_fixed_frontend() {
    require_command npm
    [ -d "$PROJECT_ROOT/frontend/node_modules" ] || die '前端依赖不存在，请先在 frontend 目录执行 npm install'
    (
        cd "$PROJECT_ROOT/frontend"
        npm run build
    )
    docker build -t "$FRONTEND_IMAGE" "$PROJECT_ROOT/frontend"
    if container_exists "$FRONTEND_CONTAINER"; then
        docker rm -f "$FRONTEND_CONTAINER" >/dev/null
    fi
    local network
    network="$(docker inspect -f '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "$BACKEND_CONTAINER" 2>/dev/null || true)"
    [ -n "$network" ] || die 'custom-backend 未加入验收网络，无法启动当前前端'
    docker run -d \
        --name "$FRONTEND_CONTAINER" \
        --network "$network" \
        -p "${LOCAL_ACCEPTANCE_HTTP_PORT:-80}:80" \
        "$FRONTEND_IMAGE" >/dev/null
    printf '[INFO] 已用当前源码构建并启动固定验收前端: %s\n' "$FRONTEND_CONTAINER"
    return 0
}

stop_frontend() {
    if ! [ -f "$FRONTEND_PID_FILE" ]; then
        return 0
    fi
    local pid
    pid="$(cat "$FRONTEND_PID_FILE")"
    if kill -0 "$pid" >/dev/null 2>&1; then
        kill "$pid" >/dev/null 2>&1 || true
        for _ in 1 2 3 4 5; do
            kill -0 "$pid" >/dev/null 2>&1 || break
            sleep 1
        done
    fi
    rm -f "$FRONTEND_PID_FILE"
    printf '[INFO] 前端已停止\n'
}

up() {
    require_docker
    start_backend
    if ! wait_for_http "http://127.0.0.1:${BACKEND_PORT}/healthz" 60; then
        container_is_healthy "$BACKEND_CONTAINER" || die "custom-backend 未在 ${BACKEND_PORT} 就绪"
    fi
    start_fixed_frontend
    wait_for_http "$ACCEPTANCE_URL" 30 || die "固定验收前端未在 ${ACCEPTANCE_URL} 就绪"
    printf '\n[SUCCESS] 本地验收服务已就绪\n'
    if wait_for_http "$ACCEPTANCE_URL" 3; then
        printf '前端验收地址: %s\n' "$ACCEPTANCE_URL"
    else
        printf '前端验收地址: http://127.0.0.1:%s/platform/videos\n' "$FRONTEND_PORT"
    fi
    printf '后端健康检查: http://127.0.0.1:%s/healthz\n' "$BACKEND_PORT"
}

down() {
    require_docker
    stop_frontend
    if container_exists "$BACKEND_CONTAINER"; then
        docker stop "$BACKEND_CONTAINER" >/dev/null 2>&1 || true
    fi
    if container_exists "$FRONTEND_CONTAINER"; then
        docker rm -f "$FRONTEND_CONTAINER" >/dev/null 2>&1 || true
    fi
    compose stop minio postgres >/dev/null 2>&1 || true
    printf '[SUCCESS] 本地验收服务已停止，数据卷保留\n'
}

restart() {
    down
    up
}

status() {
    require_docker
    docker ps -a --filter "name=^/${BACKEND_CONTAINER}$" --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'
    if pid_is_running "$FRONTEND_PID_FILE"; then
        printf 'frontend: running (PID=%s)\n' "$(cat "$FRONTEND_PID_FILE")"
    elif wait_for_http "$ACCEPTANCE_URL" 3; then
        printf 'frontend: fixed acceptance URL available (%s)\n' "$ACCEPTANCE_URL"
    else
        printf 'frontend: stopped\n'
    fi
    if curl -fsS --max-time 3 "http://127.0.0.1:${BACKEND_PORT}/healthz" >/dev/null 2>&1; then
        printf 'custom-backend: healthy\n'
    else
        printf 'custom-backend: unavailable\n'
    fi
}

logs() {
    require_docker
    compose logs -f custom-backend
}

usage() {
    cat <<'EOF'
本地视频验收服务

用法:
  ./scripts/local-acceptance.sh up       启动依赖、custom-backend 和前端
  ./scripts/local-acceptance.sh restart  重启本地验收服务
  ./scripts/local-acceptance.sh down     停止服务，保留数据卷
  ./scripts/local-acceptance.sh status   查看服务状态
  ./scripts/local-acceptance.sh logs     查看 custom-backend 日志
EOF
}

case "${1:-up}" in
    up) up ;;
    down) down ;;
    restart) restart ;;
    status) status ;;
    logs) logs ;;
    help|--help|-h) usage ;;
    *) usage; exit 2 ;;
esac
