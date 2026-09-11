# 开发指南

开发环境使用 `docker-compose.dev.yml` 启动依赖服务，并支持独立运行 Go 后端、Web 前端和 docreader。代码修改后按所在模块执行构建与测试，再检查相关集成路径。

## 技术栈与环境要求 {#_1-技术栈与环境要求}

WeKnora 由三个可独立开发的进程组成：

| 组件 | 目录 | 语言 / 运行时 | 版本要求（来源） |
| --- | --- | --- | --- |
| 主后端 `app` | `cmd/server` + `internal/` | Go | **Go 1.26.0**（`go.mod` 中 `go 1.26.0`），需 CGO（DuckDB、sqlite-vec 绑定） |
| 文档解析服务 `docreader` | `docreader/` | Python + gRPC | **Python >= 3.10.18**（`docreader/pyproject.toml` 中 `requires-python`），依赖用 **uv** 管理（仓库含 `uv.lock`，Docker 内 `uv sync --locked`） |
| 前端 `frontend` | `frontend/` | Node.js + Vue 3 | Node 22 系（`devDependencies` 含 `@tsconfig/node22`、`@types/node ^22`），Vite 7 + TypeScript ~6.0 + Vue 3.5 + TDesign，版本号 `0.8.0` |
| CLI | `cli/`（独立 Go module） | Go | Go 1.26（`.github/workflows/cli.yml` 矩阵 `go: ['1.26']`） |

推荐额外安装的开发工具：

```bash
# 数据库迁移 CLI（scripts/migrate.sh 依赖）
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# 代码检查（make lint 调用）
# 安装方式见 https://golangci-lint.run；仓库根有 .golangci.yml 配置

# Swagger 文档生成（make docs 调用）
make install-swagger    # go install github.com/swaggo/swag/cmd/swag@latest

# Python 依赖管理
pip install uv          # docreader 使用 uv sync 安装依赖

# Docker + Docker Compose（v2 插件或独立 docker-compose 均可，scripts/dev.sh 自动探测）
```

## 快速开始：开发模式（推荐） {#_2-快速开始-开发模式-推荐}

开发模式在 Docker 中运行基础设施，在本地运行 `app` 和 `frontend`。修改应用代码后可直接重启进程，无需重建依赖镜像。入口为 `scripts/dev.sh`，Makefile 的 `dev-*` 目标封装了相应操作。

```bash
# 1. 准备环境变量：dev.sh 会加载 .env（必须存在），再用 .env.local 覆盖（可选）
cp .env.example .env

# 2. 启动基础设施（ParadeDB/Postgres + Redis + docreader，默认还带 Langfuse）
make dev-start                      # 等价 ./scripts/dev.sh start
make dev-start DEV_ARGS=--qdrant    # 附加可选 profile

# 3. 另开终端：本地跑后端（内部执行 go run -ldflags=... ./cmd/server）
make dev-app                        # 等价 ./scripts/dev.sh app

# 4. 再开终端：本地跑前端（cd frontend && npm install && npm run dev）
make dev-frontend                   # 等价 ./scripts/dev.sh frontend

# 其他
make dev-status   # 查看容器状态
make dev-logs     # 查看日志
make dev-stop     # 停止
make dev-restart  # 重启
```

前端 dev server 监听 `5173`（`frontend/vite.config.ts` 中 `server.port: 5173`），并把 `/api` 与 `/files` 代理到本地后端（`DEV_PROXY_TARGET`）。`vite preview`（端口 `4173`）用生产构建产物起服务，是最接近 release 镜像的验证环境。

### docker-compose.dev.yml 服务清单 {#_2-1-docker-compose-dev-yml-服务清单}

`docker-compose.dev.yml` 只包含依赖服务，不含 `app`/`frontend`。默认启动与 profile 可选服务如下（profile 通过 `dev.sh start` 的参数开启）：

