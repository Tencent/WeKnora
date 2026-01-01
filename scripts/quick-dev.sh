#!/bin/bash
# 빠른 개발 환경 시작 스크립트
# 이 스크립트는 하나의 터미널에서 모든 필수 서비스를 시작합니다.

# 색상 설정
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 프로젝트 루트 디렉토리 가져오기
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
printf "%b\n" "${GREEN}  WeKnora 빠른 개발 환경 시작${NC}"
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

# 프로젝트 루트 디렉토리에 있는지 확인
cd "$PROJECT_ROOT"

# 1. 인프라 시작
log_info "단계 1/3: 인프라 서비스 시작..."
./scripts/dev.sh start
if [ $? -ne 0 ]; then
    log_error "인프라 시작 실패"
    exit 1
fi

# 서비스 준비 대기
log_info "서비스 시작 완료 대기 중..."
sleep 5

# 2. 백엔드 시작 여부 묻기
echo ""
log_info "단계 2/3: 백엔드 애플리케이션 시작"
printf "%b" "${YELLOW}현재 터미널에서 백엔드를 시작하시겠습니까? (y/N): ${NC}"
read -r start_backend

if [ "$start_backend" = "y" ] || [ "$start_backend" = "Y" ]; then
    log_info "백엔드 시작..."
    # 백그라운드에서 백엔드 시작
    nohup bash -c 'cd "'$PROJECT_ROOT'" && ./scripts/dev.sh app' > "$PROJECT_ROOT/logs/backend.log" 2>&1 &
    BACKEND_PID=$!
    echo $BACKEND_PID > "$PROJECT_ROOT/tmp/backend.pid"
    log_success "백엔드가 백그라운드에서 시작되었습니다 (PID: $BACKEND_PID)"
    log_info "백엔드 로그 보기: tail -f $PROJECT_ROOT/logs/backend.log"
else
    log_warning "백엔드 시작 건너뜀"
    log_info "나중에 새 터미널에서 실행: make dev-app 또는 ./scripts/dev.sh app"
fi

# 3. 프론트엔드 시작 여부 묻기
echo ""
log_info "단계 3/3: 프론트엔드 애플리케이션 시작"
printf "%b" "${YELLOW}현재 터미널에서 프론트엔드를 시작하시겠습니까? (y/N): ${NC}"
read -r start_frontend

if [ "$start_frontend" = "y" ] || [ "$start_frontend" = "Y" ]; then
    log_info "프론트엔드 시작..."
    # 백그라운드에서 프론트엔드 시작
    nohup bash -c 'cd "'$PROJECT_ROOT'/frontend" && npm run dev' > "$PROJECT_ROOT/logs/frontend.log" 2>&1 &
    FRONTEND_PID=$!
    echo $FRONTEND_PID > "$PROJECT_ROOT/tmp/frontend.pid"
    log_success "프론트엔드가 백그라운드에서 시작되었습니다 (PID: $FRONTEND_PID)"
    log_info "프론트엔드 로그 보기: tail -f $PROJECT_ROOT/logs/frontend.log"
else
    log_warning "프론트엔드 시작 건너뜀"
    log_info "나중에 새 터미널에서 실행: make dev-frontend 또는 ./scripts/dev.sh frontend"
fi

# 요약 표시
echo ""
printf "%b\n" "${GREEN}========================================${NC}"
printf "%b\n" "${GREEN}  시작 완료!${NC}"
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

log_info "액세스 주소:"
echo "  - 프론트엔드: http://localhost:5173"
echo "  - 백엔드 API: http://localhost:8080"
echo "  - MinIO 콘솔: http://localhost:9001"
echo "  - Jaeger UI: http://localhost:16686"
echo ""

log_info "관리 명령:"
echo "  - 서비스 상태 보기: make dev-status"
echo "  - 로그 보기: make dev-logs"
echo "  - 모든 서비스 중지: make dev-stop"
echo ""

if [ -f "$PROJECT_ROOT/tmp/backend.pid" ] || [ -f "$PROJECT_ROOT/tmp/frontend.pid" ]; then
    log_warning "백그라운드 프로세스 중지:"
    if [ -f "$PROJECT_ROOT/tmp/backend.pid" ]; then
        echo "  - 백엔드 중지: kill \$(cat $PROJECT_ROOT/tmp/backend.pid)"
    fi
    if [ -f "$PROJECT_ROOT/tmp/frontend.pid" ]; then
        echo "  - 프론트엔드 중지: kill \$(cat $PROJECT_ROOT/tmp/frontend.pid)"
    fi
fi

echo ""
log_success "개발 환경이 준비되었습니다. 코딩을 시작하세요!"
echo ""
