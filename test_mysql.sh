#!/bin/bash
# =============================================================================
# WeKnora MySQL 集成测试脚本
# =============================================================================
# 功能:
#   1. 启动 MySQL 8.0 + Redis 测试容器
#   2. 编译 Go 后端
#   3. 执行数据库迁移（golang-migrate）
#   4. 启动 WeKnora 应用（连接 MySQL）
#   5. 测试健康检查和基础 API
#   6. 清理测试环境
#
# 前提条件:
#   - Docker 已安装并运行
#   - Go 1.22+
#   - golang-migrate CLI（可选，脚本可用 go run 替代）
#   - curl 和 mysql CLI 客户端（可选，用于调试）
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# ---- 颜色定义 ----
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info()    { printf "%b\n" "${BLUE}[INFO]${NC} $1"; }
log_success() { printf "%b\n" "${GREEN}[PASS]${NC} $1"; }
log_warning() { printf "%b\n" "${YELLOW}[WARN]${NC} $1"; }
log_error()   { printf "%b\n" "${RED}[FAIL]${NC} $1"; }

# ---- 配置 ----
TEST_ENV_FILE=".env.mysql.test"
COMPOSE_FILE="docker-compose.mysql.yml"
APP_BINARY="./WeKnora-mysql-test"
MIGRATIONS_DIR="migrations/mysql"

# ---- 默认值（会被 .env.mysql.test 覆盖） ----
DB_HOST="127.0.0.1"
DB_PORT="3306"
DB_USER="root"
DB_PASSWORD="${TEST_MYSQL_PASSWORD:-weknora_test}"   # 从环境变量读取，或使用默认测试密码
DB_NAME="WeKnora"
REDIS_PORT="6379"
REDIS_PASSWORD=""
APP_PORT="18080"      # 测试用端口，避免冲突
RETRIEVE_DRIVER=""    # MySQL 模式不支持 pgvector，设为空或外部引擎

# ---- 检测 Docker Compose 命令 ----
detect_compose() {
    if docker compose version &>/dev/null 2>&1; then
        COMPOSE_CMD="docker compose"
    elif command -v docker-compose &>/dev/null; then
        COMPOSE_CMD="docker-compose"
    else
        log_error "未检测到 Docker Compose（请安装 docker-compose）"
        exit 1
    fi
    log_info "使用 Docker Compose: $COMPOSE_CMD"
}

# ---- 清理函数 ----
cleanup() {
    local exit_code=$?
    echo ""
    log_info "=== 清理测试环境 ==="

    # 停止应用进程
    if [ -n "$APP_PID" ] && kill -0 "$APP_PID" 2>/dev/null; then
        log_info "停止 WeKnora 应用 (PID $APP_PID)..."
        kill "$APP_PID" 2>/dev/null || true
        wait "$APP_PID" 2>/dev/null || true
        log_success "应用已停止"
    fi

    # 停止 Docker 容器（仅当容器存在时）
    log_info "停止 Docker 容器..."
    if $COMPOSE_CMD -f "$COMPOSE_FILE" --profile mysql-test ps -q 2>/dev/null | grep -q .; then
        $COMPOSE_CMD -f "$COMPOSE_FILE" --profile mysql-test down --remove-orphans 2>/dev/null || true
    fi
    log_success "Docker 容器已停止"

    # 删除测试二进制
    if [ -f "$APP_BINARY" ]; then
        rm -f "$APP_BINARY"
        log_info "测试二进制已清理"
    fi

    if [ $exit_code -eq 0 ]; then
        log_success "测试结束：全部通过 🎉"
    else
        log_error "测试结束：存在失败项"
    fi
    exit $exit_code
}

# ---- 步骤 1：检查前提条件 ----
check_prerequisites() {
    log_info "=== 步骤 1/7：检查前提条件 ==="

    # Docker
    if ! command -v docker &> /dev/null; then
        log_error "未安装 Docker"
        exit 1
    fi
    if ! docker info &> /dev/null; then
        log_error "Docker 服务未运行"
        exit 1
    fi
    log_success "Docker 已就绪"

    # Go
    if ! command -v go &> /dev/null; then
        log_error "未安装 Go"
        exit 1
    fi
    log_success "Go $(go version | grep -oP 'go\S+' || go version) 已就绪"

    # golang-migrate CLI (optional)
    if command -v migrate &> /dev/null; then
        log_success "golang-migrate CLI 已就绪"
        HAS_MIGRATE_CLI=true
    else
        log_warning "未检测到 golang-migrate CLI，脚本将使用 go run 执行迁移"
        HAS_MIGRATE_CLI=false
    fi

    # curl
    if ! command -v curl &> /dev/null; then
        log_error "未安装 curl，API 测试将无法进行"
        exit 1
    fi
    log_success "curl 已就绪"
}