| 服务 | 镜像 | 端口（默认） | 启动条件 |
| --- | --- | --- | --- |
| `postgres` | `paradedb/paradedb:v0.22.2-pg17`（自带 pg_search/BM25） | `5432` | 默认启动 |
| `redis` | `redis:7.0-alpine`（`--requirepass`） | `6379` | 默认启动 |
| `docreader` | 本地构建 `docker/Dockerfile.docreader` | `50051`（gRPC） | 默认启动 |
| `searxng`（+`searxng-init`） | `searxng/searxng:latest` | `127.0.0.1:8888` | `--searxng` / `--full`（compose profile `searxng`） |
| `minio` | `minio/minio:latest` | `9000` / 控制台 `9001` | `--minio` / `--full` |
| `qdrant` | `qdrant/qdrant:v1.16.2` | `6333` / `6334` | `--qdrant` / `--full` |
| `opensearch` | `opensearchproject/opensearch:3.3.2`（关闭 security，纯 HTTP） | `9200` | profile `opensearch` / `full` |
| `opensearch-dashboards` | `opensearchproject/opensearch-dashboards:3.3.0` | `5601` | profile `opensearch-ui`（按需单独启动） |
| `milvus` | `milvusdb/milvus:v2.6.11`（standalone，内嵌 etcd） | `19530` / `9091` | profile `milvus` / `full` |
| `neo4j` | `neo4j:latest`（APOC 插件） | `7474` / `7687` | `--neo4j` / `--full` |
| `dex` | `dexidp/dex:latest`（OIDC 测试身份源，配置 `misc/dex-config.yaml`） | `5556` | `--dex` / `--full` |
| `langfuse-web` / `langfuse-worker` / `langfuse-clickhouse` / `langfuse-minio` / `langfuse-db-init` | Langfuse v3 自建栈，复用 dev 的 postgres（独立 `langfuse` 库）与 redis（DB 1） | web `3000`、minio `9100/9101` | `--langfuse`（`dev.sh` 默认开启，`--no-langfuse` 关闭） |
| `odl-hybrid` | 本地构建 `docker/Dockerfile.odl-hybrid`（Docling PDF 后端） | `5002` | `--odl-hybrid`（镜像较大，按需） |
| `sandbox` | `wechatopenai/weknora-sandbox`（Skills 脚本执行沙箱，仅 build/pull，非常驻） | - | profile `full` |

`dev.sh start` 的可选参数：`--minio`、`--qdrant`、`--neo4j`、`--dex`、`--langfuse`（默认开）、`--no-langfuse`、`--odl-hybrid`、`--full`（全部可选服务，不含 odl-hybrid）。通过 Makefile 传参：`make dev-start DEV_ARGS=--odl-hybrid`。

### 本地单独跑 docreader {#_2-2-本地单独跑-docreader}

`dev-start` 默认把 docreader 跑在容器里；如需本地调试 Python 代码：

```bash
cd docreader
uv sync                       # 按 uv.lock 安装依赖（容器内为 uv sync --locked --no-dev）
uv run -m docreader.main      # 启动 gRPC 服务（与 Dockerfile CMD 一致），监听 DOCREADER_GRPC_PORT（默认 50051）
```

docreader 的大量调优参数（PDF 渲染 DPI、扫描件判定、SSRF 白名单、gRPC TLS 等）以 `DOCREADER_*` 环境变量注入，完整清单见 `docker-compose.dev.yml` 的 `docreader.environment` 段。

### Lite 模式（零外部依赖） {#_2-3-lite-模式-零外部依赖}

Lite 模式把 SQLite（+sqlite-vec）与内存队列编译进单个二进制，适合快速体验与桌面端：

```bash
make build-lite     # 先构建前端到 web/，再 CGO 构建 Go（tags: sqlite_fts5）；SKIP_FRONTEND=1 跳过前端
make run-lite       # 依赖 .env.lite，构建并启动 WeKnora-lite
make package-lite   # 打 tarball 发行包（scripts/package-lite.sh）
make package-mac-app  # 打 macOS .app（scripts/package-mac-app.sh）
```

## Makefile 目标全览 {#_3-makefile-目标全览}

以下目标定义在根目录 `Makefile`，`make help` 也有一份中文帮助。

### 基础构建与运行 {#_3-1-基础构建与运行}

