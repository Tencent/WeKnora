#!/bin/bash
# 快速启动开发环境的一键脚本
# 此脚本会在一个终端中启动所有必需的服务

# 设置颜色
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # 无颜色

# 获取项目根目录
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

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

echo ""
printf "%b\n" "${GREEN}========================================${NC}"
printf "%b\n" "${GREEN}  WeKnora 快速开发环境启动${NC}"
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

# 检查是否在项目根目录
cd "$PROJECT_ROOT"

# 1. 启动基础设施
log_info "步骤 1/3: 启动基础设施服务..."
./scripts/dev.sh start
if [ $? -ne 0 ]; then
    log_error "基础设施启动失败"
    exit 1
fi

# 等待服务就绪
log_info "等待服务启动完成..."
sleep 5

# 2. 询问是否启动后端
echo ""
log_info "步骤 2/3: 启动后端应用"
printf "%b" "${YELLOW}是否在当前终端启动后端? (y/N): ${NC}"
read -r start_backend

if [ "$start_backend" = "y" ] || [ "$start_backend" = "Y" ]; then
    log_info "启动后端..."
    # 在后台启动后端
    nohup bash -c 'cd "'$PROJECT_ROOT'" && ./scripts/dev.sh app' > "$PROJECT_ROOT/logs/backend.log" 2>&1 &
    BACKEND_PID=$!
    echo $BACKEND_PID > "$PROJECT_ROOT/tmp/backend.pid"
    log_success "后端已在后台启动 (PID: $BACKEND_PID)"
    log_info "查看后端日志: tail -f $PROJECT_ROOT/logs/backend.log"
else
    log_warning "跳过后端启动"
    log_info "稍后在新终端运行: make dev-app 或 ./scripts/dev.sh app"
fi

# 3. 询问是否启动前端
echo ""
log_info "步骤 3/3: 启动前端应用"
printf "%b" "${YELLOW}是否在当前终端启动前端? (y/N): ${NC}"
read -r start_frontend

if [ "$start_frontend" = "y" ] || [ "$start_frontend" = "Y" ]; then
    log_info "启动前端..."
    # 在后台启动前端
    nohup bash -c 'cd "'$PROJECT_ROOT'/frontend" && npm run dev' > "$PROJECT_ROOT/logs/frontend.log" 2>&1 &
    FRONTEND_PID=$!
    echo $FRONTEND_PID > "$PROJECT_ROOT/tmp/frontend.pid"
    log_success "前端已在后台启动 (PID: $FRONTEND_PID)"
    log_info "查看前端日志: tail -f $PROJECT_ROOT/logs/frontend.log"
else
    log_warning "跳过前端启动"
    log_info "稍后在新终端运行: make dev-frontend 或 ./scripts/dev.sh frontend"
fi

# 显示总结
echo ""
printf "%b\n" "${GREEN}========================================${NC}"
printf "%b\n" "${GREEN}  启动完成！${NC}"
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

log_info "访问地址:"
echo "  - 前端: http://localhost:5173"
echo "  - 后端 API: http://localhost:8080"
echo "  - MinIO Console: http://localhost:9001"
echo ""

log_info "管理命令:"
echo "  - 查看服务状态: make dev-status"
echo "  - 查看日志: make dev-logs"
echo "  - 停止所有服务: make dev-stop"
echo ""

if [ -f "$PROJECT_ROOT/tmp/backend.pid" ] || [ -f "$PROJECT_ROOT/tmp/frontend.pid" ]; then
    log_warning "停止后台进程:"
    if [ -f "$PROJECT_ROOT/tmp/backend.pid" ]; then
        echo "  - 停止后端: kill \$(cat $PROJECT_ROOT/tmp/backend.pid)"
    fi
    if [ -f "$PROJECT_ROOT/tmp/frontend.pid" ]; then
        echo "  - 停止前端: kill \$(cat $PROJECT_ROOT/tmp/frontend.pid)"
    fi
fi

echo ""
log_success "开发环境已就绪，开始编码吧！"
echo ""