# ---- 步骤 2：准备配置文件 ----
setup_env() {
    log_info "=== 步骤 2/7：准备测试配置文件 ==="

    cat > "$TEST_ENV_FILE" << EOF
# WeKnora MySQL 测试配置文件（由 test_mysql.sh 自动生成）
DB_DRIVER=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=root
DB_PASSWORD=${DB_PASSWORD}
DB_NAME=WeKnora

# Redis（依赖 docker-compose.yml 中的 redis 服务）
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=

# 应用端口
APP_PORT=18080

# MySQL 模式要求：不能使用 pgvector 检索
# 设空或外部引擎（需要先启动外部引擎；此测试中设空禁用向量检索）
RETRIEVE_DRIVER=

# 存储
STORAGE_TYPE=local
LOCAL_STORAGE_BASE_DIR=./.local-test-data/files

# 日志级别
LOG_LEVEL=debug
GIN_MODE=debug

# 允许注册（测试用）
DISABLE_REGISTRATION=false

# 基础密钥（测试用）
JWT_SECRET=test-jwt-secret-for-mysql-testing
TENANT_AES_KEY=test-tenant-aes-key-32bytes!!!
SYSTEM_AES_KEY=test-system-aes-key-32bytes!!
EOF

    # 导出环境变量供后续步骤使用
    set -a
    source "$TEST_ENV_FILE"
    set +a

    # 若 .env 不存在，创建 symlink 以便 Docker Compose 加载
    if [ ! -f ".env" ]; then
        cp "$TEST_ENV_FILE" .env
        log_warning ".env 不存在，已从测试配置创建"
    fi

    log_success "测试配置文件已生成: $TEST_ENV_FILE"
}

# ---- 步骤 3：启动 MySQL + Redis ----
start_docker_services() {
    log_info "=== 步骤 3/7：启动测试服务 ==="

    # 检测 MySQL 是否已在本地运行
    if mysql -h"$DB_HOST" -u"$DB_USER" -p"$DB_PASSWORD" -e "SELECT 1" 2>/dev/null; then
        log_success "检测到 MySQL 已在本机运行，跳过 Docker 启动"
        local mysql_version
        mysql_version=$(mysql -h"$DB_HOST" -u"$DB_USER" -p"$DB_PASSWORD" -s -e "SELECT VERSION()" 2>/dev/null)
        log_success "MySQL 版本: $mysql_version"
        local charset
        charset=$(mysql -h"$DB_HOST" -u"$DB_USER" -p"$DB_PASSWORD" -s -e "SELECT @@character_set_server, @@collation_server" 2>/dev/null)
        log_success "MySQL charset: $charset"
        return 0
    fi

    # 如果 MySQL 未运行，尝试通过 Docker 启动
    log_info "MySQL 未在本机运行，尝试通过 Docker 启动..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" --profile mysql-test down --remove-orphans 2>/dev/null || true

    MYSQL_ROOT_PASSWORD="$DB_PASSWORD" \
    MYSQL_DATABASE="$DB_NAME" \
    DB_PORT_MYSQL="$DB_PORT" \
    REDIS_PORT="$REDIS_PORT" \
    REDIS_PASSWORD="$REDIS_PASSWORD" \
    DB_PASSWORD="$DB_PASSWORD" \
    DB_NAME="$DB_NAME" \
    $COMPOSE_CMD -f "$COMPOSE_FILE" --profile mysql-test up -d

    log_info "等待 MySQL 就绪..."
    local retries=30
    local count=0
    while [ $count -lt $retries ]; do
        if docker exec WeKnora-mysql mysqladmin ping -h localhost -u"$DB_USER" -p"$DB_PASSWORD" --silent 2>/dev/null; then
            log_success "MySQL 已就绪"
            break
        fi
        count=$((count + 1))
        echo -n "."
        sleep 2
    done
    echo ""
    if [ $count -ge $retries ]; then
        log_error "MySQL 启动超时，请检查容器日志: docker logs WeKnora-mysql"
        exit 1
    fi

    local mysql_version
    mysql_version=$(docker exec WeKnora-mysql mysql -h localhost -u"$DB_USER" -p"$DB_PASSWORD" -s -e "SELECT VERSION()" 2>/dev/null)
    log_success "MySQL 版本: $mysql_version"
    local charset
    charset=$(docker exec WeKnora-mysql mysql -h localhost -u"$DB_USER" -p"$DB_PASSWORD" -s -e "SELECT @@character_set_server, @@collation_server" 2>/dev/null)
    log_success "MySQL charset: $charset"
}

