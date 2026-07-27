# WeKnora 配置清单

## 一、本次实际配置

本次只新增本地 `.env`（已被 `.gitignore` 忽略），未修改上游受版本控制的配置文件。目的为按官方 Docker Compose 核心路径启动，且不写入任何第三方 API Key。

App、DocReader 与 Frontend 均已从当前工作区源码/构建产物生成本地 `latest` 镜像；因此下表中的 `WEKNORA_VERSION=latest` 在本机解析到本次构建的 image ID，而不是仅证明远端稳定镜像可运行。

| 文件 | 是否修改 | 用途 | 本次处理 |
|---|---|---|---|
| `.env` | 新增（不入 Git） | Compose 插值与 App 运行环境 | 随机生成 DB/Redis/JWT/Tenant/System 本地密钥；使用本地存储；关闭 Langfuse |
| `.env.example` | 未修改 | 全量配置模板 | 发现非空 Langfuse 占位 Key 风险，建议上游改为空 |
| `docker-compose.yml` | 未修改 | 核心与可选 profile 编排 | `docker compose config --quiet` 已通过 |
| `config/config.yaml` | 未修改 | 后端主配置与 Prompt/服务默认项 | 以只读代码审计为主，Compose 将其挂载到 App |

`.env` 中实际秘密不会写入本文。当前非秘密配置为：

```dotenv
WEKNORA_VERSION=latest
GIN_MODE=release
LOG_LEVEL=info
TZ=Asia/Shanghai
WEKNORA_LANGUAGE=zh-CN
WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL=x17674728682@gmail.com

DB_DRIVER=postgres
DB_HOST=postgres
DB_PORT=5432
DB_USER=postgres
DB_NAME=WeKnora

STORAGE_TYPE=local
LOCAL_STORAGE_BASE_DIR=/data/files

REDIS_ADDR=redis:6379
REDIS_DB=0
REDIS_PREFIX=stream:

OLLAMA_BASE_URL=http://host.docker.internal:11434
LANGFUSE_ENABLED=false
LANGFUSE_PUBLIC_KEY=
LANGFUSE_SECRET_KEY=

WEKNORA_SANDBOX_MODE=docker
WEKNORA_MODEL_MAX_CONCURRENCY=8
SSRF_WHITELIST_EXTRA=searxng,qdrant,milvus,weaviate,doris-fe,dashscope.aliyuncs.com
```

`WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL` 用于官方一次性 bootstrap：仅当平台尚无 System Admin 时，将已注册的匹配邮箱晋升为平台管理员；账户 `admin` 已于本次配置后完成晋升。该账户在租户 `10000` 中同时保持 `owner` 角色。

`SSRF_WHITELIST_EXTRA` 保留 Compose 默认内部服务，并额外精确放行 `dashscope.aliyuncs.com`。原因是当前宿主机网络代理将该可信阿里云域名解析到 Fake-IP 网段 `198.18.0.0/15`；未放行通配域名或整个 CIDR。

必须存在但不应复制到文档或 Git 的值：`DB_PASSWORD`、`REDIS_PASSWORD`、`JWT_SECRET`、`TENANT_AES_KEY`、`SYSTEM_AES_KEY`。生产环境应从 Secret Manager 或编排平台 Secret 注入。

## 二、不同目标下需要修改的配置文件

### 2.1 本地核心启动（本次路径）

必须检查：

1. `.env`：数据库、Redis、JWT/加密密钥、存储、镜像版本。
2. `docker-compose.yml`：通常无需修改；仅端口冲突或远程后端时覆盖。
3. `config/config.yaml`：仅调整服务默认行为、Prompt 路径或高级后端配置时修改。

验证命令：

```bash
docker compose config --quiet
docker compose up -d
docker compose ps
curl -fsS http://localhost:8080/health
curl -fsSI http://localhost/
```

### 2.2 配置内置模型

需要：

1. 复制并修改 `config/builtin_models.yaml.example` 为 `config/builtin_models.yaml`。
2. 在 `.env` 中设置 YAML 所引用的模型名、Base URL、Provider 与 API Key 环境变量。
3. 在 `docker-compose.yml` 的 App volumes 中按注释启用只读挂载。

不要把真实 API Key 写进 YAML。模型维度、thinking mode、超时与最大并发需与供应商能力一致。参考 `docs/BUILTIN_MODELS.md`。

### 2.3 启用可选 Docker Compose Profile

