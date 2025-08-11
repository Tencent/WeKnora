#!/bin/bash
# 该脚本用于按需启动/停止Ollama和docker-compose服务

# 设置颜色
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # 无颜色

# 获取项目根目录（脚本所在目录的上一级）
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

# 版本信息
VERSION="1.0.0"
SCRIPT_NAME=$(basename "$0")

# 显示帮助信息
show_help() {
    echo -e "${GREEN}WeKnora 启动脚本 v${VERSION}${NC}"
    echo -e "${GREEN}用法:${NC} $0 [选项]"
    echo "选项:"
    echo "  -h, --help     显示帮助信息"
    echo "  -o, --ollama   启动Ollama服务"
    echo "  -d, --docker   启动Docker容器服务"
    echo "  -a, --all      启动所有服务（默认）"
    echo "  -s, --stop     停止所有服务"
    echo "  -c, --check    检查环境并诊断问题"
    echo "  -r, --restart  重新构建并重启指定容器"
    echo "  -l, --list     列出所有正在运行的容器"
    echo "  -v, --version  显示版本信息"
    exit 0
}

# 显示版本信息
show_version() {
    echo -e "${GREEN}WeKnora 启动脚本 v${VERSION}${NC}"
    exit 0
}

# 日志函数
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

# 检查并创建.env文件
check_env_file() {
    log_info "检查环境变量配置..."
    if [ ! -f "$PROJECT_ROOT/.env" ]; then
        log_warning ".env 文件不存在，将从模板创建"
        if [ -f "$PROJECT_ROOT/.env.example" ]; then
            cp "$PROJECT_ROOT/.env.example" "$PROJECT_ROOT/.env"
            log_success "已从 .env.example 创建 .env 文件"
        else
            log_error "未找到 .env.example 模板文件，无法创建 .env 文件"
            return 1
        fi
    else
        log_info ".env 文件已存在"
    fi
    
    # 检查必要的环境变量是否已设置
    source "$PROJECT_ROOT/.env"
    local missing_vars=()
    
    # 检查基础变量
    if [ -z "$DB_DRIVER" ]; then missing_vars+=("DB_DRIVER"); fi
    if [ -z "$STORAGE_TYPE" ]; then missing_vars+=("STORAGE_TYPE"); fi
    
    if [ ${#missing_vars[@]} -gt 0 ]; then
        log_warning "以下环境变量未设置，将使用默认值: ${missing_vars[*]}"
    else
        log_success "所有必要的环境变量已设置"
    fi
    
    return 0
}

# 安装Ollama（根据平台不同采用不同方法）
install_ollama() {
    log_info "Ollama未安装，正在安装..."
    
    OS=$(uname)
    if [ "$OS" = "Darwin" ]; then
        # Mac安装方式
        log_info "检测到Mac系统，使用brew安装Ollama..."
        if ! command -v brew &> /dev/null; then
            # 通过安装包安装
            log_info "Homebrew未安装，使用直接下载方式..."
            curl -fsSL https://ollama.com/download/Ollama-darwin.zip -o ollama.zip
            unzip ollama.zip
            mv ollama /usr/local/bin
            rm ollama.zip
        else
            brew install ollama
        fi
    else
        # Linux安装方式
        log_info "检测到Linux系统，使用安装脚本..."
        curl -fsSL https://ollama.com/install.sh | sh
    fi
    
    if [ $? -eq 0 ]; then
        log_success "Ollama安装完成"
        return 0
    else
        log_error "Ollama安装失败"
        return 1
    fi
}

# 启动Ollama服务
start_ollama() {
    log_info "正在检查Ollama服务..."
    
    # 检查Ollama是否已安装
    if ! command -v ollama &> /dev/null; then
        install_ollama
        if [ $? -ne 0 ]; then
            return 1
        fi
    fi

    # 检查Ollama服务是否已运行
    if curl -s http://localhost:11435/api/version &> /dev/null; then
        log_success "Ollama服务已经在运行"
    else
        log_info "启动Ollama服务..."
        export OLLAMA_HOST=0.0.0.0:11435
        ollama serve >/dev/null 2>&1 &
        
        # 等待服务启动
        MAX_RETRIES=30
        COUNT=0
        while [ $COUNT -lt $MAX_RETRIES ]; do
            if curl -s http://localhost:11435/api/version &> /dev/null; then
                log_success "Ollama服务已成功启动"
                break
            fi
            echo "等待Ollama服务启动... ($COUNT/$MAX_RETRIES)"
            sleep 1
            COUNT=$((COUNT + 1))
        done
        
        if [ $COUNT -eq $MAX_RETRIES ]; then
            log_error "Ollama服务启动失败"
            return 1
        fi
    fi

    log_success "Ollama服务地址: http://localhost:11435"
    return 0
}

# 停止Ollama服务
stop_ollama() {
    log_info "正在停止Ollama服务..."
    
    # 检查Ollama是否已安装
    if ! command -v ollama &> /dev/null; then
        log_info "Ollama未安装，无需停止"
        return 0
    fi
    
    # 查找并终止Ollama进程
    if pgrep -x "ollama" > /dev/null; then
        pkill -f "ollama serve"
        log_success "Ollama服务已停止"
    else
        log_info "Ollama服务未运行"
    fi
    
    return 0
}

# 检查Docker是否已安装
check_docker() {
    log_info "检查Docker环境..."
    
    if ! command -v docker &> /dev/null; then
        log_error "未安装Docker，请先安装Docker"
        return 1
    fi
    
    if ! command -v docker-compose &> /dev/null; then
        log_error "未安装docker-compose，请先安装docker-compose"
        return 1
    fi
    
    # 检查Docker服务运行状态
    if ! docker info &> /dev/null; then
        log_error "Docker服务未运行，请启动Docker服务"
        return 1
    fi
    
    log_success "Docker环境检查通过"
    return 0
}

# 启动Docker容器
start_docker() {
    log_info "正在启动Docker容器..."
    
    # 检查Docker环境
    check_docker
    if [ $? -ne 0 ]; then
        return 1
    fi
    
    # 检查.env文件
    check_env_file
    
    # 读取.env文件
    source "$PROJECT_ROOT/.env"
    storage_type=${STORAGE_TYPE:-local}

    # 检测当前系统平台
    log_info "检测系统平台信息..."
    if [ "$(uname -m)" = "x86_64" ]; then
        export PLATFORM="linux/amd64"
    elif [ "$(uname -m)" = "aarch64" ] || [ "$(uname -m)" = "arm64" ]; then
        export PLATFORM="linux/arm64"
    else
        log_warning "未识别的平台类型：$(uname -m)，将使用默认平台 linux/amd64"
        export PLATFORM="linux/amd64"
    fi
    log_info "当前平台：$PLATFORM"
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 启动基本服务
    log_info "启动核心服务容器..."
    PLATFORM=$PLATFORM docker-compose up --build -d
    if [ $? -ne 0 ]; then
        log_error "Docker容器启动失败"
        return 1
    fi
    
    # 如果存储类型是minio，则启动MinIO服务
    if [ "$storage_type" == "minio" ]; then
        log_info "检测到MinIO存储配置，启动MinIO服务..."
        docker-compose -f ./docker/docker-compose.minio.yml up --build -d
        if [ $? -ne 0 ]; then
            log_error "MinIO服务启动失败"
            return 1
        fi
        log_success "MinIO服务已启动"
    else
        log_info "使用本地存储，不启动MinIO服务"
    fi
    
    log_success "所有Docker容器已成功启动"
    
    # 显示容器状态
    log_info "当前容器状态:"
    docker-compose ps
    
    return 0
}

# 停止Docker容器
stop_docker() {
    log_info "正在停止Docker容器..."
    
    # 检查Docker环境
    check_docker
    if [ $? -ne 0 ]; then
        return 1
    fi
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 停止所有容器
    docker-compose down --remove-orphans
    if [ $? -ne 0 ]; then
        log_error "Docker容器停止失败"
        return 1
    fi
    
    # 如果存在minio配置，也停止minio
    if [ -f "$PROJECT_ROOT/docker/docker-compose.minio.yml" ]; then
        docker-compose -f "./docker/docker-compose.minio.yml" down
    fi
    
    log_success "所有Docker容器已停止"
    return 0
}

# 列出所有正在运行的容器
list_containers() {
    log_info "列出所有正在运行的容器..."
    
    # 检查Docker环境
    check_docker
    if [ $? -ne 0 ]; then
        return 1
    fi
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 列出所有容器
    echo -e "${BLUE}当前正在运行的容器:${NC}"
    docker-compose ps --services | sort
    
    return 0
}

# 重启指定容器
restart_container() {
    local container_name="$1"
    
    if [ -z "$container_name" ]; then
        log_error "未指定容器名称"
        echo "可用的容器有:"
        list_containers
        return 1
    fi
    
    log_info "正在重新构建并重启容器: $container_name"
    
    # 检查Docker环境
    check_docker
    if [ $? -ne 0 ]; then
        return 1
    fi
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 检查容器是否存在
    if ! docker-compose ps --services | grep -q "^$container_name$"; then
        log_error "容器 '$container_name' 不存在或未运行"
        echo "可用的容器有:"
        list_containers
        return 1
    fi
    
    # 构建并重启容器
    log_info "正在重新构建容器 '$container_name'..."
    docker-compose build "$container_name"
    if [ $? -ne 0 ]; then
        log_error "容器 '$container_name' 构建失败"
        return 1
    fi
    
    log_info "正在重启容器 '$container_name'..."
    docker-compose up -d --no-deps "$container_name"
    if [ $? -ne 0 ]; then
        log_error "容器 '$container_name' 重启失败"
        return 1
    fi
    
    log_success "容器 '$container_name' 已成功重新构建并重启"
    return 0
}

# 检查系统环境
check_environment() {
    log_info "开始环境检查..."
    
    # 检查操作系统
    OS=$(uname)
    log_info "操作系统: $OS"
    
    # 检查Docker
    check_docker
    
    # 检查.env文件
    check_env_file
    
    # 检查Ollama
    if command -v ollama &> /dev/null; then
        log_success "Ollama已安装"
        if curl -s http://localhost:11435/api/version &> /dev/null; then
            version=$(curl -s http://localhost:11435/api/version | grep -o '"version":"[^"]*"' | cut -d'"' -f4)
            log_success "Ollama服务正在运行，版本: $version"
        else
            log_warning "Ollama已安装但服务未运行"
        fi
    else
        log_warning "Ollama未安装"
    fi
    
    # 检查磁盘空间
    log_info "检查磁盘空间..."
    df -h | grep -E "(Filesystem|/$)"
    
    # 检查内存
    log_info "检查内存使用情况..."
    if [ "$OS" = "Darwin" ]; then
        vm_stat | perl -ne '/page size of (\d+)/ and $size=$1; /Pages free: (\d+)/ and print "Free Memory: ", $1 * $size / 1048576, " MB\n"'
    else
        free -h | grep -E "(total|Mem:)"
    fi
    
    # 检查CPU
    log_info "CPU信息:"
    if [ "$OS" = "Darwin" ]; then
        sysctl -n machdep.cpu.brand_string
        echo "CPU核心数: $(sysctl -n hw.ncpu)"
    else
        grep "model name" /proc/cpuinfo | head -1
        echo "CPU核心数: $(nproc)"
    fi
    
    # 检查容器状态
    log_info "检查容器状态..."
    if docker ps &> /dev/null; then
        docker ps -a
    else
        log_warning "无法获取容器状态，Docker可能未运行"
    fi
    
    log_success "环境检查完成"
    return 0
}

# 解析命令行参数
START_OLLAMA=false
START_DOCKER=false
STOP_SERVICES=false
CHECK_ENVIRONMENT=false
LIST_CONTAINERS=false
RESTART_CONTAINER=false
CONTAINER_NAME=""

# 没有参数时默认启动所有服务
if [ $# -eq 0 ]; then
    START_OLLAMA=true
    START_DOCKER=true
fi

while [ "$1" != "" ]; do
    case $1 in
        -h | --help )       show_help
                            ;;
        -o | --ollama )     START_OLLAMA=true
                            ;;
        -d | --docker )     START_DOCKER=true
                            ;;
        -a | --all )        START_OLLAMA=true
                            START_DOCKER=true
                            ;;
        -s | --stop )       STOP_SERVICES=true
                            ;;
        -c | --check )      CHECK_ENVIRONMENT=true
                            ;;
        -l | --list )       LIST_CONTAINERS=true
                            ;;
        -r | --restart )    RESTART_CONTAINER=true
                            CONTAINER_NAME="$2"
                            shift
                            ;;
        -v | --version )    show_version
                            ;;
        * )                 log_error "未知选项: $1"
                            show_help
                            ;;
    esac
    shift
