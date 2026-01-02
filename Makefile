.PHONY: help build run test clean docker-build-app docker-build-docreader docker-build-frontend docker-build-all docker-run migrate-up migrate-down docker-restart docker-stop start-all stop-all start-ollama stop-ollama build-images build-images-app build-images-docreader build-images-frontend clean-images check-env list-containers pull-images show-platform dev-start dev-stop dev-restart dev-logs dev-status dev-app dev-frontend docs install-swagger

# Show help
help:
	@echo "WeKnora Makefile 도움말"
	@echo ""
	@echo "기본 명령:"
	@echo "  build             애플리케이션 빌드"
	@echo "  run               애플리케이션 실행"
	@echo "  test              테스트 실행"
	@echo "  clean             빌드 파일 정리"
	@echo ""
	@echo "Docker 명령:"
	@echo "  docker-build-app       애플리케이션 Docker 이미지 빌드 (wechatopenai/weknora-app)"
	@echo "  docker-build-docreader 문서 리더 이미지 빌드 (wechatopenai/weknora-docreader)"
	@echo "  docker-build-frontend  프론트엔드 이미지 빌드 (wechatopenai/weknora-ui)"
	@echo "  docker-build-all       모든 Docker 이미지 빌드"
	@echo "  docker-run            Docker 컨테이너 실행"
	@echo "  docker-stop           Docker 컨테이너 중지"
	@echo "  docker-restart        Docker 컨테이너 재시작"
	@echo ""
	@echo "서비스 관리:"
	@echo "  start-all         모든 서비스 시작"
	@echo "  stop-all          모든 서비스 중지"
	@echo "  start-ollama      Ollama 서비스만 시작"
	@echo ""
	@echo "이미지 빌드:"
	@echo "  build-images      소스에서 모든 이미지 빌드"
	@echo "  build-images-app  소스에서 애플리케이션 이미지 빌드"
	@echo "  build-images-docreader 소스에서 문서 리더 이미지 빌드"
	@echo "  build-images-frontend  소스에서 프론트엔드 이미지 빌드"
	@echo "  clean-images      로컬 이미지 정리"
	@echo ""
	@echo "데이터베이스:"
	@echo "  migrate-up        데이터베이스 마이그레이션 실행"
	@echo "  migrate-down      데이터베이스 마이그레이션 롤백"
	@echo ""
	@echo "개발 도구:"
	@echo "  fmt               코드 포맷팅"
	@echo "  lint              코드 검사"
	@echo "  deps              의존성 설치"
	@echo "  docs              Swagger API 문서 생성"
	@echo "  install-swagger   swag 도구 설치"
	@echo ""
	@echo "환경 검사:"
	@echo "  check-env         환경 구성 검사"
	@echo "  list-containers   실행 중인 컨테이너 목록 표시"
	@echo "  pull-images       최신 이미지 가져오기"
	@echo "  show-platform     현재 빌드 플랫폼 표시"
	@echo ""
	@echo "개발 모드 (권장):"
	@echo "  dev-start         개발 환경 인프라 시작 (의존 서비스만 시작)"
	@echo "  dev-stop          개발 환경 중지"
	@echo "  dev-restart       개발 환경 재시작"
	@echo "  dev-logs          개발 환경 로그 보기"
	@echo "  dev-status        개발 환경 상태 보기"
	@echo "  dev-app           백엔드 애플리케이션 시작 (로컬 실행, dev-start 선행 필요)"
	@echo "  dev-frontend      프론트엔드 시작 (로컬 실행, dev-start 선행 필요)"

# Go related variables
BINARY_NAME=WeKnora
MAIN_PATH=./cmd/server

# Docker related variables
DOCKER_IMAGE=wechatopenai/weknora-app
DOCKER_TAG=latest

# Platform detection
ifeq ($(shell uname -m),x86_64)
    PLATFORM=linux/amd64
else ifeq ($(shell uname -m),aarch64)
    PLATFORM=linux/arm64
else ifeq ($(shell uname -m),arm64)
    PLATFORM=linux/arm64
else
    PLATFORM=linux/amd64
endif

# Build the application
build:
	go build -o $(BINARY_NAME) $(MAIN_PATH)

# Run the application
run: build
	./$(BINARY_NAME)

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	go clean
	rm -f $(BINARY_NAME)

# Build Docker image
docker-build-app:
	@echo "버전 정보 가져오는 중..."
	@eval $$(./scripts/get_version.sh env); \
	./scripts/get_version.sh info; \
	docker build --platform $(PLATFORM) \
		--build-arg VERSION_ARG="$$VERSION" \
		--build-arg COMMIT_ID_ARG="$$COMMIT_ID" \
		--build-arg BUILD_TIME_ARG="$$BUILD_TIME" \
		--build-arg GO_VERSION_ARG="$$GO_VERSION" \
		-f docker/Dockerfile.app -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

# Build docreader Docker image
docker-build-docreader:
	docker build --platform $(PLATFORM) -f docker/Dockerfile.docreader -t wechatopenai/weknora-docreader:latest .

