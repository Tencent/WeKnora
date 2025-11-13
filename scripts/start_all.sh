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
VERSION="1.0.1" # 版本更新
SCRIPT_NAME=$(basename "$0")

# 显示帮助信息
show_help() {
    printf "%b\n" "${GREEN}WeKnora 启动脚本 v${VERSION}${NC}"
    printf "%b\n" "${GREEN}用法:${NC} $0 [选项]"
    echo "选项:"
    echo "  -h, --help     显示帮助信息"
    echo "  -o, --ollama   启动Ollama服务"
    echo "  -d, --docker   启动Docker容器服务"
    echo "  -a, --all      启动所有服务（默认）"
    echo "  -s, --stop     停止所有服务"
    echo "  -c, --check    检查环境并诊断问题"
    echo "  -r, --restart  重新构建并重启指定容器"
    echo "  -l, --list     列出所有正在运行的容器"
    echo "  -p, --pull     拉取最新的Docker镜像"
    echo "  --no-pull      启动时不拉取镜像（默认会拉取）"
    echo "  -v, --version  显示版本信息"
    exit 0
}

# 显示版本信息
show_version() {
    printf "%b\n" "${GREEN}WeKnora 启动脚本 v${VERSION}${NC}"
    exit 0
}

# 日志函数
log_info() {
    printf "%b\n" "${BLUE}[INFO]${NC} $1"
}

log_warning() {
    printf "%b\n" "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    printf "%b\n" "${RED}[ERROR]${NC} $1"
}

log_success() {
    printf "%b\n" "${GREEN}[SUCCESS]${NC} $1"
}

# 选择可用的 Docker Compose 命令（优先 docker compose，其次 docker-compose）
DOCKER_COMPOSE_BIN=""
DOCKER_COMPOSE_SUBCMD=""

detect_compose_cmd() {
	# 优先使用 Docker Compose 插件
	if docker compose version &> /dev/null; then
		DOCKER_COMPOSE_BIN="docker"
		DOCKER_COMPOSE_SUBCMD="compose"
		return 0
	fi

	# 回退到 docker-compose (v1)
	if command -v docker-compose &> /dev/null; then
		if docker-compose version &> /dev/null; then
			DOCKER_COMPOSE_BIN="docker-compose"
			DOCKER_COMPOSE_SUBCMD=""
			return 0
		fi
	fi

	# 都不可用
	return 1
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
    
    return 0
}

# 安装Ollama（根据平台不同采用不同方法）
install_ollama() {
    # 检查是否为远程服务
    get_ollama_base_url
    
    if [ $IS_REMOTE -eq 1 ]; then
        log_info "检测到远程Ollama服务配置，无需在本地安装Ollama"
        return 0
    fi

    log_info "本地Ollama未安装，正在安装..."
    
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
        log_success "本地Ollama安装完成"
        return 0
    else
        log_error "本地Ollama安装失败"
        return 1
    fi
}

# 获取Ollama基础URL，检查是否为远程服务
get_ollama_base_url() {

    check_env_file

    # 从环境变量获取Ollama基础URL
    OLLAMA_URL=${OLLAMA_BASE_URL:-"http://host.docker.internal:11434"}
    # 提取主机部分
    OLLAMA_HOST=$(echo "$OLLAMA_URL" | sed -E 's|^https?://||' | sed -E 's|:[0-9]+$||' | sed -E 's|/.*$||')
    # 提取端口部分
    OLLAMA_PORT=$(echo "$OLLAMA_URL" | grep -oE ':[0-9]+' | grep -oE '[0-9]+' || echo "11434")
    # 检查是否为localhost或127.0.0.1
    IS_REMOTE=0
    if [ "$OLLAMA_HOST" = "localhost" ] || [ "$OLLAMA_HOST" = "127.0.0.1" ] || [ "$OLLAMA_HOST" = "host.docker.internal" ]; then
        IS_REMOTE=0  # 本地服务
    else
        IS_REMOTE=1  # 远程服务
    fi
}