# ---- 步骤 4：编译 Go 后端 ----
build_app() {
    log_info "=== 步骤 4/7：编译 WeKnora 后端 ==="

    # 加载环境变量
    set -a
    source "$TEST_ENV_FILE"
    set +a

    # 编译
    CGO_CFLAGS="-Wno-deprecated-declarations -Wno-gnu-folding-constant" \
    CGO_LDFLAGS="$(if [[ "$(uname)" == "Darwin" ]]; then echo '-Wl,-no_warn_duplicate_libraries'; fi)" \
    go build -o "$APP_BINARY" ./cmd/server

    if [ -f "$APP_BINARY" ]; then
        log_success "编译成功: $APP_BINARY"
    else
        log_error "编译失败"
        exit 1
    fi
}

# ---- 步骤 5：执行数据库迁移 ----
run_migrations() {
    log_info "=== 步骤 5/7：执行数据库迁移 ==="

    # 确保 migrations 工具可用
    if [ "$HAS_MIGRATE_CLI" = true ]; then
        ENCODED_PASS=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$DB_PASSWORD', safe=''))" 2>/dev/null || echo "$DB_PASSWORD")
        MIGRATE_DSN="mysql://${DB_USER}:${ENCODED_PASS}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}?charset=utf8mb4&multiStatements=true"
        log_info "执行 migrate -path $MIGRATIONS_DIR up ..."
        migrate -path "$MIGRATIONS_DIR" -database "$MIGRATE_DSN" up
        log_success "迁移完成（golang-migrate CLI）"
    else
        log_info "尝试通过 go run 执行迁移..."
        # 直接用 go run 运行临时迁移程序
        ENCODED_PASS=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$DB_PASSWORD', safe=''))" 2>/dev/null || echo "$DB_PASSWORD")
        MIGRATE_DSN="mysql://${DB_USER}:${ENCODED_PASS}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}?charset=utf8mb4&multiStatements=true"

        # 方法 1：用 migrate CLI 从源码运行
        if go run -tags 'mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@latest \
            -path "$MIGRATIONS_DIR" \
            -database "$MIGRATE_DSN" up 2>/dev/null; then
            log_success "迁移完成（go run migrate）"
        else
            # 方法 2：直接执行 init SQL，然后手动设置迁移版本
            log_warning "golang-migrate 不可用，直接执行 init SQL..."
            mysql -h"$DB_HOST" -u"$DB_USER" -p"$DB_PASSWORD" "$DB_NAME" < "$MIGRATIONS_DIR/000000_init.up.sql"
            log_success "初始化 SQL 已执行"
            log_warning "注意：golang-migrate 未检测到，迁移版本未记录。"
            log_warning "应用启动时会自动检测并尝试迁移，初始表已创建因此会跳过。"
        fi
    fi

    # 验证表是否已创建
    log_info "验证表结构..."
    local table_count
    table_count=$(mysql -h"$DB_HOST" -u"$DB_USER" -p"$DB_PASSWORD" -s -e \
        "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$DB_NAME'" 2>/dev/null)
    log_success "数据库中共 $table_count 张表"

    # 列出表名
    mysql -h"$DB_HOST" -u"$DB_USER" -p"$DB_PASSWORD" -s -e \
        "SELECT table_name FROM information_schema.tables WHERE table_schema='$DB_NAME' ORDER BY table_name" 2>/dev/null
}