# Build frontend Docker image
docker-build-frontend:
	docker build --platform $(PLATFORM) -f frontend/Dockerfile -t wechatopenai/weknora-ui:latest frontend/

# Build all Docker images
docker-build-all: docker-build-app docker-build-docreader docker-build-frontend

# Run Docker container (전통적인 방식)
docker-run:
	docker-compose up

# 새 스크립트를 사용하여 모든 서비스 시작
start-all:
	./scripts/start_all.sh

# 새 스크립트를 사용하여 Ollama 서비스만 시작
start-ollama:
	./scripts/start_all.sh --ollama

# 새 스크립트를 사용하여 Docker 컨테이너만 시작
start-docker:
	./scripts/start_all.sh --docker

# 새 스크립트를 사용하여 모든 서비스 중지
stop-all:
	./scripts/start_all.sh --stop

# Stop Docker container (전통적인 방식)
docker-stop:
	docker-compose down

# 소스에서 이미지 빌드 관련 명령
build-images:
	./scripts/build_images.sh

build-images-app:
	./scripts/build_images.sh --app

build-images-docreader:
	./scripts/build_images.sh --docreader

build-images-frontend:
	./scripts/build_images.sh --frontend

clean-images:
	./scripts/build_images.sh --clean

# Restart Docker container (stop, start)
docker-restart:
	docker-compose stop -t 60
	docker-compose up

# Database migrations
migrate-up:
	./scripts/migrate.sh up

migrate-down:
	./scripts/migrate.sh down

migrate-version:
	./scripts/migrate.sh version

migrate-create:
	@if [ -z "$(name)" ]; then \
		echo "Error: migration name is required"; \
		echo "Usage: make migrate-create name=your_migration_name"; \
		exit 1; \
	fi
	./scripts/migrate.sh create $(name)

migrate-force:
	@if [ -z "$(version)" ]; then \
		echo "Error: version is required"; \
		echo "Usage: make migrate-force version=4"; \
		exit 1; \
	fi
	./scripts/migrate.sh force $(version)

migrate-goto:
	@if [ -z "$(version)" ]; then \
		echo "Error: version is required"; \
		echo "Usage: make migrate-goto version=3"; \
		exit 1; \
	fi
	./scripts/migrate.sh goto $(version)

# Generate API documentation (Swagger)
docs:
	@echo "Swagger API 문서 생성 중..."
	swag init -g $(MAIN_PATH)/main.go -o ./docs --parseDependency --parseInternal
	@echo "문서가 ./docs 디렉토리에 생성되었습니다"
	@echo "서비스 시작 후 http://localhost:8080/swagger/index.html 방문하여 문서를 확인하세요"

# Install swagger tool
install-swagger:
	go install github.com/swaggo/swag/cmd/swag@latest

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

# Install dependencies
deps:
	go mod download

# Build for production
build-prod:
	VERSION=$${VERSION:-unknown}; \
	COMMIT_ID=$${COMMIT_ID:-unknown}; \
	BUILD_TIME=$${BUILD_TIME:-unknown}; \
	GO_VERSION=$${GO_VERSION:-unknown}; \
	LDFLAGS="-X 'github.com/Tencent/WeKnora/internal/handler.Version=$$VERSION' -X 'github.com/Tencent/WeKnora/internal/handler.CommitID=$$COMMIT_ID' -X 'github.com/Tencent/WeKnora/internal/handler.BuildTime=$$BUILD_TIME' -X 'github.com/Tencent/WeKnora/internal/handler.GoVersion=$$GO_VERSION'"; \
	go build -ldflags="-w -s $$LDFLAGS" -o $(BINARY_NAME) $(MAIN_PATH)

clean-db:
	@echo "Cleaning database..."
	@if [ $$(docker volume ls -q -f name=weknora_postgres-data) ]; then \
		docker volume rm weknora_postgres-data; \
	fi
	@if [ $$(docker volume ls -q -f name=weknora_minio_data) ]; then \
		docker volume rm weknora_minio_data; \
	fi
	@if [ $$(docker volume ls -q -f name=weknora_redis_data) ]; then \
		docker volume rm weknora_redis_data; \
	fi

# Environment check
check-env:
	./scripts/start_all.sh --check

# List containers
list-containers:
	./scripts/start_all.sh --list

# Pull latest images
pull-images:
	./scripts/start_all.sh --pull

# Show current platform
show-platform:
	@echo "현재 시스템 아키텍처: $(shell uname -m)"
	@echo "Docker 빌드 플랫폼: $(PLATFORM)"

# Development mode commands
dev-start:
	./scripts/dev.sh start

dev-stop:
	./scripts/dev.sh stop

dev-restart:
	./scripts/dev.sh restart

dev-logs:
	./scripts/dev.sh logs

dev-status:
	./scripts/dev.sh status

dev-app:
	./scripts/dev.sh app

dev-frontend:
	./scripts/dev.sh frontend
