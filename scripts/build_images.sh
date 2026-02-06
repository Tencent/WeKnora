#!/bin/bash
# 该脚本用于从源码构建WeKnora的所有Docker镜像

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
    echo -e "${GREEN}WeKnora 镜像构建脚本 v${VERSION}${NC}"
    echo -e "${GREEN}用法:${NC} $0 [选项]"
    echo "选项:"
    echo "  -h, --help     显示帮助信息"
    echo "  -a, --all      构建所有镜像（默认）"
    echo "  -p, --app      仅构建应用镜像"
    echo "  -d, --docreader 仅构建文档读取器镜像"
    echo "  -f, --frontend 仅构建前端镜像"
    echo "  -s, --sandbox  仅构建沙箱镜像"
    echo "  -c, --clean    清理所有本地镜像"
    echo "  -v, --version  显示版本信息"
    exit 0
}

# 显示版本信息
show_version() {
    echo -e "${GREEN}WeKnora 镜像构建脚本 v${VERSION}${NC}"
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

# 检查Docker是否已安装
check_docker() {
    log_info "检查Docker环境..."
    
    if ! command -v docker &> /dev/null; then
        log_error "未安装Docker，请先安装Docker"
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

# 检测平台
check_platform() {
    log_info "检测系统平台信息..."
    if [ "$(uname -m)" = "x86_64" ]; then
        export PLATFORM="linux/amd64"
        export TARGETARCH="amd64"
    elif [ "$(uname -m)" = "aarch64" ] || [ "$(uname -m)" = "arm64" ]; then
        export PLATFORM="linux/arm64"
        export TARGETARCH="arm64"
    else
        log_warning "未识别的平台类型：$(uname -m)，将使用默认平台 linux/amd64"
        export PLATFORM="linux/amd64"
        export TARGETARCH="amd64"
    fi
    log_info "当前平台：$PLATFORM"
    log_info "当前架构：$TARGETARCH"
}

# 获取版本信息
get_version_info() {
    # 从VERSION文件获取版本号
    if [ -f "VERSION" ]; then
        VERSION=$(cat VERSION | tr -d '\n\r')
    else
        VERSION="unknown"
    fi
    
    # 获取commit ID
    if command -v git >/dev/null 2>&1; then
        COMMIT_ID=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    else
        COMMIT_ID="unknown"
    fi
    
    # 获取构建时间
    BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
    
    # 获取Go版本
    if command -v go >/dev/null 2>&1; then
        GO_VERSION=$(go version 2>/dev/null || echo "unknown")
    else
        GO_VERSION="unknown"
    fi
    
    log_info "版本信息: $VERSION"
    log_info "Commit ID: $COMMIT_ID"
    log_info "构建时间: $BUILD_TIME"
    log_info "Go版本: $GO_VERSION"
}

# 构建应用镜像
build_app_image() {
    log_info "构建应用镜像 (weknora-app)..."
    
    cd "$PROJECT_ROOT"
    
    # 获取版本信息
    get_version_info
    
    docker build \
        --platform $PLATFORM \
        --build-arg GOPRIVATE_ARG=${GOPRIVATE:-""} \
        --build-arg GOPROXY_ARG=${GOPROXY:-"https://goproxy.cn,direct"} \
        --build-arg GOSUMDB_ARG=${GOSUMDB:-"off"} \
        --build-arg VERSION_ARG="$VERSION" \
        --build-arg COMMIT_ID_ARG="$COMMIT_ID" \
        --build-arg BUILD_TIME_ARG="$BUILD_TIME" \
        --build-arg GO_VERSION_ARG="$GO_VERSION" \
        -f docker/Dockerfile.app \
        -t wechatopenai/weknora-app:latest \
        .
    
    if [ $? -eq 0 ]; then
        log_success "应用镜像构建成功"
        return 0
    else
        log_error "应用镜像构建失败"
        return 1
    fi
}

# 构建文档读取器镜像
build_docreader_image() {
    log_info "构建文档读取器镜像 (weknora-docreader)..."
    
    cd "$PROJECT_ROOT"
    
    docker build \
        --platform $PLATFORM \
        --build-arg PLATFORM=$PLATFORM \
        --build-arg TARGETARCH=$TARGETARCH \
        -f docker/Dockerfile.docreader \
        -t wechatopenai/weknora-docreader:latest \
        .
    
    if [ $? -eq 0 ]; then
        log_success "文档读取器镜像构建成功"
        return 0
    else
        log_error "文档读取器镜像构建失败"
        return 1
    fi
}

# 构建前端镜像
build_frontend_image() {
    log_info "构建前端镜像 (weknora-ui)..."
    
    cd "$PROJECT_ROOT"
    
    docker build \
        --platform $PLATFORM \
        -f frontend/Dockerfile \
        -t wechatopenai/weknora-ui:latest \
        frontend/
    
    if [ $? -eq 0 ]; then
        log_success "前端镜像构建成功"
        return 0
    else
        log_error "前端镜像构建失败"
        return 1
    fi
}

# 构建沙箱镜像
build_sandbox_image() {
    log_info "构建沙箱镜像 (weknora-sandbox)..."

    cd "$PROJECT_ROOT"

    docker build \
        --platform $PLATFORM \
        -f docker/Dockerfile.sandbox \
        -t wechatopenai/weknora-sandbox:latest \
        .

    if [ $? -eq 0 ]; then
        log_success "沙箱镜像构建成功"
        return 0
    else
        log_error "沙箱镜像构建失败"
        return 1
    fi
}

# 构建所有镜像
build_all_images() {
    log_info "开始构建所有镜像..."

    local app_result=0
    local docreader_result=0
    local frontend_result=0
    local sandbox_result=0

    # 构建应用镜像
    build_app_image
    app_result=$?

    # 构建文档读取器镜像
    build_docreader_image
    docreader_result=$?

    # 构建前端镜像
    build_frontend_image
    frontend_result=$?

    # 构建沙箱镜像
    build_sandbox_image
    sandbox_result=$?

    # 显示构建结果
    echo ""
    log_info "=== 构建结果 ==="
    if [ $app_result -eq 0 ]; then
        log_success "✓ 应用镜像构建成功"
    else
        log_error "✗ 应用镜像构建失败"
    fi

    if [ $docreader_result -eq 0 ]; then
        log_success "✓ 文档读取器镜像构建成功"
    else
        log_error "✗ 文档读取器镜像构建失败"
    fi

    if [ $frontend_result -eq 0 ]; then
        log_success "✓ 前端镜像构建成功"
    else
        log_error "✗ 前端镜像构建失败"
    fi

    if [ $sandbox_result -eq 0 ]; then
        log_success "✓ 沙箱镜像构建成功"
    else
        log_error "✗ 沙箱镜像构建失败"
    fi

    if [ $app_result -eq 0 ] && [ $docreader_result -eq 0 ] && [ $frontend_result -eq 0 ] && [ $sandbox_result -eq 0 ]; then
        log_success "所有镜像构建完成！"
        return 0
    else
        log_error "部分镜像构建失败"
        return 1
    fi
}

# 清理本地镜像
clean_images() {
    log_info "清理本地WeKnora镜像..."
    
    # 停止相关容器
    log_info "停止相关容器..."
    docker stop $(docker ps -q --filter "ancestor=wechatopenai/weknora-app:latest" 2>/dev/null) 2>/dev/null || true
    docker stop $(docker ps -q --filter "ancestor=wechatopenai/weknora-docreader:latest" 2>/dev/null) 2>/dev/null || true
    docker stop $(docker ps -q --filter "ancestor=wechatopenai/weknora-ui:latest" 2>/dev/null) 2>/dev/null || true
    
    # 删除相关容器
    log_info "删除相关容器..."
    docker rm $(docker ps -aq --filter "ancestor=wechatopenai/weknora-app:latest" 2>/dev/null) 2>/dev/null || true
    docker rm $(docker ps -aq --filter "ancestor=wechatopenai/weknora-docreader:latest" 2>/dev/null) 2>/dev/null || true
    docker rm $(docker ps -aq --filter "ancestor=wechatopenai/weknora-ui:latest" 2>/dev/null) 2>/dev/null || true
    
    # 删除镜像
    log_info "删除本地镜像..."
    docker rmi wechatopenai/weknora-app:latest 2>/dev/null || true
    docker rmi wechatopenai/weknora-docreader:latest 2>/dev/null || true
    docker rmi wechatopenai/weknora-ui:latest 2>/dev/null || true
    docker rmi wechatopenai/weknora-sandbox:latest 2>/dev/null || true
    
    docker image prune -f
    
    log_success "镜像清理完成"
    return 0
}

# 解析命令行参数
BUILD_ALL=false
BUILD_APP=false
BUILD_DOCREADER=false
BUILD_FRONTEND=false
BUILD_SANDBOX=false
CLEAN_IMAGES=false

# 没有参数时默认构建所有镜像
if [ $# -eq 0 ]; then
    BUILD_ALL=true
fi

while [ "$1" != "" ]; do
    case $1 in
        -h | --help )       show_help
                            ;;
        -a | --all )        BUILD_ALL=true
                            ;;
        -p | --app )        BUILD_APP=true
                            ;;
        -d | --docreader )  BUILD_DOCREADER=true
                            ;;
        -f | --frontend )   BUILD_FRONTEND=true
                            ;;
        -s | --sandbox )    BUILD_SANDBOX=true
                            ;;
        -c | --clean )      CLEAN_IMAGES=true
                            ;;
        -v | --version )    show_version
                            ;;
        * )                 log_error "未知选项: $1"
                            show_help
                            ;;
    esac
    shift
done

# 检查Docker环境
check_docker
if [ $? -ne 0 ]; then
    exit 1
fi

# 检测平台
check_platform

# 执行清理操作
if [ "$CLEAN_IMAGES" = true ]; then
    clean_images
    exit $?
fi

# 执行构建操作
if [ "$BUILD_ALL" = true ]; then
    build_all_images
    exit $?
fi

if [ "$BUILD_APP" = true ]; then
    build_app_image
    exit $?
fi

if [ "$BUILD_DOCREADER" = true ]; then
    build_docreader_image
    exit $?
fi

if [ "$BUILD_FRONTEND" = true ]; then
    build_frontend_image
    exit $?
fi

if [ "$BUILD_SANDBOX" = true ]; then
    build_sandbox_image
    exit $?
fi

exit 0