# 启动Ollama服务
start_ollama() {
    log_info "正在检查Ollama服务..."
    # 提取主机和端口
    get_ollama_base_url
    log_info "Ollama服务地址: $OLLAMA_URL"
    
    if [ $IS_REMOTE -eq 1 ]; then
        log_info "检测到远程Ollama服务，将直接使用远程服务，不进行本地安装和启动"
        # 检查远程服务是否可用
        if curl -s "$OLLAMA_URL/api/tags" &> /dev/null; then
            log_success "远程Ollama服务可访问"
            return 0
        else
            log_warning "远程Ollama服务不可访问，请确认服务地址正确且已启动"
            return 1
        fi
    fi
    
    # 以下为本地服务的处理
    # 检查Ollama是否已安装
    if ! command -v ollama &> /dev/null; then
        install_ollama
        if [ $? -ne 0 ]; then
            return 1
        fi
    fi

    # 检查Ollama服务是否已运行
    if curl -s "http://localhost:$OLLAMA_PORT/api/tags" &> /dev/null; then
        log_success "本地Ollama服务已经在运行，端口：$OLLAMA_PORT"
    else
        log_info "启动本地Ollama服务..."
        # 注意：官方推荐使用 systemctl 或 launchctl 管理服务，直接后台运行仅用于临时场景
        systemctl restart ollama || (ollama serve > /dev/null 2>&1 < /dev/null &)
        
        # 等待服务启动
        MAX_RETRIES=30
        COUNT=0
        while [ $COUNT -lt $MAX_RETRIES ]; do
            if curl -s "http://localhost:$OLLAMA_PORT/api/tags" &> /dev/null; then
                log_success "本地Ollama服务已成功启动，端口：$OLLAMA_PORT"
                break
            fi
            echo -ne "等待Ollama服务启动... ($COUNT/$MAX_RETRIES)\r"
            sleep 1
            COUNT=$((COUNT + 1))
        done
        echo "" # 换行
        
        if [ $COUNT -eq $MAX_RETRIES ]; then
            log_error "本地Ollama服务启动失败"
            return 1
        fi
    fi

    log_success "本地Ollama服务地址: http://localhost:$OLLAMA_PORT"
    return 0
}

# 停止Ollama服务
stop_ollama() {
    log_info "正在停止Ollama服务..."
    
    # 检查是否为远程服务
    get_ollama_base_url
    
    if [ $IS_REMOTE -eq 1 ]; then
        log_info "检测到远程Ollama服务，无需在本地停止"
        return 0
    fi
    
    # 检查Ollama是否已安装
    if ! command -v ollama &> /dev/null; then
        log_info "本地Ollama未安装，无需停止"
        return 0
    fi
    
    # 查找并终止Ollama进程
    if pgrep -x "ollama" > /dev/null; then
        # 优先使用systemctl
        if command -v systemctl &> /dev/null; then
            sudo systemctl stop ollama
        else
            pkill -f "ollama serve"
        fi
        log_success "本地Ollama服务已停止"
    else
        log_info "本地Ollama服务未运行"
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
    
	# 检查并选择可用的 Docker Compose 命令
	if detect_compose_cmd; then
		if [ "$DOCKER_COMPOSE_BIN" = "docker" ]; then
			log_info "已检测到 Docker Compose 插件 (docker compose)"
		else
			log_info "已检测到 docker-compose (v1)"
		fi
	else
		log_error "未检测到 Docker Compose（既没有 docker compose 也没有 docker-compose）。请安装其中之一。"
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

check_platform() {
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
    
    check_platform
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 启动基本服务
    log_info "启动核心服务容器..."
	# 统一通过已检测到的 Compose 命令启动
	if [ "$NO_PULL" = true ]; then
		# 不拉取镜像，使用本地镜像
		log_info "跳过镜像拉取，使用本地镜像..."
		PLATFORM=$PLATFORM "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD up --build -d
	else
		# 拉取最新镜像
		log_info "拉取最新镜像..."
		PLATFORM=$PLATFORM "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD up --pull always -d
	fi
    if [ $? -ne 0 ]; then
        log_error "Docker容器启动失败"
        return 1
    fi
    
    log_success "所有Docker容器已成功启动"
    
    # 显示容器状态
    log_info "当前容器状态:"
	"$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD ps
    
    return 0
}

# 停止Docker容器
stop_docker() {
    log_info "正在停止Docker容器..."
    
    # 检查Docker环境
    check_docker
    if [ $? -ne 0 ]; then
        # 即使检查失败也尝试停止，以防万一
        log_warning "Docker环境检查失败，仍将尝试停止容器..."
    fi
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 停止所有容器
	"$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD down --remove-orphans
    if [ $? -ne 0 ]; then
        log_error "Docker容器停止失败"
        return 1
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
    printf "%b\n" "${BLUE}当前正在运行的容器:${NC}"
	"$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD ps --services | sort
    
    return 0
}

# 拉取最新的Docker镜像
pull_images() {
    log_info "正在拉取最新的Docker镜像..."
    
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
    
    check_platform
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 拉取所有镜像
    log_info "拉取所有服务的最新镜像..."
	PLATFORM=$PLATFORM "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD pull
    if [ $? -ne 0 ]; then
        log_error "镜像拉取失败"
        return 1
    fi
    
    log_success "所有镜像已成功拉取到最新版本"
    
    # 显示拉取的镜像信息
    log_info "已拉取的镜像:"
    docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.CreatedAt}}\t{{.Size}}" | head -10
    
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
    
    check_platform
    
    # 进入项目根目录再执行docker-compose命令
    cd "$PROJECT_ROOT"
    
    # 检查容器是否存在
	if ! "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD ps --services | grep -q "^$container_name$"; then
        log_error "容器 '$container_name' 不存在或未运行"
        echo "可用的容器有:"
        list_containers
        return 1
    fi
    
    # 构建并重启容器
    log_info "正在重新构建容器 '$container_name'..."
	PLATFORM=$PLATFORM "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD build "$container_name"
    if [ $? -ne 0 ]; then
        log_error "容器 '$container_name' 构建失败"
        return 1
    fi
    
    log_info "正在重启容器 '$container_name'..."
	PLATFORM=$PLATFORM "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD up -d --no-deps "$container_name"
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
    
    get_ollama_base_url
    
    if [ $IS_REMOTE -eq 1 ]; then
        log_info "检测到远程Ollama服务配置"
        if curl -s "$OLLAMA_URL/api/tags" &> /dev/null; then
            version=$(curl -s "$OLLAMA_URL/api/tags" | grep -o '"version":"[^"]*"' | cut -d'"' -f4)
            log_success "远程Ollama服务可访问，版本: $version"
        else
            log_warning "远程Ollama服务不可访问，请确认服务地址正确且已启动"
        fi
    else
        if command -v ollama &> /dev/null; then
            log_success "本地Ollama已安装"
            if curl -s "http://localhost:$OLLAMA_PORT/api/tags" &> /dev/null; then
                version=$(curl -s "http://localhost:$OLLAMA_PORT/api/tags" | grep -o '"version":"[^"]*"' | cut -d'"' -f4)
                log_success "本地Ollama服务正在运行，版本: $version"
            else
                log_warning "本地Ollama已安装但服务未运行"
            fi
        else
            log_warning "本地Ollama未安装"
        fi
    fi
    
    # 检查磁盘空间
    log_info "检查磁盘空间..."
    df -h | grep -E "(Filesystem|/$)"
    
    # 检查内存
    log_info "检查内存使用情况..."
    if [ "$OS" = "Darwin" ]; then
        vm_stat | perl -ne '/page size of (\d+)/ and $size=$1; /Pages free:\s*(\d+)/ and print "Free Memory: ", $1 * $size / 1048576, " MB\n"'
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
    if docker info &> /dev/null; then
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
PULL_IMAGES=false
NO_PULL=false
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
        -p | --pull )       PULL_IMAGES=true
                            ;;
        --no-pull )         NO_PULL=true
                            START_OLLAMA=true
                            START_DOCKER=true
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

