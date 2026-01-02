# WeKnora 개발 가이드

## 빠른 개발 모드 (권장)

`app` 또는 `frontend` 코드를 자주 수정해야 하는 경우, **매번 Docker 이미지를 다시 빌드할 필요 없이** 로컬 개발 모드를 사용할 수 있습니다.

### 방법 1: Make 명령어 사용 (권장)

#### 1. 인프라 서비스 시작

```bash
make dev-start
```

이 명령은 다음 서비스의 Docker 컨테이너를 시작합니다:
- PostgreSQL (데이터베이스)
- Redis (캐시)
- MinIO (객체 스토리지)
- Neo4j (그래프 데이터베이스)
- DocReader (문서 읽기 서비스)
- Jaeger (링크 추적)

#### 2. 백엔드 애플리케이션 시작 (새 터미널)

```bash
make dev-app
```

이 명령은 로컬에서 Go 애플리케이션을 직접 실행합니다. 코드를 수정한 후 Ctrl+C로 중지하고 다시 실행하면 됩니다.

#### 3. 프론트엔드 시작 (새 터미널)

```bash
make dev-frontend
```

이 명령은 Vite 개발 서버를 시작하며, 핫 리로딩을 지원하여 코드 수정 후 자동으로 새로 고침됩니다.

#### 4. 서비스 상태 확인

```bash
make dev-status
```

#### 5. 모든 서비스 중지

```bash
make dev-stop
```

### 방법 2: 스크립트 명령어 사용

스크립트를 직접 사용하는 것을 선호하는 경우:

```bash
# 인프라 시작
./scripts/dev.sh start

# 백엔드 시작 (새 터미널)
./scripts/dev.sh app

# 프론트엔드 시작 (새 터미널)
./scripts/dev.sh frontend

# 로그 확인
./scripts/dev.sh logs

# 모든 서비스 중지
./scripts/dev.sh stop
```

## 접속 주소

### 개발 환경

- **프론트엔드 개발 서버**: http://localhost:5173
- **백엔드 API**: http://localhost:8080
- **PostgreSQL**: localhost:5432
- **Redis**: localhost:6379
- **MinIO Console**: http://localhost:9001
- **Neo4j Browser**: http://localhost:7474
- **Jaeger UI**: http://localhost:16686

## 개발 워크플로우 비교

### ❌ 기존 방식 (느림)

```bash
# 코드를 수정할 때마다:
sh scripts/build_images.sh -p      # 이미지 다시 빌드 (매우 느림)
sh scripts/start_all.sh --no-pull  # 컨테이너 재시작
```

**소요 시간**: 수정할 때마다 2-5분

### ✅ 새로운 방식 (빠름)

```bash
# 최초 시작 (한 번만 필요):
make dev-start

# 다른 두 터미널에서 각각 실행:
make dev-app       # Go 코드 수정 후 Ctrl+C로 재시작 (초 단위)
make dev-frontend  # 프론트엔드 코드 수정 시 자동 핫 리로드 (재시작 필요 없음)
```

**소요 시간**:
- 최초 시작: 1-2분
- 이후 백엔드 수정: 5-10초 (Go 앱 재시작)
- 이후 프론트엔드 수정: 실시간 핫 리로드

## Air를 사용한 백엔드 핫 리로드 (선택 사항)

백엔드 코드 수정 후 자동으로 재시작되기를 원한다면 `air`를 설치할 수 있습니다:

### 1. Air 설치

```bash
go install github.com/cosmtrek/air@latest
```

### 2. 구성 파일 생성

프로젝트 루트 디렉터리에 `.air.toml`을 생성합니다:

