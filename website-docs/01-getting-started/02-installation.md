# 安装部署

WeKnora 支持 Docker Compose、Kubernetes Helm、Lite 单二进制和桌面应用。服务器部署可选择 Compose 或 Helm；本地使用可选择 Lite；参与开发时使用独立的开发编排。各方式的依赖、启动命令和数据目录如下。

## 部署形态总览

| 形态 | 入口 | 数据库 | 队列/流 | 适用场景 |
| --- | --- | --- | --- | --- |
| Docker Compose（标准） | `docker-compose.yml` | ParadeDB（PostgreSQL） | Redis + Asynq | 生产 / 团队自托管，推荐 |
| Docker Compose（开发） | `docker-compose.dev.yml` | 同上（仅基础设施进容器） | 同上 | 本地开发：app / frontend 在宿主机运行 |
| Helm | `helm/` | ParadeDB（chart 内置） | Redis（chart 内置） | Kubernetes >= 1.25 |
| Lite 单二进制 | `make build-lite` / `scripts/package-lite.sh` | SQLite（FTS5 + sqlite-vec） | 内存（无 Redis） | 个人 / 离线 / 低资源环境 |
| 桌面应用（**未正式发布**） | `cmd/desktop`（Wails v2）+ `scripts/package-mac-app.sh` | SQLite | 内存 | 桌面单机使用，带图形界面与本地数据目录 |

```mermaid
flowchart TB
    subgraph prod["标准部署 (docker compose up)"]
        FE1["frontend :80"] --> APP1["app :8080"]
        APP1 --> PG1[("postgres :5432")]
        APP1 --> RD1[("redis :6379")]
        APP1 --> DR1["docreader :50051"]
        APP1 -. "profile 可选" .-> OPT1["qdrant / milvus / neo4j / minio / searxng / langfuse / mcp ..."]
    end
    subgraph dev["开发模式 (make dev-start)"]
        LOCALAPP["宿主机 go run app :8080"] --> PG2[("postgres 容器")]
        LOCALAPP --> RD2[("redis 容器")]
        LOCALAPP --> DR2["docreader 容器 :50051"]
        LOCALFE["宿主机 npm run dev 前端"] --> LOCALAPP
    end
    subgraph lite["Lite / 桌面 (单进程)"]
        BIN["WeKnora-lite 二进制 (内嵌 web/ 前端)"]
        BIN --> SQLITE[("SQLite: FTS5 + sqlite-vec")]
        BIN --> MEMQ[("内存流管理")]
        BIN -. "可选" .-> DR3["docreader 127.0.0.1:50051"]
        BIN --> OLLAMA["Ollama :11434"]
    end
```

## 硬件与依赖要求