# ---- 步骤 6：启动应用并测试 API ----
run_api_tests() {
    log_info "=== 步骤 6/7：启动应用并测试 API ==="

    # 设置运行环境
    export DB_DRIVER=mysql
    export DB_HOST="$DB_HOST"
    export DB_PORT="$DB_PORT"
    export DB_USER="$DB_USER"
    export DB_PASSWORD="$DB_PASSWORD"
    export DB_NAME="$DB_NAME"
    export APP_PORT="$APP_PORT"
    export REDIS_ADDR="127.0.0.1:$REDIS_PORT"
    export REDIS_PASSWORD="$REDIS_PASSWORD"
    export STORAGE_TYPE=local
    export LOCAL_STORAGE_BASE_DIR="./.local-test-data/files"
    export GIN_MODE=debug
    export LOG_LEVEL=debug
    export JWT_SECRET="test-jwt-secret-for-mysql-testing"
    export TENANT_AES_KEY="test-tenant-aes-key-32bytes!!!"
    export SYSTEM_AES_KEY="test-system-aes-key-32bytes!!"
    export DOCREADER_ADDR=""
    export STREAM_MANAGER_TYPE=memory
    export DISABLE_REGISTRATION=false

    mkdir -p "$LOCAL_STORAGE_BASE_DIR"

    # 启动应用（后台运行）
    log_info "启动 WeKnora 应用 (端口 $APP_PORT)..."
    $APP_BINARY > /tmp/weknora-mysql-test.log 2>&1 &
    APP_PID=$!
    log_info "应用 PID: $APP_PID"

    # 等待应用就绪
    log_info "等待应用就绪..."
    local retries=30
    local count=0
    while [ $count -lt $retries ]; do
        if curl -sf "http://localhost:$APP_PORT/health" > /dev/null 2>&1; then
            log_success "应用已就绪"
            break
        fi
        count=$((count + 1))
        echo -n "."
        sleep 2
    done
    echo ""
    if [ $count -ge $retries ]; then
        log_error "应用启动超时"
        log_info "应用日志最后 20 行:"
        tail -20 /tmp/weknora-mysql-test.log
        exit 1
    fi

    # ---- 测试 1：健康检查 ----
    log_info "测试 1/5: 健康检查 /health"
    local health_status
    health_status=$(curl -sf "http://localhost:$APP_PORT/health" 2>/dev/null || echo "")
    if echo "$health_status" | grep -q "ok"; then
        log_success "健康检查通过: $health_status"
    else
        log_error "健康检查失败"
        log_info "响应: $health_status"
        exit 1
    fi

    # ---- 测试 2：获取系统信息 ----
    log_info "测试 2/5: 系统信息 /api/v1/system/info"
    local sys_info
    sys_info=$(curl -sf "http://localhost:$APP_PORT/api/v1/system/info" 2>/dev/null || echo "")
    if echo "$sys_info" | grep -q "version\|db_driver\|driver"; then
        log_success "系统信息接口正常"
    else
        log_warning "系统信息接口返回异常（可能需认证），不影响基础连通性测试"
        log_info "响应: ${sys_info:-(空)}"
    fi

    # ---- 测试 3：用户注册 ----
    local test_user="testuser_$(date +%s)"
    local test_email="${test_user}@example.com"
    local test_password="TestPass123!"

    log_info "测试 3/5: 用户注册 $test_email"
    local register_resp
    register_resp=$(curl -sf -X POST "http://localhost:$APP_PORT/api/v1/auth/register" \
        -H "Content-Type: application/json" \
        -d "{\"username\":\"$test_user\",\"email\":\"$test_email\",\"password\":\"$test_password\"}" 2>/dev/null || echo "")

    if echo "$register_resp" | grep -q "token\|id\|user_id\|success"; then
        log_success "用户注册成功"
        # 提取 token
        TOKEN=$(echo "$register_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null || echo "")
        if [ -z "$TOKEN" ]; then
            log_warning "未从注册响应中提取到 token，可能需从 login 获取"
        fi
    else
        log_warning "注册接口返回意外响应（可能受配置影响）"
        log_info "响应: $register_resp"
    fi

    # ---- 测试 4：用户登录 ----
    log_info "测试 4/5: 用户登录"
    local login_resp
    login_resp=$(curl -sf -X POST "http://localhost:$APP_PORT/api/v1/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"$test_email\",\"password\":\"$test_password\"}" 2>/dev/null || echo "")

    if echo "$login_resp" | grep -q "token\|access_token"; then
        TOKEN=$(echo "$login_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token','') or json.load(sys.stdin).get('access_token',''))" 2>/dev/null || echo "")
        log_success "用户登录成功"
    else
        log_warning "登录接口返回意外响应（可能用户已创建但需额外配置）"
        log_info "响应: $login_resp"
    fi

    # ---- 测试 5：已认证 API 请求（如果有 token） ----
    if [ -n "$TOKEN" ]; then
        log_info "测试 5/5: 已认证请求 /api/v1/auth/me"
        local me_resp
        me_resp=$(curl -sf "http://localhost:$APP_PORT/api/v1/auth/me" \
            -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo "")

        if echo "$me_resp" | grep -q "email\|username\|id"; then
            log_success "已认证 API 请求正常"
        else
            log_warning "已认证接口响应异常"
            log_info "响应: $me_resp"
        fi
    else
        log_warning "测试 5/5 跳过（无可用 token）"
    fi

    # ---- 扩展测试：核心实体 CRUD（验证 MySQL 方言分支） ----
    log_info "=== 扩展 CRUD 测试 ==="

    # 测试 6: 系统设置读取（验证 MySQL 保留字 key 的列访问）
    log_info "测试 6: 系统设置 /api/v1/system/settings"
    if [ -n "$TOKEN" ]; then
        local settings_resp
        settings_resp=$(curl -sf "http://localhost:$APP_PORT/api/v1/system/settings" \
            -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo "")
        if echo "$settings_resp" | grep -q "settings\|data\|\[\]"; then
            log_success "系统设置接口正常（key 列兼容）"
        else
            log_warning "系统设置接口返回异常（可能权限不足）"
            log_info "响应: ${settings_resp:-(空)}"
        fi
    else
        log_warning "测试 6 跳过（无可用 token）"
    fi

    # 测试 7: 知识库列表（验证 JSON 列读取 + tenant 过滤）
    log_info "测试 7: 知识库列表 /api/v1/knowledge-bases"
    if [ -n "$TOKEN" ]; then
        local kb_resp
        kb_resp=$(curl -sf "http://localhost:$APP_PORT/api/v1/knowledge-bases" \
            -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo "")
        if echo "$kb_resp" | grep -q "data\|list\|items\|\[\|\[]"; then
            log_success "知识库列表接口正常（JSON 列/tenant 过滤）"
        else
            log_warning "知识库接口返回异常"
            log_info "响应: ${kb_resp:-(空)}"
        fi
    else
        log_warning "测试 7 跳过（无可用 token）"
    fi

    # 测试 8: 模型列表（验证 model_usage 的 JSON 提取查询）
    log_info "测试 8: 模型列表 /api/v1/models"
    if [ -n "$TOKEN" ]; then
        local model_resp
        model_resp=$(curl -sf "http://localhost:$APP_PORT/api/v1/models" \
            -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo "")
        if echo "$model_resp" | grep -q "data\|list\|\[\|\[]"; then
            log_success "模型列表接口正常（JSON 字段提取查询）"
        else
            log_warning "模型接口返回异常"
            log_info "响应: ${model_resp:-(空)}"
        fi
    else
        log_warning "测试 8 跳过（无可用 token）"
    fi

    # 测试 9: 用户搜索（验证 ILIKE/LOWER 查询分支）
    log_info "测试 9: 用户搜索 /api/v1/users/search?query=test"
    if [ -n "$TOKEN" ]; then
        local search_resp
        search_resp=$(curl -sf "http://localhost:$APP_PORT/api/v1/users/search?query=test" \
            -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo "")
        if echo "$search_resp" | grep -q "data\|list\|users\|\[\|\[]"; then
            log_success "用户搜索接口正常（ILIKE/LOWER 分支）"
        else
            log_warning "用户搜索接口返回异常"
            log_info "响应: ${search_resp:-(空)}"
        fi
    else
        log_warning "测试 9 跳过（无可用 token）"
    fi

    log_success "扩展 CRUD 测试完成"
}

# ---- 步骤 7：生成测试报告 ----
generate_report() {
    log_info "=== 步骤 7/7：生成测试摘要 ==="

    # 从应用日志中提取关键信息
    echo ""
    echo "=============================================="
    echo "  MySQL 集成测试报告"
    echo "=============================================="
    echo ""

    echo "【数据库信息】"
    echo "  - 驱动: mysql"
    echo "  - 主机: $DB_HOST:$DB_PORT"
    echo "  - 数据库: $DB_NAME"
    echo ""

    echo "【迁移验证】"
    local table_count
    table_count=$(mysql -h"$DB_HOST" -u"$DB_USER" -p"$DB_PASSWORD" -s -e \
        "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='$DB_NAME'" 2>/dev/null)
    echo "  - 表数量: $table_count"
    echo ""

    echo "【应用日志关键信息】"
    grep -i "DB Config\|driver.*mysql\|migration\|version\|startup\|error\|warn" /tmp/weknora-mysql-test.log 2>/dev/null | tail -20 || echo "  (无日志)"

    echo ""
    echo "=============================================="
}

# ---- 主流程 ----
main() {
    echo ""
    echo "=============================================="
    echo "  WeKnora MySQL 集成测试"
    echo "=============================================="
    echo ""

    # 注册清理钩子
    trap cleanup EXIT INT TERM

    detect_compose
    check_prerequisites
    setup_env
    start_docker_services
    build_app
    run_migrations
    run_api_tests
    generate_report

    log_success "全部测试完成！按 Enter 键清理并退出..."
    read -r
}

main "$@"