| 目标 | 作用 |
| --- | --- |
| `build` | `go build -o WeKnora ./cmd/server` |
| `run` | 先 `build` 再运行 `./WeKnora` |
| `test` | `go test -v ./...` |
| `clean` | `go clean` 并删除二进制 |
| `build-prod` | 生产构建：CGO_ENABLED=1，`-ldflags "-w -s"` 注入 Version/CommitID/BuildTime/GoVersion（`internal/handler` 包变量），并设置 protobuf `conflictPolicy=warn`（规避 qdrant/milvus proto 冲突） |
| `fmt` | `go fmt ./...` |
| `lint` | `golangci-lint run` |
| `deps` | `go mod download` |
| `docs` | `swag init -g ./cmd/server/main.go -o ./docs --parseDependency --parseInternal` 生成 Swagger 文档 |
| `install-swagger` | 安装 `swag` CLI |

### Docker 镜像与服务管理 {#_3-2-docker-镜像与服务管理}

| 目标 | 作用 |
| --- | --- |
| `docker-build-app` | 构建 `wechatopenai/weknora-app`（`docker/Dockerfile.app`，注入 `scripts/get_version.sh` 的版本信息） |
| `docker-build-docreader` | 构建 `wechatopenai/weknora-docreader`（`docker/Dockerfile.docreader`） |
| `docker-build-frontend` | 多阶段构建 `wechatopenai/weknora-ui`（builder 内 `npm ci` + `npm run build`，无需宿主机预构建 dist） |
| `docker-build-all` | 以上三个镜像 |
| `docker-run` | 确保 `.env` 存在（缺失时从 `.env.example` 复制或 touch）后 `docker-compose up` |
| `docker-stop` / `docker-restart` | `docker-compose down` / `stop -t 60` + `up` |
| `start-all` / `stop-all` | `scripts/start_all.sh`（一键启动/停止全部服务） |
| `start-ollama` / `start-docker` | `start_all.sh --ollama` / `--docker` |
| `build-images` / `build-images-app` / `build-images-docreader` / `build-images-frontend` / `clean-images` | `scripts/build_images.sh` 从源码构建/清理镜像 |
| `check-env` / `list-containers` / `pull-images` | `start_all.sh --check / --list / --pull` |
| `show-platform` | 显示 `uname -m` 与 Docker 构建平台（amd64/arm64 自动探测） |
| `clean-db` | 删除 `weknora_postgres-data` / `weknora_minio_data` / `weknora_redis_data` 三个 Docker volume（**清空数据**） |

### 数据库迁移（详见《数据库与迁移》一章） {#_3-3-数据库迁移-详见《数据库与迁移》一章}

| 目标 | 作用 |
| --- | --- |
| `migrate-up` / `migrate-down` | `scripts/migrate.sh up / down` |
| `migrate-version` | 查看当前迁移版本 |
| `migrate-create name=xxx` | 创建一对新迁移文件 |
| `migrate-force version=N` | 强制设置版本（dirty state 恢复） |
| `migrate-goto version=N` | 迁移到指定版本 |

### 开发模式与 Lite {#_3-4-开发模式与-lite}

| 目标 | 作用 |
| --- | --- |
| `dev-start` / `dev-stop` / `dev-restart` / `dev-logs` / `dev-status` | `scripts/dev.sh start/stop/restart/logs/status`（支持 `DEV_ARGS` 传 profile 参数） |
| `dev-app` | 本地 `go run ./cmd/server`（带版本 ldflags） |
| `dev-frontend` | 本地 `npm run dev` |
| `build-lite` / `run-lite` / `package-lite` / `package-mac-app` | Lite 模式构建/运行/打包（见 2.3） |
| `download_spatial` | `go run cmd/download/duckdb/duckdb.go` 下载 DuckDB spatial 扩展（数据分析工具用） |

## 测试体系 {#_4-测试体系}

### Go 单元测试（主模块） {#_4-1-go-单元测试-主模块}

```bash
make test          # go test -v ./...
# 或按包运行：
go test ./internal/infrastructure/chunker/...
go test -run TestXxx ./internal/application/service/...
```

主模块测试广泛使用 `go-sqlmock`、`miniredis` 等内存替身（见 `go.mod`），大部分无需真实数据库即可运行。部分包依赖 CGO（DuckDB/sqlite-vec）。

