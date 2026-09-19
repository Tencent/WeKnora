# 外部插件目录（生产容器化部署用）

WeKnora 的 Go 应用（`app` 容器）从这里加载**外部插件**。插件按扩展类型放在对应子目录：

```
plugins/
├── datasource/   # 数据源插件（dingtalk、github、localdir …）
├── parser/       # 文档解析插件
├── search/       # 网络搜索插件
├── model/        # 模型插件
└── retriever/    # 检索引擎插件
```

## 部署要求

1. **每个插件是一个独立子目录**，内含 `plugin.yaml` + 可执行二进制。
2. **二进制必须是 Linux/amd64**（`GOOS=linux GOARCH=amd64 go build`），并且有可执行权限。
3. 目录由 `docker-compose.yml` 挂载进 `app` 容器为 `/app/plugins`（只读），
   容器内通过 `WEKNORA_PLUGIN_DIR_*` 环境变量指向各类型子目录。
4. `plugins/` 已被 `.dockerignore` 排除——它只在运行期挂载，不会打进 app 镜像。

## 覆盖默认路径

`docker-compose.yml` 给每个类型一个默认容器内路径 `/app/plugins/<type>`。
如果某类不需要插件，在 `.env` 里把对应变量置空即可（空值 = 不扫描该类型）：

```bash
WEKNORA_PLUGIN_DIR_MODEL=
```

## 说明

- **offline / trusted（ProcessRuntime）** 插件作为 `app` 容器的子进程运行，插件与 app
  共享容器边界（协作式安全，非强隔离）。
- **isolated（DockerRuntime 强隔离）** 插件容器由独立的 **plugin-runtime agent** 启动，
  而非 app 容器：app 不挂载 `docker.sock`、不安装 docker CLI，只通过共享卷 `plugin-runtime`
  上的 Unix socket 调用 agent。agent 是唯一持有 Docker 权限的组件，负责 `--network none`
  容器、每插件 egress 代理与 socket 目录管理。插件容器仍无网卡，联网只能经 egress 代理。
- 生产务必设置 `WEKNORA_PLUGIN_RUNTIME_AGENT_TOKEN`（app 与 agent 之间的共享认证令牌）；
  未设置时仅依赖 Unix socket 文件权限（`0600`），只适合单机受信环境。