- **标准 Docker 部署**：Docker 20.10+ 与 Docker Compose v2（v1 `docker-compose` 也兼容，`scripts/start_all.sh` 会自动探测）；建议 4 核 CPU / 8GB 内存起步（docreader 含 LibreOffice、Playwright，较吃内存），磁盘按知识库规模预留（Postgres 卷 + `/data/files` 文件卷）。启用 Milvus / OpenSearch / Langfuse 等可选组件需相应增加内存。
- **模型服务**：本地推理需 [Ollama](https://ollama.com)（默认地址 `http://host.docker.internal:11434`，`OLLAMA_OPTIONAL=true` 时不可用仅告警不阻断）；或任意 OpenAI 兼容 API（DeepSeek、通义、智谱、硅基流动等）。上述 8GB 起步不含 Ollama 模型权重；Neo4j 默认关闭（需启用 `neo4j` profile）。
- **源码编译**：Go 1.26（见 `docker/Dockerfile.app` builder 阶段 `golang:1.26-bookworm`）、CGO（依赖 `libsqlite3-dev`）、Node.js + npm（前端）、Python 3.10 + uv（docreader）。
- **Kubernetes**：>= 1.25.0（`helm/Chart.yaml`）。

Compose 默认 ParadeDB `v0.22.6-pg17` 的 x86 CPU 基线为 `x86-64-v2`（包括 SSE4.2、POPCNT），不再强制要求 AVX2。这不代表所有 ARM CPU 或其他可选服务均兼容。

## 一、Docker Compose 标准部署（docker-compose.yml）

最快路径：

```bash
git clone https://github.com/Tencent/WeKnora.git && cd WeKnora
cp .env.example .env              # 编辑必填项：DB_USER/DB_PASSWORD/DB_NAME、REDIS_PASSWORD、JWT_SECRET、SYSTEM_AES_KEY
make start-all                # 等价 ./scripts/start_all.sh（默认拉取最新镜像）
# 或直接：
docker compose pull           # 拉取与 WEKNORA_VERSION 匹配的镜像
docker compose up -d
docker compose ps                 # 等所有服务变成 healthy/running
```

`.env.example` 中的 `JWT_SECRET`、`SYSTEM_AES_KEY` 默认留空，需要在首次部署时生成一次并妥善保存：`JWT_SECRET` 可用 `openssl rand -hex 32`，`SYSTEM_AES_KEY` 必须是 32 字节，可用 `openssl rand -hex 16`。升级已有部署时请沿用原来的 `SYSTEM_AES_KEY`，否则已加密的凭据无法解密。各密钥的作用见[配置详解](./04-configuration.md)。

停止用 `docker compose down`（加 `-v` 会连数据卷一起删，慎用）。仓库里的 `make start-all` 是同一条命令的封装（`scripts/start_all.sh`，额外做 Ollama 检查、`.env` 兜底、沙箱镜像预拉取），两者选一即可。

启动后在浏览器打开 `http://localhost` 就是前端（端口由 `FRONTEND_PORT` 决定，默认 80），首次访问会落到注册页。前端 Nginx 把 `/api/` 反代到后端，所以接口调用同样走 `http://localhost/api/v1`；后端 `8080` 端口也直接映射到宿主机，`curl http://localhost:8080/health` 可用于确认后端就绪。

> 注意：`docker-compose.yml` 的 app 服务使用 `env_file: [.env]`，`.env` 不存在会导致 compose 解析失败。`make docker-run` / `start_all.sh` 会自动 `cp .env.example .env` 或 `touch .env` 兜底。

### 版本升级

若已有部署并下载了更新的 release：

> 如果数据库仍为 ParadeDB `v0.22.2-pg17`，先按 [ParadeDB 升级说明](06-paradedb-upgrade.md) 停止写入、备份、保留数据卷更换镜像并完成 `pg_search` 扩展升级，再恢复应用。仅替换镜像不会更新已有数据库的扩展 SQL；迁移 `000099` 会处理 WeKnora 库中符合条件的 `0.22.2–0.22.5`，其他数据库仍需单独检查。

```bash
# 在 .env 中将 WEKNORA_VERSION 设为目标版本（如 0.7.0），或保持 latest
docker compose pull
docker compose up -d
```

> 仅执行 `docker compose up -d` 会复用本地缓存镜像，可能导致 Web UI 显示版本与下载的 release 不一致。

### 核心服务（默认启动）

| 服务 | 镜像 | 端口（宿主:容器） | 依赖 | 说明 |
| --- | --- | --- | --- | --- |
| `frontend` | `wechatopenai/weknora-ui:${WEKNORA_VERSION:-latest}` | `${FRONTEND_PORT:-80}:80` | app（healthy） | Nginx 托管 SPA 并反代到 app；`APP_HOST`/`APP_BACKEND_PORT`/`APP_SCHEME` 可指向远程后端 |
| `app` | `wechatopenai/weknora-app` | `${APP_PORT:-8080}:8080` | postgres（healthy）、redis、docreader（healthy） | Go 后端；挂载 `./config/config.yaml`、`data-files` 卷；健康检查 `GET /health` |
| `docreader` | `wechatopenai/weknora-docreader` | 仅 `expose: 50051`（不发布到宿主机） | — | 文档解析 gRPC 服务；健康检查 `grpc_health_probe`；与 app 共享 `docreader-tmp` 卷传递图片 |
| `postgres` | `paradedb/paradedb:v0.22.6-pg17` | 不映射宿主端口 | — | ParadeDB = PostgreSQL 17 + BM25/向量扩展，默认检索引擎 |
| `redis` | `redis:7.0-alpine` | 不映射宿主端口 | — | `--appendonly yes --requirepass ${REDIS_PASSWORD}` |

### 可选服务与 profiles

按需以 `docker compose --profile <name> up -d` 启用：

| profile | 服务 | 端口 | 用途 |
| --- | --- | --- | --- |
| `searxng`（含 `full`） | `searxng-init` + `searxng` | `127.0.0.1:8888`（`SEARXNG_BIND`/`SEARXNG_PORT`） | 自建 Web 搜索；默认仅绑定回环，公开前必须轮换 `SEARXNG_SECRET` |
| `minio`（含 `full`） | `minio` | 9000（S3）/ 9001（控制台） | S3 兼容对象存储（`STORAGE_TYPE=minio`），默认账号 `minioadmin/minioadmin` |
| `neo4j`（含 `full`） | `neo4j` | 7474 / 7687 | 知识图谱（`NEO4J_ENABLE=true`），默认 `neo4j/password` |
| `qdrant`（含 `full`） | `qdrant` | 6333（REST）/ 6334（gRPC） | 向量库（`RETRIEVE_DRIVER=qdrant`） |
| `milvus` | `milvus` | 19530 / 9091 | 向量库（standalone，内嵌 etcd） |
| `weaviate` | `weaviate` | 9035（HTTP）/ 50052（gRPC） | 向量库 |
| `doris` | `doris-fe` + `doris-be` | 8030（FE HTTP）/ 9030（FE MySQL）/ 8040（BE） | Apache Doris 4.1 检索引擎（需 >= 3.0，HNSW ANN） |
| `dex`（含 `full`） | `dex` | 5556 | OIDC 测试用 IdP（配置在 `misc/dex-config.yaml`） |
| `langfuse`（含 `full`） | `langfuse-db-init`、`langfuse-clickhouse`、`langfuse-minio`、`langfuse-worker`、`langfuse-web` | 3000（UI）/ 9100/9101（专用 MinIO） | 自建 Langfuse 可观测栈，复用 WeKnora 的 postgres（新建 `langfuse` 库）与 redis（DB 1） |
| `odl-hybrid` | `odl-hybrid` | expose 5002 | OpenDataLoader/Docling PDF 混合解析后端（仅本地构建，配 `DOCREADER_ODL_HYBRID` 使用） |
| `full` | `sandbox`、`mcp` 及上述带 full 标记的服务 | mcp: `${MCP_PORT:-8082}:8000` | `sandbox` 仅用于 build/pull 镜像（`command: ["true"]`，非常驻）。Docker 沙箱默认关闭，需设 `WEKNORA_SANDBOX_DOCKER_ENABLED=true` 并挂载 `docker.sock`（等同宿主机 root）；Cube/E2B 不依赖本机 daemon。`mcp` 为 MCP Server |

app 容器的 `environment` 段落是全量环境变量清单（数据库、向量库、对象存储、Docreader 调优、租户策略、OIDC 等），详见 [04-configuration.md](./04-configuration.md)。

## 二、开发模式（docker-compose.dev.yml + scripts/dev.sh）

开发编排只把**基础设施**放进容器（postgres、redis、docreader 端口全部映射到宿主机），app 与 frontend 在宿主机上以热更新方式运行：

```bash
make dev-start          # ./scripts/dev.sh start，可加 DEV_ARGS=--odl-hybrid / --minio / --qdrant / --neo4j / --dex / --full
make dev-app            # 宿主机启动 Go 后端（自动把 DB_HOST/REDIS_ADDR 指到 localhost）
make dev-frontend       # 宿主机启动 Vue 前端 dev server
make dev-logs / dev-status / dev-stop / dev-restart
```

与生产编排的差异：

- postgres（`5432`）、redis（`6379`）、docreader（`50051`）都发布到宿主机端口，便于本地进程直连；
- 额外提供 `opensearch`（9200）与 `opensearch-dashboards`（5601，profile `opensearch-ui`）单节点开发环境（security 插件关闭）；
- `dev.sh` 会加载 `.env` 与 `.env.local`（后者覆盖前者），并支持 `DEV_REMOTE_HOST` 指向远程基础设施。

## 三、镜像构建（docker/ 目录）

| Dockerfile | 产物镜像 | 要点 |
| --- | --- | --- |
| `docker/Dockerfile.app` | `wechatopenai/weknora-app` | 三阶段：先构建 BrowserSkill 的 `bsk` 与配套 Chrome 扩展（装入 `/opt/weknora/browserskill/`，见[本机浏览器](../05-clients/09-local-browser.md)）；再用 `golang:1.26-bookworm` 编译（`make build-prod`，默认 `WITH_ANYDOC=1` 链接进程内 office 解析引擎，注入版本信息，预下载 DuckDB 扩展 `cmd/download/duckdb`）→ `debian:12.12-slim` 运行层（含 `migrate` 迁移工具、python3/node/uvx（供 stdio MCP 使用）、ffmpeg（ASR）、gosu 降权，以及第三方许可证文本）。入口 `scripts/docker-entrypoint.sh`：修复挂载目录属主；若挂载了 docker.sock，按 socket GID 把 appuser 加入对应组（compose `group_add` 在 gosu 后无效），再以 appuser 运行 `./WeKnora`。`EXPOSE 8080` |
| `docker/Dockerfile.docreader` | `wechatopenai/weknora-docreader` | Python 3.10 + uv 依赖锁定；生成 protobuf；运行层安装 LibreOffice、OpenJDK 17、antiword、Playwright（webkit）与 `grpc_health_probe`。轻量版不含 PaddleOCR。`EXPOSE 50051`。支持 `APT_MIRROR` 构建参数 |
| `docker/Dockerfile.odl-hybrid` | `weknora-odl-hybrid:local` | 安装 `opendataloader-pdf[hybrid]`（Docling），监听 5002，默认 `--no-ocr`；仅本地构建不发布 |
| `docker/Dockerfile.sandbox` | `wechatopenai/weknora-sandbox` | Agent 会话沙箱镜像。基础环境为 Python 3.12-slim + Node 20 + uv/pnpm，默认 `root` 执行，保留 `user`(UID 1000) 供显式选择。默认构建目标 `sandbox` 供 Docker 后端使用；另有 `cube`（含 Cube envd）、`desktop` / `desktop-cube`（带图形桌面）等目标，见[沙箱部署](../06-development/04-sandbox-deployment.md) |
| `frontend/Dockerfile` | `wechatopenai/weknora-ui` | 两阶段：digest 锁定的 `node:24-bookworm-slim`（`$BUILDPLATFORM`，避免多架构 CI 用 QEMU 跑 Vite）内 `npm ci` + `npm run build`（`VITE_IS_DOCKER` / `VITE_FRONTEND_COMMIT`），可选 `NPM_REGISTRY` / `NODE_MAX_OLD_SPACE_SIZE`；运行层为按 digest 固定的 `nginx:1.30.3-alpine`（兼容 CentOS 7 旧内核）。无需宿主机预构建 `dist/` |

从源码构建全部镜像：

```bash
make build-images        # ./scripts/build_images.sh，参数 --app/--docreader/--frontend/--sandbox/--clean
# 或单独：
make docker-build-app
make docker-build-docreader
make docker-build-frontend
```

## 四、Makefile 部署相关目标速查

| 目标 | 作用 |
| --- | --- |
| `make start-all` / `stop-all` | 调 `scripts/start_all.sh` 启停整套服务（含 Ollama 检查、.env 兜底、沙箱镜像预拉取） |
| `make start-ollama` / `start-docker` | 仅启动 Ollama / 仅启动 Docker 服务 |
| `make docker-run` / `docker-stop` / `docker-restart` | 传统 `docker-compose up/down/restart`（自动兜底 `.env`） |
| `make build-images*` / `clean-images` / `pull-images` | 源码构建 / 清理 / 拉取镜像 |
| `make check-env` / `list-containers` / `show-platform` | 环境检查（`scripts/check-env.sh` 校验 .env 必填变量与工具链）/ 容器列表 / 构建平台（自动识别 amd64/arm64） |
| `make migrate-up` / `migrate-down` / `migrate-version` / `migrate-create name=x` / `migrate-force version=n` / `migrate-goto version=n` | 数据库迁移（`scripts/migrate.sh`；容器内默认 `AUTO_MIGRATE=true` 启动时自动迁移） |
| `make dev-*` | 开发模式（见上文） |
| `make build` / `run` / `build-prod` | 本地编译运行 `cmd/server`（`build-prod` 需 CGO，注入版本号与 `Edition=standard`） |
| `make build-lite` / `run-lite` / `package-lite` | Lite 模式构建 / 运行（读 `.env.lite`）/ 打发行包 |
| `make package-mac-app` | 打包 macOS 桌面应用 |
| `make docs` / `install-swagger` | 生成 Swagger 文档（`http://localhost:8080/swagger/index.html`，release 模式禁用） |
| `make clean-db` | 删除 postgres/minio/redis 数据卷（危险操作） |

## 五、scripts/ 启动脚本

| 脚本 | 职责 |
| --- | --- |
| `scripts/start_all.sh` | 一键启动：参数 `-o`（仅 Ollama）、`-d`（仅 Docker）、`-a`（全部，默认）、`-s`（停止）、`-c`（检查环境）、`-l`（列容器）、`-p`(拉镜像)；自动探测 compose v1/v2、按 `uname -m` 设定 `PLATFORM`、后台预拉取 sandbox 镜像 |
| `scripts/dev.sh` | 开发环境编排（见上文），子命令 `start/stop/restart/logs/status/app/frontend` |
| `scripts/check-env.sh` | 校验 `.env` 必填变量（DB_*、STORAGE_TYPE、REDIS_ADDR、OLLAMA_BASE_URL 等）与 Go/npm/Docker/Air 工具链 |
| `scripts/build_images.sh` | 构建镜像并注入版本（git tag / commit / build time），支持跨架构 |
| `scripts/build_frontend_dist.sh` | 宿主机构建前端静态产物 `frontend/dist`（Lite / 桌面打包等非 Docker 场景；UI 镜像改由 Dockerfile 多阶段构建） |
| `scripts/migrate.sh` | golang-migrate 封装 |
| `scripts/docker-entrypoint.sh` | app 容器入口（属主修复 + docker.sock GID 补组 + gosu 降权） |
| `scripts/package-lite.sh` / `package-mac-app.sh` | Lite tarball / macOS .app 打包 |

## 六、Helm 部署（helm/）

`helm/Chart.yaml`：apiVersion v2，chart 名 `weknora`，appVersion 跟随版本（如 v0.8.2），要求 Kubernetes >= 1.25.0。

Chart 内包含五个组件：`app`（`wechatopenai/weknora-app`）、`frontend`（`wechatopenai/weknora-ui`）、`docreader`、`postgresql`（ParadeDB 镜像，chart 默认 `paradedb/paradedb:v0.18.9-pg17`，与 Compose 的版本不同）、`redis`（`redis:7-alpine`），并可选启用 `minio` 与 `neo4j`。

`helm/values.yaml` 关键配置：

```yaml
app:
  replicaCount: 1
  env:
    GIN_MODE: release
    RETRIEVE_DRIVER: postgres      # postgres / elasticsearch_v7 / elasticsearch_v8 / qdrant ...
    STORAGE_TYPE: local            # local / minio / cos / tos / s3
    STREAM_MANAGER_TYPE: redis
postgresql:
  enabled: true
  persistence: { enabled: true, size: 10Gi }
redis:
  enabled: true
  persistence: { enabled: true, size: 1Gi }
dataFiles:
  persistence: { enabled: true, size: 10Gi }
global:
  maxFileSizeMB: 50                 # 上传大小上限，同时作用于 frontend / app / docreader
secrets:                            # 必填项，或用 existingSecret 引用已有 Secret
  dbPassword: ""
  redisPassword: ""
  jwtSecret: ""
  systemAesKey: ""                  # 32 字节 AES-256 主密钥
```

`global.maxFileSizeMB` 与 Compose 的 `MAX_FILE_SIZE_MB` 含义相同，chart 会把它写入 frontend（Nginx 请求体上限）、app（上传限制）与 docreader（gRPC 消息上限）三处。可选的 MinIO 镜像为 `quay.io/minio/minio`。

```bash
helm install weknora ./helm -n weknora --create-namespace \
  --set secrets.dbPassword=xxx --set secrets.redisPassword=xxx \
  --set secrets.jwtSecret=xxx --set secrets.systemAesKey=$(openssl rand -hex 16)
```

## 七、桌面端（Lite 模式 / 桌面应用）

桌面端面向本机与低资源环境，底层都是同一套 Lite 运行时（单进程 + SQLite + 内存队列），只是分发与启动方式不同：**单二进制**（命令行启动，也可作为后台服务）、**桌面应用**（图形界面，双击启动）。知识库与问答能力一致；免注册登录、本机沙箱与绑定本机项目目录只在桌面应用中提供。

### Lite 运行时（零外部依赖） {#_7-1-lite-运行时-零外部依赖}

Lite 模式通过编译期 `EDITION=lite` 与运行期 `.env.lite` 环境实现「一进程跑全套」：

- **数据库**：`DB_DRIVER=sqlite` + `DB_PATH=./data/weknora.db`，编译加 `-tags "sqlite_fts5"`；
- **检索**：`RETRIEVE_DRIVER=sqlite`，走 SQLite FTS5 全文检索 + sqlite-vec 向量检索，无需任何向量数据库；
- **队列/流**：`STREAM_MANAGER_TYPE=memory`（`internal/stream/factory.go`），不需要 Redis，Asynq 分布式队列在 Lite 模式下为内存/no-op；
- **前端**：`make build-lite` 会把 `frontend/dist` 复制为仓库根的 `web/`，二进制直接内嵌托管静态资源（`WEKNORA_WEB_DIR` 可指定目录，router 的 `serveFrontendStatic` 提供服务）；
- **文档解析**：仍可选连本地 docreader（`DOCREADER_ADDR=127.0.0.1:50051`）；
- **沙箱**：单二进制启动时不预置后端，可在设置页按空间配置 Docker、CubeSandbox 或 E2B；桌面应用的会话在未指定远程沙箱时使用本机操作系统沙箱，见[桌面客户端](../05-clients/05-desktop.md)。

```bash
cp .env.lite.example .env.lite      # 填写 SYSTEM_AES_KEY（openssl rand -hex 16）与 JWT_SECRET
make run-lite                       # 构建并以 .env.lite 环境启动 ./WeKnora-lite
make package-lite                   # 打包发行 tarball（scripts/package-lite.sh）
```

单二进制通过浏览器访问时，与标准版一样需要注册和登录。桌面应用启动时通过原生桥接取得每进程随机凭据，调用 `POST /auth/auto-setup` 自动创建本地账号并登录；该接口不接受匿名 HTTP 请求。

### 桌面应用（cmd/desktop，Wails v2） {#_7-2-桌面应用-cmd-desktop-wails-v2}

桌面应用提供图形界面的本机使用方式：双击启动，进程内自带后端与 SQLite，数据落在系统的应用数据目录；另有端口设置、局域网绑定与更新检查等桌面特有能力。运行时能力与 [Lite 运行时（零外部依赖）](#_7-1-lite-运行时-零外部依赖) 相同。

::: warning 尚未正式发布
桌面应用目前**没有随 Release 提供安装包**，需要自己按下面的步骤构建。`release-lite.yml` 里已有跨平台（macOS universal/amd64/arm64、Linux amd64、Windows amd64）的构建任务，但该工作流的 tag 触发被注释掉、只能手动触发，且当前最新 Release 未附带任何产物。
:::

- 入口 `cmd/desktop/main.go` + `cmd/desktop/wails.json`；`cmd/desktop/app.go` 向前端暴露 `GetAPIBaseURL`（返回 `http://127.0.0.1:PORT/api/v1`）、HTTP 端口与「绑定到局域网」设置、`CheckForUpdates` 自动更新检查等绑定方法。
- `scripts/package-mac-app.sh`：先构建前端到 `web/`，再 `wails build -tags "sqlite_fts5"`，最后组装 `.app` 包 —— `Contents/MacOS/WeKnora Lite` 为主程序，`Contents/Resources` 内嵌 `.env`、config、`migrations/sqlite`、web 前端；相对路径数据自动重定向到 `~/Library/Application Support/WeKnora Lite/data/`，日志写 `~/Library/Logs/WeKnora Lite/`。

```bash
make package-mac-app
```

## 八、源码编译运行

```bash
# 后端（标准版，需本地 postgres/redis/docreader，见开发模式）
go mod download
make build && ./WeKnora                       # 或 make build-prod

# 前端
cd frontend && npm ci && npm run dev          # 开发；npm run build 产出 dist/

# docreader
cd docreader && uv sync --locked && bash scripts/generate_proto.sh && uv run -m docreader.main  # 与镜像 CMD 一致
```

配置文件查找顺序（`internal/config/config.go` 的 `LoadConfig`）：当前目录 → `./config` → `$HOME/.appname` → `/etc/appname/`，文件名 `config.yaml`。

## 常见部署拓扑

```mermaid
flowchart TB
    subgraph host["单机 Docker Compose（最常见）"]
        direction LR
        U1["用户"] --> N1["frontend :80"] --> A1["app :8080"]
        A1 --> D1["docreader"]
        A1 --> P1[("postgres")]
        A1 --> R1[("redis")]
        A1 --> O1["宿主机 Ollama :11434 (host.docker.internal)"]
    end
    subgraph k8s["Kubernetes (Helm)"]
        direction LR
        ING["Ingress"] --> FE2["frontend Deployment"] --> A2["app Deployment"]
        A2 --> PVC1[("PVC: postgres 10Gi / redis 1Gi / data-files 10Gi")]
        A2 --> D2["docreader Deployment"]
    end
    subgraph laptop["个人：Lite / 桌面"]
        direction LR
        U3["用户"] --> L1["WeKnora-lite 单进程 (内嵌前端 + SQLite + 内存队列)"]
        L1 --> O3["Ollama / 远程 OpenAI 兼容 API"]
    end
```

## 下一步

部署完成后，请阅读 [03-quickstart.md](./03-quickstart.md) 完成初始化与首次问答。