# 拉取最新镜像
if [ "$PULL_IMAGES" = true ]; then
    pull_images
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
    OLLAMA_RESULT=1
    DOCKER_RESULT=1
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
            printf "%b\n" "${GREEN}  - 前端界面: http://localhost:${FRONTEND_PORT:-80}${NC}"
            printf "%b\n" "${GREEN}  - API接口: http://localhost:${APP_PORT:-8080}${NC}"
            printf "%b\n" "${GREEN}  - Jaeger链路追踪: http://localhost:16686${NC}"
            echo ""
            log_info "正在持续输出容器日志（按 Ctrl+C 退出日志，容器不会停止）..."
            "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD logs app docreader postgres --since=10s -f
        else
            log_error "部分服务启动失败，请检查日志并修复问题"
        fi
    elif [ "$START_OLLAMA" = true ] && [ $OLLAMA_RESULT -eq 0 ]; then
        log_success "Ollama服务启动完成，可通过以下地址访问:"
        printf "%b\n" "${GREEN}  - Ollama API: http://localhost:$OLLAMA_PORT${NC}"
    elif [ "$START_DOCKER" = true ] && [ $DOCKER_RESULT -eq 0 ]; then
        log_success "Docker容器启动完成，可通过以下地址访问:"
        printf "%b\n" "${GREEN}  - 前端界面: http://localhost:${FRONTEND_PORT:-80}${NC}"
        printf "%b\n" "${GREEN}  - API接口: http://localhost:${APP_PORT:-8080}${NC}"
        printf "%b\n" "${GREEN}  - Jaeger链路追踪: http://localhost:16686${NC}"
        echo ""
        log_info "正在持续输出容器日志（按 Ctrl+C 退出日志，容器不会停止）..."
        "$DOCKER_COMPOSE_BIN" $DOCKER_COMPOSE_SUBCMD logs app docreader postgres --since=10s -f
    fi
fi

exit 0