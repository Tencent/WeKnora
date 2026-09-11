#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BACKEND_CONTAINER="${LOCAL_ACCEPTANCE_BACKEND_CONTAINER:-vidsage-custom-backend}"
PREVIEW_BINARY="${LOCAL_ACCEPTANCE_PREVIEW_BINARY:-/app/training-orchestration-preview}"
DEFAULT_OUTPUT_PATH="$PROJECT_ROOT/../output/acceptance/training-two-stage-orchestration/stage-06/real-run-4-generation-report.json"

usage() {
    cat <<'EOF'
在 acceptance custom-backend 容器内执行隔离真实生成预览。

用法:
  ./scripts/run-training-orchestration-preview.sh [报告路径] [日志路径]

说明:
  预览程序运行在 acceptance 容器内，复用容器注入的数据库、WeKnora、
  知识库和模型配置。报告和 stderr 日志写回宿主机。
EOF
}

die() {
    printf '[ERROR] %s\n' "$1" >&2
    exit 1
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
    usage
    exit 0
fi

if [[ "$#" -gt 2 ]]; then
    usage >&2
    exit 2
fi

output_path="${1:-}"
if [[ -z "$output_path" ]]; then
    output_path="$DEFAULT_OUTPUT_PATH"
fi
log_path="${2:-}"
if [[ -z "$log_path" ]]; then
    log_path="${output_path%.json}.log"
fi

if [[ "$output_path" != /* ]]; then
    output_path="$PROJECT_ROOT/$output_path"
fi
if [[ "$log_path" != /* ]]; then
    log_path="$PROJECT_ROOT/$log_path"
fi

[[ ! -e "$output_path" ]] || die "报告已存在，为保护验收证据而拒绝覆盖: $output_path"
[[ ! -e "$log_path" ]] || die "日志已存在，为保护验收证据而拒绝覆盖: $log_path"

command -v docker >/dev/null 2>&1 || die "未找到命令: docker"
docker info >/dev/null 2>&1 || die "Docker Desktop 未运行"

container_status="$(docker inspect -f '{{.State.Status}}' "$BACKEND_CONTAINER" 2>/dev/null || true)"
[[ "$container_status" == "running" ]] || die "acceptance 后端容器未运行: $BACKEND_CONTAINER"

container_health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}' "$BACKEND_CONTAINER" 2>/dev/null || true)"
[[ "$container_health" == "healthy" ]] || die "acceptance 后端容器未 healthy: $container_health"

docker exec --user app "$BACKEND_CONTAINER" test -x "$PREVIEW_BINARY" \
    || die "acceptance 容器内未找到可执行预览程序: $PREVIEW_BINARY"

mkdir -p "$(dirname "$output_path")" "$(dirname "$log_path")"

set +e
docker exec --user app "$BACKEND_CONTAINER" "$PREVIEW_BINARY" \
    >"$output_path" 2>"$log_path"
exit_code=$?
set -e

if [[ "$exit_code" -ne 0 ]]; then
    printf '[ERROR] 隔离真实生成预览失败，退出码=%s\n' "$exit_code" >&2
    printf '[ERROR] 报告: %s\n' "$output_path" >&2
    printf '[ERROR] 日志: %s\n' "$log_path" >&2
    exit "$exit_code"
fi

printf '[SUCCESS] 隔离真实生成预览已完成\n'
printf '报告: %s\n' "$output_path"
printf '日志: %s\n' "$log_path"
