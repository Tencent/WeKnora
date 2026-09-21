# 开发环境（Windows 宿主机实测配置，2026-09-21）

本文件的命令在这台机器上验证过。环境特点是：Docker Hub / GitHub 部分域名直连不稳，go 模块走 goproxy.cn。

## 组件清单

| 组件 | 形态 | 状态 |
|---|---|---|
| Go 1.27 | winget 安装（`C:\Program Files\Go`） | ✅ |
| MinGW gcc（CGO 必需） | winget `BrechtSanders.WinLibs.POSIX.UCRT` | ✅ |
| sqlite3 头文件 shim | `docker/dev/sqlite3-shim/sqlite3.h`（桥接 mattn/go-sqlite3 的 `sqlite3-binding.h`） | ✅ |
| postgres（paradedb 兼容） | 自建镜像 `paradedb-local:pg17`（`docker/dev/Dockerfile.paradedb`） | ✅ 运行中 |
| redis | `redis:7.0-alpine` 容器 | ✅ 运行中 |
| 前端依赖 | `frontend/` npm install | ✅ |
| Playwright chromium | `npx playwright install chromium` | ✅ |

## 启动中间件

```bash
# 注意必须带 DOCKER_API_VERSION（本机 Docker Desktop 与 compose v5 协商异常）
DOCKER_API_VERSION=1.47 docker compose -f docker-compose.dev.yml -f docker-compose.dev.fix.yml up -d postgres redis
```

验证：

```bash
docker exec WeKnora-postgres-dev pg_isready -U postgres          # accepting connections
docker exec WeKnora-postgres-dev psql -U postgres -d WeKnora -c "SELECT extname FROM pg_extension"  # 含 pg_search, vector
docker exec WeKnora-redis-dev redis-cli -a "$REDIS_PASSWORD" ping  # PONG
```

## 构建 / 运行 app（Windows 原生）

CGO 三件套缺一不可（sqlite-vec + duckdb 都是 CGO 依赖）：

```bash
export PATH="/c/Program Files/Go/bin:/c/Users/Administrator/AppData/Local/Microsoft/WinGet/Packages/BrechtSanders.WinLibs.POSIX.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe/mingw64/bin:$PATH"
export CGO_ENABLED=1
export CGO_CFLAGS="-I$(pwd -W)/docker/dev/sqlite3-shim -IC:/Users/Administrator/go/pkg/mod/github.com/mattn/go-sqlite3@v1.14.24"
go build -o weknora-server.exe ./cmd/server
```

lite 模式配置（默认 `.env` 即 lite：sqlite + 内存流 + 本地存储，无需 postgres/redis 即可启动）：

```bash
set -a; source .env; set +a   # 关键：cmd/server 不读 .env（只有 cmd/desktop 用 godotenv），必须显式导出
./weknora-server.exe
curl localhost:8080/health    # 200，日志可见迁移 0→26
```

切全量模式：`.env` 改 `DB_DRIVER=postgres`、`DB_HOST=localhost`、`RETRIEVE_DRIVER=postgres`、`STREAM_MANAGER_TYPE=redis`（`.env.example` 有全量模板；中间件端口已由 dev compose 发布到 localhost）。

## 测试

```bash
go test ./internal/agent/...          # 带上面的 CGO env
cd frontend && npm run dev            # http://localhost:5173
cd tests/e2e && node smoke.spec.cjs   # 冒烟：前端 200 + 后端 /health 200 + 截图到 artifacts/
```

e2e 脚本约定：放 `tests/e2e/`（独立 package.json，playwright@1.63.0 已装，浏览器二进制共享 `~/AppData/Local/ms-playwright`）；截图等证据放 `tests/e2e/artifacts/`。

## 已知坑（都踩过）

1. **境内镜像站的 paradedb/paradedb:v0.22.6-pg17 是坏的**：entrypoint/gosu/postgres 主程序全为 0 字节（容器报 `exec format error`），1ms.run 与 daocloud 同源同 digest 同损坏。解决：`docker/dev/Dockerfile.paradedb` 自建（postgres:17 + pg_search .deb + pgvector 源码，资产预下载在 `docker/dev/`）。
2. **Docker Desktop 高负载崩溃**：并行构建/大镜像解压时 daemon EOF。已加 `.wslconfig`（memory=20GB, processors=8, swap=8GB）；构建任务串行执行，不并发。
3. **`docker compose` 必须加 `DOCKER_API_VERSION=1.47`**，否则 API 协商 500。
4. **Git Bash 路径转换**：`docker run` 参数里的 `/etc/...` 会被改写成 Windows 路径，`export MSYS_NO_PATHCONV=1` 或注意甄别（报 "cannot open 'C:/Program Files/Git/...'" 即是）。
5. **`gh` 的 POST 偶发 TLS 超时但 curl 正常**；超时的 POST 可能已生效，重试前先查重。
6. **fork 的 Issues 默认关闭**（410 Gone），已手动开启。
7. **`cmd/server` 不读 `.env`**（只有 `cmd/desktop` 用 godotenv）：原生运行前必须 `set -a; source .env; set +a`，否则报 `unsupported database driver: `（空值）。
