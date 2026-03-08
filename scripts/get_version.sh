#!/bin/bash
# 统一的版本信息获取脚本
# 支持本地构建和CI构建环境

# 设置默认值
VERSION="unknown"
EDITION="${EDITION:-standard}"
COMMIT_ID="unknown"
BUILD_TIME="unknown"
GO_VERSION="unknown"

# 获取版本号
if [ -f "VERSION" ]; then
    VERSION=$(cat VERSION | tr -d '\n\r')
fi

# 获取commit ID
if [ -n "$GITHUB_SHA" ]; then
    # GitHub Actions环境
    COMMIT_ID="${GITHUB_SHA:0:7}"
elif command -v git >/dev/null 2>&1; then
    # 本地环境
    COMMIT_ID=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
fi

# 获取构建时间
if [ -n "$GITHUB_ACTIONS" ]; then
    # GitHub Actions环境，使用标准时间格式
    BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
else
    # 本地环境
    BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
fi

# 获取Go版本
if command -v go >/dev/null 2>&1; then
    GO_VERSION=$(go version 2>/dev/null || echo "unknown")
fi

# 根据参数输出不同格式
case "${1:-env}" in
    "env")
        # 输出环境变量格式，对包含空格的值进行转义
        echo "VERSION=$VERSION"
        echo "EDITION=$EDITION"
        echo "COMMIT_ID=$COMMIT_ID"
        echo "BUILD_TIME=\"$BUILD_TIME\""
        echo "GO_VERSION=\"$GO_VERSION\""
        ;;
    "json")
        # 输出JSON格式
        cat << EOF
{
  "version": "$VERSION",
  "edition": "$EDITION",
  "commit_id": "$COMMIT_ID",
  "build_time": "$BUILD_TIME",
  "go_version": "$GO_VERSION"
}
EOF
        ;;
    "docker-args")
        # 输出Docker构建参数格式
        echo "--build-arg VERSION_ARG=$VERSION"
        echo "--build-arg COMMIT_ID_ARG=$COMMIT_ID"
        echo "--build-arg BUILD_TIME_ARG=$BUILD_TIME"
        echo "--build-arg GO_VERSION_ARG=$GO_VERSION"
        ;;
    "ldflags")
        # 输出Go ldflags格式
        echo "-X 'github.com/Tencent/WeKnora/internal/handler.Version=$VERSION' -X 'github.com/Tencent/WeKnora/internal/handler.Edition=$EDITION' -X 'github.com/Tencent/WeKnora/internal/handler.CommitID=$COMMIT_ID' -X 'github.com/Tencent/WeKnora/internal/handler.BuildTime=$BUILD_TIME' -X 'github.com/Tencent/WeKnora/internal/handler.GoVersion=$GO_VERSION'"
        ;;
    "info")
        # 输出信息格式
        echo "版本信息: $VERSION"
        echo "版本类型: $EDITION"
        echo "Commit ID: $COMMIT_ID"
        echo "构建时间: $BUILD_TIME"
        echo "Go版本: $GO_VERSION"
        ;;
    *)
        echo "用法: $0 [env|json|docker-args|ldflags|info]"
        echo "  env        - 输出环境变量格式 (默认)"
        echo "  json       - 输出JSON格式"
        echo "  docker-args - 输出Docker构建参数格式"
        echo "  ldflags    - 输出Go ldflags格式"
        echo "  info       - 输出信息格式"
        exit 1
        ;;
esac