| 能力 | 命令/Profile | 需要检查的文件或变量 |
|---|---|---|
| Neo4j/GraphRAG | `--profile neo4j` | `.env`：`NEO4J_*`、`ENABLE_GRAPH_RAG`；`docker-compose.yml` |
| MinIO | `--profile minio` | `.env`：`STORAGE_TYPE=minio`、`MINIO_*` |
| Langfuse | `--profile langfuse` | `.env`：真实 `LANGFUSE_*`、初始化账号与强秘密 |
| SearXNG | `--profile searxng` | `.env`：绑定地址、端口、随机 `SEARXNG_SECRET`；`docker/searxng/` |
| Qdrant | `--profile qdrant` | `.env`：`QDRANT_*`、API Key/TLS |
| Milvus | `--profile milvus` | `.env`：`MILVUS_*` 与资源规格 |
| Weaviate | `--profile weaviate` | `.env`：`WEAVIATE_*` 与鉴权 |
| Doris | `--profile doris` | `.env`：`DORIS_*`；持久卷与资源 |
| ODL Hybrid | `--profile odl-hybrid --build` | `.env`：有效的 hybrid backend 名称（如 `DOCREADER_ODL_HYBRID=docling-fast`）、`DOCREADER_ODL_*`；`docker/Dockerfile.odl-hybrid` |
| Full 聚合 | `--profile full` | 包含 Sandbox、SearXNG、MinIO、Neo4j、Qdrant、Dex、Langfuse、MCP 及核心服务；ODL Hybrid、Milvus、Weaviate、Doris 仍需分别追加 profile；8 GB Docker 内存不建议启用 |

### 2.4 本地源码开发模式

需要检查：

- `.env`：手工启动各进程时将后端连接地址改为宿主映射，如 `DB_HOST=localhost`、`DOCREADER_ADDR=localhost:50051`、`REDIS_ADDR=localhost:6379`；使用标准 `scripts/dev.sh` 时脚本会临时覆盖这些地址，通常无需永久改写 `.env`。
- `docker-compose.dev.yml`：基础设施端口、服务与 profile。
- `frontend/vite.config.ts`：开发代理目标应指向 `http://localhost:8080`。
- `.air.toml`：可选 Go 热重载。
- `scripts/dev.sh`、`Makefile`：标准启动入口，不应硬编码秘密。

宿主机当前没有 Go，因此本次不修改系统环境；源码 Go 验证使用 Go 容器。Node 22/npm 10、Python/uv 已存在。

### 2.5 Lite / Desktop

需要检查：

- `.env.lite.example`：SQLite、内存队列、本地存储与 Lite 能力。
- `docs/LITE.md`：构建和已知能力差异。
- `frontend/`：Lite 二进制嵌入的静态产物。
- `scripts/package-lite.sh`、`scripts/package-mac-app.sh`、`Formula/weknora-lite.rb`、`deploy/weknora-lite.service`：分发目标专用配置。

### 2.6 Kubernetes / Helm

需要修改或覆盖：

- `helm/values.yaml`：镜像 tag、replica、资源、PVC、Ingress、外部依赖。
- Kubernetes Secret 或 `secrets.existingSecret`：DB/Redis/JWT/API Key，不应明文提交 values。
- `helm/templates/`：一般不直接修改，除非新增平台能力。

上线前运行：

```bash
helm lint ./helm
helm template weknora ./helm --set secrets.dbPassword=x --set secrets.redisPassword=x --set secrets.jwtSecret=x >/dev/null
```

当前配置存在版本漂移：`helm/values.yaml` 的 ParadeDB 默认值及 Helm README 示例为 `v0.18.9-pg17`，Compose 为 `v0.22.2-pg17`，生产前必须统一并验证迁移兼容。

## 三、需要按集成修改的配置

| 集成 | 配置位置 | 关键要求 |
|---|---|---|
| Feishu/Notion/Yuque/RSS | 前端数据源设置与数据库加密凭证；必要时 `.env` 网络策略 | 最小权限、回调/限流、凭证轮换 |
| WeCom/Feishu/Slack/Telegram/钉钉等 IM | 前端集成中心、回调地址、平台密钥 | HTTPS、验签、防重放、可达性 |
| Website Embed | Embed Channel 设置、反向代理、可信代理/CORS/CSP | 域名白名单、secure mode、真实客户端 IP |
| OIDC | `.env`/平台设置、IdP 回调 | issuer/client/secret、回调 URI、时钟同步 |
| 对象存储 | `.env` 或平台存储配置 | Bucket 权限、endpoint、public endpoint、备份 |
| 外部向量库 | 平台 Vector Store 设置与 `.env` allowlist | TLS/鉴权、维度/metric、SSRF allowlist |
| Langfuse Cloud/自建 | `.env` 与 Compose profile | Public/Secret Key 必须成对，默认关闭 |

## 四、安全配置规则

1. `.env`、`config/builtin_models.yaml`（含真实环境值时）、小程序 private config 和本地 profile 均不得提交。
2. 生产环境不得使用示例密码、固定 JWT、固定 AES Key、默认 SearXNG/Langfuse 秘密。
3. `scripts/check-env.sh` 当前会 `source .env` 并回显秘密值，不建议在生产或不可信 `.env` 上运行；应先完成安全改造。
4. 公网入口必须使用 TLS；设置可信代理、域名白名单、CSP/CORS、SSRF allowlist 和上传大小限制。
5. 密钥轮换前确认历史密文可解密；数据库与文件卷必须协同备份。

## 五、配置验收

- `docker compose config --quiet` 无错误。
- Git 状态不出现 `.env` 或真实秘密。
- 未配置的可选服务不被占位值误启用。
- 核心服务健康；端口、镜像 tag、数据库与 Redis 密码实际一致。
- 生产配置通过弱默认值、空秘密、外部地址、TLS 和备份检查。
