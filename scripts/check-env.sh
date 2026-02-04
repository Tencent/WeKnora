#!/bin/bash
# 检查开发环境配置

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
    printf "%b\n" "${GREEN}[✓]${NC} $1"
}

log_error() {
    printf "%b\n" "${RED}[✗]${NC} $1"
}

log_warning() {
    printf "%b\n" "${YELLOW}[!]${NC} $1"
}

echo ""
printf "%b\n" "${GREEN}========================================${NC}"
printf "%b\n" "${GREEN}  WeKnora 开发环境配置检查${NC}"
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

cd "$PROJECT_ROOT"

# 检查 .env 文件
log_info "检查 .env 文件..."
if [ -f ".env" ]; then
    log_success ".env 文件存在"
else
    log_error ".env 文件不存在"
    echo ""
    log_info "解决方法："
    echo "  1. 复制示例文件: cp .env.example .env"
    echo "  2. 编辑 .env 文件并配置必要的环境变量"
    exit 1
fi

echo ""
log_info "检查必要的环境变量..."

# 加载 .env 文件
set -a
source .env
set +a

# 检查必要的环境变量
errors=0

check_var() {
    local var_name=$1
    local var_value="${!var_name}"
    
    if [ -z "$var_value" ]; then
        log_error "$var_name 未设置"
        errors=$((errors + 1))
    else
        log_success "$var_name = $var_value"
    fi
}

# 数据库配置
log_info "数据库配置:"
check_var "DB_DRIVER"
check_var "DB_HOST"
check_var "DB_PORT"
check_var "DB_USER"
check_var "DB_PASSWORD"
check_var "DB_NAME"

echo ""
log_info "存储配置:"
check_var "STORAGE_TYPE"

if [ "$STORAGE_TYPE" = "minio" ]; then
    check_var "MINIO_BUCKET_NAME"
fi

echo ""
log_info "Redis 配置:"
check_var "REDIS_ADDR"

echo ""
log_info "Ollama 配置:"
check_var "OLLAMA_BASE_URL"

echo ""
log_info "模型配置:"
if [ -n "$INIT_LLM_MODEL_NAME" ]; then
    log_success "INIT_LLM_MODEL_NAME = $INIT_LLM_MODEL_NAME"
else
    log_warning "INIT_LLM_MODEL_NAME 未设置（可选）"
fi

if [ -n "$INIT_EMBEDDING_MODEL_NAME" ]; then
    log_success "INIT_EMBEDDING_MODEL_NAME = $INIT_EMBEDDING_MODEL_NAME"
else
    log_warning "INIT_EMBEDDING_MODEL_NAME 未设置（可选）"
fi

# 检查 Go 环境
echo ""
log_info "检查 Go 环境..."
if command -v go &> /dev/null; then
    go_version=$(go version)
    log_success "Go 已安装: $go_version"
else
    log_error "Go 未安装"
    errors=$((errors + 1))
fi

# 检查 Air
if command -v air &> /dev/null; then
    log_success "Air 已安装（支持热重载）"
else
    log_warning "Air 未安装（可选，用于热重载）"
    log_info "安装命令: go install github.com/air-verse/air@latest"
fi

# 检查 npm
echo ""
log_info "检查 Node.js 环境..."
if command -v npm &> /dev/null; then
    npm_version=$(npm --version)
    log_success "npm 已安装: $npm_version"
else
    log_error "npm 未安装"
    errors=$((errors + 1))
fi

# 检查 Docker
echo ""
log_info "检查 Docker 环境..."
if command -v docker &> /dev/null; then
    docker_version=$(docker --version)
    log_success "Docker 已安装: $docker_version"
    
    if docker info &> /dev/null; then
        log_success "Docker 服务正在运行"
    else
        log_error "Docker 服务未运行"
        errors=$((errors + 1))
    fi
else
    log_error "Docker 未安装"
    errors=$((errors + 1))
fi

# 检查 Docker Compose
if docker compose version &> /dev/null; then
    compose_version=$(docker compose version)
    log_success "Docker Compose 已安装: $compose_version"
elif command -v docker-compose &> /dev/null; then
    compose_version=$(docker-compose --version)
    log_success "docker-compose 已安装: $compose_version"
else
    log_error "Docker Compose 未安装"
    errors=$((errors + 1))
fi

# 总结
echo ""
printf "%b\n" "${GREEN}========================================${NC}"
if [ $errors -eq 0 ]; then
    log_success "所有检查通过！环境配置正常"
    echo ""
    log_info "下一步："
    echo "  1. 启动开发环境: make dev-start"
    echo "  2. 启动后端: make dev-app"
    echo "  3. 启动前端: make dev-frontend"
else
    log_error "发现 $errors 个问题，请修复后再启动开发环境"
    echo ""
    log_info "常见问题："
    echo "  - 如果 .env 文件不存在，请复制 .env.example"
    echo "  - 确保 DB_DRIVER 设置为 'postgres' 或 'mysql'"
    echo "  - 确保 Docker 服务正在运行"
fi
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

exit $errors