### docreader 测试（Python） {#_4-2-docreader-测试-python}

测试位于 `docreader/tests/`，使用标准库 `unittest` 编写（文件内 `unittest.main()`），覆盖解析路由、并发、EPUB/Excel/MHTML/PDF 解析、SSRF 防护等：

```bash
cd docreader
uv sync
uv run python -m unittest discover -s tests -v      # 全部
uv run python -m unittest tests.test_parser_routing  # 单个
```

### CLI 测试与验收测试 {#_4-3-cli-测试与验收测试}

`cli/` 是独立 Go module，自带 `cli/Makefile`：

```bash
cd cli
make test            # go test ./...
make test-coverage   # 带覆盖率
make lint            # go vet
```

跨切面的契约/集成测试集中在 `cli/acceptance/`（见 `cli/acceptance/doc.go`）：

- `cli/acceptance/contract/` — envelope JSON 输出形状 golden 测试 + error.code 注册表一致性；
- `cli/acceptance/e2e/` — 对真实 WeKnora server 的黑盒测试（testscript 风格），需要环境变量指向测试服务器；CI 侧由 `.github/workflows/cli-e2e.yml` 承载，**按需触发**（`workflow_dispatch` 手动，或给 PR 打 `acceptance-e2e` 标签），使用 secrets `WEKNORA_E2E_HOST` / `WEKNORA_E2E_TOKEN`。

### tests/ 目录与前端测试 {#_4-4-tests-目录与前端测试}

- `tests/miniprogram/miniprogram.test.js` — 小程序客户端的集成测试（Node 测试脚本），是 `tests/` 目前唯一内容；
- 前端：`cd frontend && npm run type-check`（vue-tsc）与 `npm test`（`tsx --test`，Node test runner）。

### 引导式学习集成测试

`scripts/seed-guided-learning.py` 通过公开 API 创建两组一次性用户、独立租户、知识库和模型配置，发布六份合成来源文档，再建立六个带来源分块引用的 Wiki 页面。脚本不依赖已有 `.runtime` 文件或其他工作区。

使用 Python 3.10+ 和 Linux/macOS。按上方开发模式启动专用测试实例，开启注册并设置 `WEKNORA_AUTH_DEFAULT_TENANT_MODE=create_personal`、`WEKNORA_TENANT_ENABLE_CROSS_TENANT_ACCESS=false`。数据库迁移必须完成，异步任务处理正常。前端的 `VITE_DEV_PROXY_TARGET` 必须指向同一后端。请勿连接生产实例。

以下命令在仓库根目录执行，服务端口按实际配置填写；脚本仅接受 HTTP loopback 地址：

```bash
export GL_RUNTIME_DIR=.runtime/guided-learning
export GL_API_BASE=http://127.0.0.1:8080
export GL_FRONTEND_BASE=http://127.0.0.1:5173

# 通过当前进程环境提供 MODEL_NAME、MODEL_BASE_URL、MODEL_API_KEY。
# 模型需支持 OpenAI 兼容的对话与 Tool Calling 接口，base URL 使用 HTTPS。
# 或传 --model-env /path/to/model.env，文件须归当前用户所有且权限为 0600。
python3 -B scripts/seed-guided-learning.py seed --consent-create-fixtures
python3 -B scripts/seed-guided-learning.py opt-in --user learner_a
python3 -B scripts/seed-guided-learning.py configure-agent --user learner_a
python3 -B scripts/test-guided-learning-api.py inspect
```

seed 会触发实际文档解析、摘要和 Wiki 生成，消耗模型额度。只有来源达到 `completed`，且分块内容非空、`index_status=ready`、`is_enabled=true`，才生成清单。学习开关不会随 seed 开启；`opt-in` 单独授权指定账号。`opt-in`、`configure-agent` 和 `reparse` 均须显式指定 `--user learner_a|learner_b|all`，不读取模型密钥，也不创建账号；`configure-agent` 只绑定已有模型与知识库，不修改学习开关。