done

# 执行环境检查
if [ "$CHECK_ENVIRONMENT" = true ]; then
    check_environment
    exit $?
fi

# 列出所有容器
if [ "$LIST_CONTAINERS" = true ]; then
    list_containers
    exit $?
fi

# 重启指定容器
if [ "$RESTART_CONTAINER" = true ]; then
    restart_container "$CONTAINER_NAME"
    exit $?
fi

# 执行服务操作
if [ "$STOP_SERVICES" = true ]; then
    # 停止服务
    stop_ollama
    OLLAMA_RESULT=$?
    
    stop_docker
    DOCKER_RESULT=$?
    
    # 显示总结
    echo ""
    log_info "=== 停止结果 ==="
    if [ $OLLAMA_RESULT -eq 0 ]; then
        log_success "✓ Ollama服务已停止"
    else
        log_error "✗ Ollama服务停止失败"
    fi
    
    if [ $DOCKER_RESULT -eq 0 ]; then
        log_success "✓ Docker容器已停止"
    else
        log_error "✗ Docker容器停止失败"
    fi
    
    log_success "服务停止完成。"
else
    # 启动服务
    if [ "$START_OLLAMA" = true ]; then
        start_ollama
        OLLAMA_RESULT=$?
    fi
    
    if [ "$START_DOCKER" = true ]; then
        start_docker
        DOCKER_RESULT=$?
    fi
    
    # 显示总结
    echo ""
    log_info "=== 启动结果 ==="
    if [ "$START_OLLAMA" = true ]; then
        if [ $OLLAMA_RESULT -eq 0 ]; then
            log_success "✓ Ollama服务已启动"
        else
            log_error "✗ Ollama服务启动失败"
        fi
    fi
    
    if [ "$START_DOCKER" = true ]; then
        if [ $DOCKER_RESULT -eq 0 ]; then
            log_success "✓ Docker容器已启动"
        else
            log_error "✗ Docker容器启动失败"
        fi
    fi
    
    if [ "$START_OLLAMA" = true ] && [ "$START_DOCKER" = true ]; then
        if [ $OLLAMA_RESULT -eq 0 ] && [ $DOCKER_RESULT -eq 0 ]; then
            log_success "所有服务启动完成，可通过以下地址访问:"
            echo -e "${GREEN}  - 前端界面: http://localhost${NC}"
            echo -e "${GREEN}  - API接口: http://localhost:8080${NC}"
            echo -e "${GREEN}  - Jaeger链路追踪: http://localhost:16686${NC}"
        else
            log_error "部分服务启动失败，请检查日志并修复问题"
        fi
    elif [ "$START_OLLAMA" = true ] && [ $OLLAMA_RESULT -eq 0 ]; then
        log_success "Ollama服务启动完成，可通过以下地址访问:"
        echo -e "${GREEN}  - Ollama API: http://localhost:11435${NC}"
    elif [ "$START_DOCKER" = true ] && [ $DOCKER_RESULT -eq 0 ]; then
        log_success "Docker容器启动完成，可通过以下地址访问:"
        echo -e "${GREEN}  - 前端界面: http://localhost${NC}"
        echo -e "${GREEN}  - API接口: http://localhost:8080${NC}"
        echo -e "${GREEN}  - Jaeger链路追踪: http://localhost:16686${NC}"
    fi
fi

exit 0 