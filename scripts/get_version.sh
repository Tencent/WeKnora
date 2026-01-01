#!/bin/bash
# 통합 버전 정보 가져오기 스크립트
# 로컬 빌드 및 CI 빌드 환경 지원

# 기본값 설정
VERSION="unknown"
COMMIT_ID="unknown"
BUILD_TIME="unknown"
GO_VERSION="unknown"

# 버전 번호 가져오기
if [ -f "VERSION" ]; then
    VERSION=$(cat VERSION | tr -d '\n\r')
fi

# commit ID 가져오기
if [ -n "$GITHUB_SHA" ]; then
    # GitHub Actions 환경
    COMMIT_ID="${GITHUB_SHA:0:7}"
elif command -v git >/dev/null 2>&1; then
    # 로컬 환경
    COMMIT_ID=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
fi

# 빌드 시간 가져오기
if [ -n "$GITHUB_ACTIONS" ]; then
    # GitHub Actions 환경, 표준 시간 형식 사용
    BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
else
    # 로컬 환경
    BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
fi

# Go 버전 가져오기
if command -v go >/dev/null 2>&1; then
    GO_VERSION=$(go version 2>/dev/null || echo "unknown")
fi

# 매개변수에 따라 다른 형식 출력
case "${1:-env}" in
    "env")
        # 환경 변수 형식 출력, 공백을 포함한 값은 이스케이프 처리
        echo "VERSION=$VERSION"
        echo "COMMIT_ID=$COMMIT_ID"
        echo "BUILD_TIME=\"$BUILD_TIME\""
        echo "GO_VERSION=\"$GO_VERSION\""
        ;;
    "json")
        # JSON 형식 출력
        cat << EOF
{
  "version": "$VERSION",
  "commit_id": "$COMMIT_ID",
  "build_time": "$BUILD_TIME",
  "go_version": "$GO_VERSION"
}
EOF
        ;;
    "docker-args")
        # Docker 빌드 매개변수 형식 출력
        echo "--build-arg VERSION_ARG=$VERSION"
        echo "--build-arg COMMIT_ID_ARG=$COMMIT_ID"
        echo "--build-arg BUILD_TIME_ARG=$BUILD_TIME"
        echo "--build-arg GO_VERSION_ARG=$GO_VERSION"
        ;;
    "ldflags")
        # Go ldflags 형식 출력
        echo "-X 'github.com/Tencent/WeKnora/internal/handler.Version=$VERSION' -X 'github.com/Tencent/WeKnora/internal/handler.CommitID=$COMMIT_ID' -X 'github.com/Tencent/WeKnora/internal/handler.BuildTime=$BUILD_TIME' -X 'github.com/Tencent/WeKnora/internal/handler.GoVersion=$GO_VERSION'"
        ;;
    "info")
        # 정보 형식 출력
        echo "버전 정보: $VERSION"
        echo "Commit ID: $COMMIT_ID"
        echo "빌드 시간: $BUILD_TIME"
        echo "Go 버전: $GO_VERSION"
        ;;
    *)
        echo "사용법: $0 [env|json|docker-args|ldflags|info]"
        echo "  env        - 환경 변수 형식 출력 (기본값)"
        echo "  json       - JSON 형식 출력"
        echo "  docker-args - Docker 빌드 매개변수 형식 출력"
        echo "  ldflags    - Go ldflags 형식 출력"
        echo "  info       - 정보 형식 출력"
        exit 1
        ;;
esac
