# WeKnora Lite

零外部依赖的单二进制部署模式。无需 Docker、PostgreSQL、Redis，适合快速体验和小规模私有部署。

## 架构

| 组件 | 标准版 | Lite 版 |
|------|--------|---------|
| 数据库 | PostgreSQL | SQLite (WAL) |
| 向量检索 | pgvector / Qdrant / ES | sqlite-vec (vec0) |
| 关键词检索 | ParadeDB BM25 / ES | SQLite FTS5 |
| 消息队列 | Redis + Asynq | 内存 SyncTaskExecutor |
| 会话存储 | Redis | 内存 |
| 流管理 | Redis / 内存 | 内存 |
| 文件存储 | MinIO / COS / 本地 | 本地 |
| 文档解析 | DocReader (gRPC) | 不可用（文本/段落导入可用）|
| 前端 | Nginx 容器 | Go 内置静态文件服务 |

## 快速开始

### 方式一：Homebrew 安装（macOS / Linux，推荐）

```bash
brew tap Tencent/weknora https://github.com/Tencent/WeKnora
brew install weknora-lite
```

安装完成后，推荐使用 **brew services** 以后台服务方式运行：

```bash
brew services start weknora-lite    # 启动服务（开机自动启动）
brew services info weknora-lite     # 查看运行状态
# 首次启动自动创建配置文件 ~/.config/weknora/.env.lite
# 数据存储在 ~/.local/share/weknora/
# 访问 http://localhost:8080
```

常用服务管理命令：

```bash
brew services stop weknora-lite     # 停止服务
brew services restart weknora-lite  # 重启服务（修改配置后需重启）
brew services info weknora-lite     # 查看状态
```

日志位于 `$(brew --prefix)/var/log/weknora-lite.log`。

也可以前台直接运行：

```bash
weknora-lite
```

如需修改配置（LLM 服务地址、安全密钥等）：

```bash
$EDITOR ~/.config/weknora/.env.lite
brew services restart weknora-lite  # 修改配置后重启生效
```

> **LLM 服务**：WeKnora Lite 需要一个 OpenAI 兼容的 LLM 服务来提供对话和 Embedding 能力。
> 可以使用 [Ollama](https://ollama.com/)（本地）、通义千问、OpenAI 等任何兼容服务，
> 在配置文件中设置对应的地址和 API Key 即可。

### 方式二：下载预编译包

从 [GitHub Releases](https://github.com/Tencent/WeKnora/releases) 下载对应平台的 tarball：

| 文件 | 平台 |
|------|------|
| `WeKnora-lite_*_linux_amd64.tar.gz` | Linux x86_64 |
| `WeKnora-lite_*_linux_arm64.tar.gz` | Linux ARM64 |
| `WeKnora-lite_*_darwin_amd64.tar.gz` | macOS Intel |
| `WeKnora-lite_*_darwin_arm64.tar.gz` | macOS Apple Silicon |

```bash
# 1. 解压
tar xzf WeKnora-lite_v0.2.0_darwin_arm64.tar.gz
cd WeKnora-lite_v0.2.0_darwin_arm64

# 2. 配置
cp .env.lite.example .env.lite
# 编辑 .env.lite，配置 LLM 服务地址和安全密钥

# 3. 运行
set -a && source .env.lite && set +a
./WeKnora-lite
# 访问 http://localhost:8080
```

### 方式三：从源码构建

前置条件：Go 1.22+（需要 CGO）、C 编译器 (gcc/clang)、Node.js 22+（前端构建）。

```bash
make run-lite
```

## 配置

Lite 模式通过 `.env.lite` 文件配置（模板见 `.env.lite.example`）。关键环境变量：

```bash
DB_DRIVER=sqlite          # 使用 SQLite
DB_PATH=./data/weknora.db # 数据库文件路径
RETRIEVE_DRIVER=sqlite    # SQLite 检索引擎 (FTS5 + sqlite-vec)
STORAGE_TYPE=local        # 本地文件存储
LOCAL_STORAGE_BASE_DIR=./data/files
STREAM_MANAGER_TYPE=memory # 内存流管理
# REDIS_ADDR=             # 留空 = 不使用 Redis
OLLAMA_BASE_URL=http://127.0.0.1:11434
```

完整配置参见 [.env.lite.example](../.env.lite.example)。

## 后台运行

### Homebrew 用户（macOS / Linux）

Homebrew 安装后直接使用 `brew services` 管理，详见上方「快速开始 → 方式一」。

### Linux systemd（tarball 安装）

tarball 中附带 `weknora-lite.service` 模板，按需修改路径后安装：

```bash
# 创建用户和目录
sudo useradd -r -s /sbin/nologin weknora
sudo mkdir -p /opt/weknora/data
sudo cp WeKnora-lite web/ .env.lite /opt/weknora/
sudo chown -R weknora:weknora /opt/weknora

# 安装并启动服务
sudo cp weknora-lite.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now weknora-lite

# 管理
sudo systemctl status weknora-lite   # 查看状态
sudo journalctl -u weknora-lite -f   # 查看日志
```

## 功能限制

与标准版相比，Lite 版有以下限制：

- **文档解析**：不支持文件上传和 URL 导入的自动解析（PDF/Word/Excel 等）。可使用文本和段落方式手动导入。
- **向量检索**：sqlite-vec 使用精确 KNN（非近似），适合 10 万条以下的小规模数据集。
- **并发**：SQLite 单写者模型，高并发写入场景下性能不如 PostgreSQL。
- **任务队列**：无持久化队列，进程重启后未完成的异步任务会丢失。
- **知识图谱**：默认禁用 (`NEO4J_ENABLE=false`)。
- **Agent Skills 沙箱**：默认禁用 (`WEKNORA_SANDBOX_MODE=disabled`)。

## 数据目录

默认所有数据存储在 `./data/` 目录下：

```
data/
├── weknora.db        # SQLite 数据库
├── weknora.db-wal    # WAL 日志
└── files/            # 上传文件
```

备份只需复制整个 `data/` 目录。
