# Go 后端容器远程调试

WeKnora 提供独立的 `docker-compose.debug.yml`，用于在容器内以 Delve 启动 Go 后端，并从宿主机的 GoLand 等 IDE 连接断点。PostgreSQL、Redis 与 DocReader 仍复用 `docker-compose.dev.yml`，源码通过 bind mount 实时映射，不需要每次构建发布镜像。

## 1. 启动调试服务

先准备普通开发环境变量并确认 Docker 可用：

```bash
cp .env.example .env
make dev-debug
```

该命令会完成以下动作：

1. 构建 `docker/Dockerfile.debug`，安装固定版本的 [Delve v1.27.1](https://github.com/go-delve/delve/releases/tag/v1.27.1)；
2. 启动 PostgreSQL、Redis、DocReader 与 `app-debug`；
3. 把仓库根目录映射到容器 `/workspace`，并为 Go module/build cache 使用独立数据卷；
4. 在容器内执行 `dlv debug ./cmd/server`，等待 IDE 连接后再继续程序。

默认调试地址为 `127.0.0.1:40000`，HTTP API 仍映射到 `127.0.0.1:8080`。如端口冲突，可在命令前覆盖宿主机端口：

```bash
DELVE_PORT=2345 APP_PORT=18080 make dev-debug
```

也可以不经过 Makefile，直接运行：

```bash
docker compose \
  -f docker-compose.dev.yml \
  -f docker-compose.debug.yml \
  up --build app-debug
```

按 `Ctrl+C` 停止本次前台调试；需要清理整个开发 Compose project 时运行 `make dev-stop`。

## 2. 连接 IDE

以 GoLand 为例，新建 **Go Remote** 配置：

- Host：`127.0.0.1`
- Port：`40000`（或自定义的 `DELVE_PORT`）
- 本地项目根目录映射到远程 `/workspace`

启动 `make dev-debug`，等待终端出现 Delve 监听信息后运行该配置。连接成功时进程仍停在入口；在 IDE 中继续执行即可命中断点。Delve 使用 headless API v2 并允许同一开发会话重新连接，具体命令语义见 [Delve `debug` 官方说明](https://github.com/go-delve/delve/blob/master/Documentation/usage/dlv_debug.md)。

## 3. 配置与可选服务

`app-debug` 默认读取仓库根目录的 `.env`。如需隔离调试配置，可指定另一个完整环境文件：

```bash
WEKNORA_DEBUG_ENV_FILE=.env.debug make dev-debug
```

向量库、MinIO、Neo4j 等仍由开发 Compose profile 管理。先按需运行例如 `make dev-start DEV_ARGS=--qdrant`，再启动调试服务；`app-debug` 与这些容器共享 `WeKnora-network-dev`。

调试镜像默认不链接可选的 anydoc Rust 静态库，适合排查 Go 后端主链路。需要调试 anydoc FFI 时，应在宿主机按[开发指南](./01-dev-guide.md)构建静态库并使用本地 `make dev-app` 调试。

## 4. 安全与故障排查

Delve 远程协议不提供传输认证，因此 Compose 默认把端口限制在宿主机 loopback。不要在不可信网络中把 `DELVE_BIND` 改成 `0.0.0.0`。调试容器额外启用 `SYS_PTRACE` 与 `seccomp:unconfined`，只应用于本地 `app-debug` 服务，不应作为生产部署配置。

常见问题：

- **断点显示未绑定**：确认源码路径映射为“本地仓库根目录 → `/workspace`”，并等待首次 CGO 编译完成。
- **端口已占用**：设置新的 `DELVE_PORT` 或 `APP_PORT` 后重试。
- **依赖配置未生效**：确认 `WEKNORA_DEBUG_ENV_FILE` 指向完整文件；服务名地址会在容器内固定为 `postgres`、`redis` 与 `docreader`。
- **构建依赖下载失败**：通过 `GOPROXY` 覆盖 Go module 代理；必要时设置 `DELVE_VERSION` 使用经验证的其他 Delve 版本。