`GL_RUNTIME_DIR` 只能位于当前仓库的 `.runtime/` 内。账号文件 `seed-accounts.json` 使用随机密码、权限 `0600`，绑定 API 地址和真实用户/租户 ID；`evidence/seed.json` 保存不含凭证的文档、页面与分块清单。运行结果写入同目录的 `evidence/`，这些文件全部被 Git 忽略。API/E2E 验收从清单取得模型 ID，不需要保留模型密钥。

```bash
# 离线夹具回归，不连接服务、不读取真实凭证
python3 -B -m unittest discover -s scripts -p 'test_guided_learning_fixtures.py' -v

# API：实际生成题目、判分、Agent 工具调用，以及 learner_b 隐私清理
python3 -B scripts/test-guided-learning-api.py run --consent-learner-b

# 浏览器依赖安装在本仓库，首次需安装 Chromium
npm --prefix frontend ci
npm --prefix frontend exec -- playwright install chromium
npm --prefix frontend run test:e2e:config
npm --prefix frontend run test:e2e:guided-learning
```

API 验收会清空 `learner_b` 的学习记录，不清除 `learner_a`。浏览器默认检查导航、图谱、来源、导出和隔离；`GL_RUN_QUIZ=1` 开启真实测验，`GL_ALLOW_LEARNER_B_CLEAR=1` 开启清空验证，`GL_RUN_MOCK_ERRORS=1` 开启单独标记的模拟错误用例。不要并行运行 seed、API 验收和浏览器验收。

重复执行 seed 会复用已有账号、知识库和文档。若初始化中断，保留账号文件后重跑；解析失败时执行以下命令，再运行 seed。`reparse` 仅重提失败的来源，已运行的任务只等待，已完成的来源仍检查有效分块。等待上限通过 `--timeout` 设置，默认每份文档 900 秒，范围 1-3600 秒。

```bash
python3 -B scripts/seed-guided-learning.py reparse --user all
# 仅在确认可以覆盖页面编辑、丢弃失效引用页面的历史后执行
python3 -B scripts/seed-guided-learning.py seed --consent-create-fixtures --refresh-pages
```

引用变化时，`--refresh-pages` 会删除并重建对应页面；只有正文等可编辑字段变化时，使用带版本号的更新。未授权时两种变化均会拒绝覆盖。更换测试服务时使用新的 `GL_RUNTIME_DIR`，不要删除账号文件后重建同一组夹具。生成题目未通过校验时测试会失败，不会用模拟答案替代真实结果。

## 代码规范与提交流程 {#_5-代码规范与提交流程}

### Go 代码规范 {#_5-1-go-代码规范}

仓库根 `.golangci.yml`（golangci-lint v2 配置格式）：

```yaml
version: 2
linters-settings:
  lll:
    line-length: 120
    tab-width: 4
linters:
  enable:
    - lll           # 控制行宽（120 列）
    - govet
    - revive
formatters:
  enable:
    - gofmt
    - gofumpt
```

提交前建议执行：

```bash
make fmt && make lint && make test
```

注意格式化标准是 **gofumpt**（比 gofmt 更严格），行宽上限 120。

### CI 与提交流程 {#_5-2-ci-与提交流程}

`.github/` 下的实际配置：

| 文件 | 触发路径 | 作用 |
| --- | --- | --- |
| `workflows/app.yml` | 根模块 Go 代码、`go.mod`、`config/`、`migrations/`、`scripts/`、`docker/Dockerfile.app` | 主模块检查：gofmt 格式校验（只针对 PR 内的提交）、`go vet`、`go test`、`go build ./cmd/server` |
| `workflows/frontend.yml` | `frontend/`、`scripts/build_frontend_dist.sh` | Node 24：`npm test` + `npm run type-check` + `npm run build`；另构建 `frontend/Dockerfile` 多阶段镜像（不推送） |
| `workflows/docreader.yml` | `docreader/`、`testdata/`、`packages/`、相关 Dockerfile | uv 装依赖 → `compileall` → `unittest discover docreader/tests`；再拉起 docreader gRPC 服务跑 `go test ./docreader/client ./docreader/proto` |
| `workflows/mcp-server.yml` | `mcp-server/` | Python 3.10-3.13 矩阵测试；合入 main 后按 `pyproject.toml` 里的版本号用 PyPI Trusted Publishing 自动发布（版本已存在则跳过上传，不依赖打 tag） |
| `workflows/cli.yml` | `cli/` | ubuntu/macos/windows 三平台矩阵，Go 1.26，`go build` + `go test -race -coverprofile` + `go vet` + skill wire 词表检查 |
| `workflows/cli-e2e.yml` | 手动 / label | CLI 端到端验收（label `acceptance-e2e` 或手动触发，见 4.3） |
| `workflows/docker-image.yml` | — | Docker 镜像构建发布 |
| `workflows/release-lite.yml` | — | Lite 版本发布 |
| `pull_request_template.md` | — | PR 模板 |
| `ISSUE_TEMPLATE/` | — | Issue 模板 |
| `dependabot.yml` | — | 依赖升级机器人 |