```toml
root = "."
testdata_dir = "testdata"
tmp_dir = "tmp"

[build]
  args_bin = []
  bin = "./tmp/main"
  cmd = "go build -o ./tmp/main ./cmd/server"
  delay = 1000
  exclude_dir = ["assets", "tmp", "vendor", "testdata", "frontend", "migrations"]
  exclude_file = []
  exclude_regex = ["_test.go"]
  exclude_unchanged = false
  follow_symlink = false
  full_bin = ""
  include_dir = []
  include_ext = ["go", "tpl", "tmpl", "html", "yaml"]
  include_file = []
  kill_delay = "0s"
  log = "build-errors.log"
  poll = false
  poll_interval = 0
  rerun = false
  rerun_delay = 500
  send_interrupt = false
  stop_on_error = false

[color]
  app = ""
  build = "yellow"
  main = "magenta"
  runner = "green"
  watcher = "cyan"

[log]
  main_only = false
  time = false

[misc]
  clean_on_exit = false

[screen]
  clear_on_rebuild = false
  keep_scroll = true
```

### 3. Air로 시작

```bash
# 프로젝트 루트 디렉터리에서
air
```

이제 Go 코드를 수정하면 자동으로 다시 컴파일하고 재시작합니다!

## 기타 개발 팁

### 프론트엔드만 수정

프론트엔드만 수정하는 경우:

```bash
cd frontend
npm run dev
```

프론트엔드는 http://localhost:8080의 백엔드 API에 연결됩니다.

### 백엔드만 수정

백엔드만 수정하는 경우:

```bash
# 인프라 시작
make dev-start

# 백엔드 실행
make dev-app
```

### 디버깅 모드

#### 백엔드 디버깅

VS Code 또는 GoLand의 디버깅 기능을 사용하여 로컬에서 실행 중인 Go 애플리케이션에 연결하도록 구성합니다.

VS Code 구성 예시 (`.vscode/launch.json`):

```json
{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Launch Server",
            "type": "go",
            "request": "launch",
            "mode": "auto",
            "program": "${workspaceFolder}/cmd/server",
            "env": {
                "DB_HOST": "localhost",
                "DOCREADER_ADDR": "localhost:50051",
                "MINIO_ENDPOINT": "localhost:9000",
                "REDIS_ADDR": "localhost:6379",
                "OTEL_EXPORTER_OTLP_ENDPOINT": "localhost:4317",
                "NEO4J_URI": "bolt://localhost:7687"
            },
            "args": []
        }
    ]
}
```

#### 프론트엔드 디버깅

브라우저 개발자 도구를 사용하면 됩니다. Vite는 소스 맵을 제공합니다.

## 프로덕션 환경 배포

개발을 완료하고 배포해야 할 때만 이미지를 빌드하면 됩니다:

```bash
# 모든 이미지 빌드
sh scripts/build_images.sh

# 또는 특정 이미지만 빌드
sh scripts/build_images.sh -p  # 백엔드만 빌드
sh scripts/build_images.sh -f  # 프론트엔드만 빌드

# 프로덕션 환경 시작
sh scripts/start_all.sh
```

## 자주 묻는 질문 (FAQ)

### Q: dev-app 시작 시 데이터베이스에 연결할 수 없다는 오류가 발생합니다.

A: 먼저 `make dev-start`를 실행하고 모든 서비스가 시작될 때까지(약 30초) 기다렸는지 확인하세요.

### Q: 프론트엔드에서 API에 액세스할 때 CORS 오류가 발생합니다.

A: 프론트엔드의 프록시 구성을 확인하고 `vite.config.ts`에 올바른 프록시가 구성되어 있는지 확인하세요.

### Q: DocReader 서비스를 다시 빌드해야 하는 경우 어떻게 해야 하나요?

A: DocReader는 여전히 Docker 이미지를 사용하므로 수정해야 하는 경우 다시 빌드해야 합니다:

```bash
sh scripts/build_images.sh -d
make dev-restart
```

## 요약

- **일상적인 개발**: `make dev-*` 명령어를 사용하여 빠르게 반복
- **테스트 통합**: `sh scripts/start_all.sh --no-pull`을 사용하여 전체 환경 테스트
- **프로덕션 배포**: `sh scripts/build_images.sh` + `sh scripts/start_all.sh` 사용