四条按路径触发的检查（app / frontend / docreader / mcp-server）覆盖了主要模块，但本地先跑一遍仍然更省时间。前端可以直接用 `scripts/verify_frontend_pr.sh`，它按 CI 同样的顺序执行 `npm test` → `npm run type-check` → `npm run build`。

提交流程：fork / 分支 → 本地 `fmt + lint + test` → PR（按模板填写）→ 相关路径触发 CI。

## 调试技巧 {#_6-调试技巧}

### 日志级别 {#_6-1-日志级别}

日志实现在 `internal/logger/logger.go`（logrus）。级别由环境变量 `LOG_LEVEL` 控制，取值 `debug` / `info` / `warn`（`warning`）/ `error` / `fatal`，未设置或非法时默认 **debug**（`getLogLevelFromEnv()`）。`LOG_PATH` 控制输出路径；两者在 `main()` 加载 `.env` 后即时生效。docreader 侧同样读取 `LOG_LEVEL`（compose 中透传）。

每个请求带 `X-Request-ID` 贯穿 app 与 docreader 日志（docreader 的 `init_logging_request_id`），排查问题时先抓 request id。

### GIN_MODE 与 Swagger {#_6-2-gin-mode-与-swagger}

- `GIN_MODE=release` 时禁用 Swagger UI（`internal/router/router.go`）、并影响 embed channel 的安全行为；开发时不要设置或设为 `debug`。
- `make docs` 生成 Swagger 后，启动服务访问 `http://localhost:8080/swagger/index.html`。

### 数据库与迁移调试 {#_6-3-数据库与迁移调试}

- `AUTO_MIGRATE=false` 可关闭启动时自动迁移；`AUTO_RECOVER_DIRTY`（默认开启，设为 `false` 关闭）控制 dirty state 自动恢复（`internal/container/container.go`）。迁移失败只告警不阻断启动，注意看启动日志里的 `Database migration failed`。
- `make migrate-version` 快速确认 schema 版本。

### LLM 链路观测（Langfuse） {#_6-4-llm-链路观测-langfuse}

`dev.sh start` 默认拉起自建 Langfuse（`http://localhost:3000`）。本地 `go run` 的 app 需要导出：

```bash
export LANGFUSE_HOST=http://localhost:3000
export LANGFUSE_PUBLIC_KEY=pk-lf-xxx
export LANGFUSE_SECRET_KEY=sk-lf-xxx
```

即可在 Langfuse UI 中查看每次会话的模型调用 trace（文档处理 span 亦落库到 `knowledge_processing_spans` 表，前端可视化）。

### pprof {#_6-5-pprof}

当前代码中**未内置** `net/http/pprof` 端点（`internal/`、`cmd/` 下无 pprof 引用）。如需性能剖析，可临时在 `cmd/server/main.go` 中 `import _ "net/http/pprof"` 并起一个独立 `http.ListenAndServe("localhost:6060", nil)`，或使用 `go test -bench . -cpuprofile` 针对具体包剖析。

### 分块策略诊断 {#_6-6-分块策略诊断}

chunker 提供 `SplitWithDiagnostics()`（`internal/infrastructure/chunker/strategy.go`），返回策略链选择、各 tier 被拒原因与文档画像，配合 `LOG_LEVEL=debug`（`chunker: tier %s rejected` 日志）可排查分块效果问题。
