# Langfuse 集成

WeKnora 内置了对 [Langfuse](https://langfuse.com) 的轻量级集成，用于统计 token 消耗、追踪 LLM 调用链路、并为每个对话生成可在 Langfuse 控制台查看的 trace。该集成解决 issue [#497](https://github.com/Tencent/WeKnora/issues/497)（token 使用量统计）和 discussion [#620](https://github.com/Tencent/WeKnora/discussions/620)（接入 Langfuse）。

## 1. 特性

- 自动上报 **chat / embedding / rerank / VLM（视觉语言模型）/ ASR（语音识别）** 全部 5 类模型调用的 prompt、响应和 token 使用量。
- 为每个对话、检索、**文件上传及后续异步处理**创建一条端到端 **trace**。HTTP 请求是根，asynq 任务以 SPAN 的形式挂在同一条 trace 下，文档解析 → chunk embedding → 多模态 OCR/Caption → 摘要 / 问题生成全部在同一棵树里可见。
- 支持 **流式响应**：记录首 token 延迟（Time-To-First-Token），完整响应在流结束后一次性写入。
- **跨进程 trace 透传**：HTTP 层把 `trace_id` / `parent_observation_id` 注入 asynq payload，worker 在 asynq middleware 层自动 resume；定时任务（例如数据源同步）则退化为独立 trace，依然按任务类型（`asynq.<type>`）聚合。
- **完全可选**：不配置 `LANGFUSE_*` 环境变量时，Langfuse 相关代码路径是 no-op，不产生任何性能开销。
- **异步批量上报**：不阻塞业务请求；队列满时静默丢弃，观测数据不会影响用户对话。
- **开箱即用的部署方式**：Docker Compose（`docker-compose.yml` 已内置环境变量）、Helm Chart（通过 `extraEnv`）、Lite 版本（本地单机）均支持。

## 2. 快速开始

### 2.1 获取 Langfuse 凭证

1. 登录 [cloud.langfuse.com](https://cloud.langfuse.com) 或自建 Langfuse 实例。
2. 进入 `Project Settings → API Keys`，生成一对 `Public Key` / `Secret Key`。

### 2.2 按部署方式配置

#### （A）Docker Compose 部署（推荐）

`docker-compose.yml` 已经把所有 `LANGFUSE_*` 环境变量串到 `app` 服务。下面提供两种选择。

##### A-1) 接入 Langfuse Cloud（最简单）

只需要在 **`.env`** 里加 3 行：

```bash
LANGFUSE_PUBLIC_KEY=pk-lf-xxxxxxxx
LANGFUSE_SECRET_KEY=sk-lf-xxxxxxxx
LANGFUSE_HOST=https://cloud.langfuse.com    # 美区用 https://us.cloud.langfuse.com
```

然后重启服务：

```bash
docker compose up -d app
docker compose logs -f app | grep Langfuse
```

看到下面这行就说明已启用：

```
[Langfuse] enabled host=https://cloud.langfuse.com flush_at=15 flush_interval=3s sample_rate=1.00
```

##### A-2) 自建 Langfuse 栈（离线 / 内网 / 数据合规）

`docker-compose.yml` 内置了一个可选的 `langfuse` profile，用一条命令就能拉起 Langfuse v3。

**设计上已尽可能复用 WeKnora 已有容器，避免资源浪费**：

| 组件 | 来源 | 备注 |
| --- | --- | --- |
| PostgreSQL | 复用 `WeKnora-postgres` | 通过一次性的 `langfuse-db-init` 容器，在同一 pg 实例里创建独立的 `langfuse` 数据库。库级隔离，互不影响。 |
| Redis | 复用 `WeKnora-redis` | 使用独立的 Redis DB 号（默认 DB 1，WeKnora 用 DB 0）。`REDIS_CONNECTION_STRING` 指定 DB 后缀。 |
| ClickHouse | 新增 `langfuse-clickhouse` | Langfuse 专有（OLAP 事件存储），WeKnora 不用，必须独立。 |
| MinIO | 新增 `langfuse-minio` | 故意和 WeKnora 的 `minio` 分开（后者是可选 profile，未必激活；Langfuse S3 要专属 bucket）。 |
| Web / Worker | 新增 `langfuse-web` + `langfuse-worker` | Langfuse 应用本体。 |

最终 `--profile langfuse` 只新增 **4 个常驻容器 + 1 个一次性 init**，内存开销由原先的 ~1.5–2.5 GB 降到约 **1.0–1.5 GB**。

```bash
# 1. 启动自建栈（ClickHouse 首次迁移大约需要 1-2 分钟）
docker compose --profile langfuse up -d

# 2. 浏览器打开 http://localhost:3000 注册管理员账号
#    然后在 Project Settings → API Keys 生成 Public/Secret Key

# 3. 把 key 填回 .env 并把 HOST 改成容器内部地址
cat >> .env <<'EOF'
LANGFUSE_HOST=http://langfuse-web:3000
LANGFUSE_PUBLIC_KEY=pk-lf-xxxxxxxx
LANGFUSE_SECRET_KEY=sk-lf-xxxxxxxx
EOF

# 4. 让 app 重新加载配置
docker compose up -d app
```

> ⚠️ **生产部署安全提示**：`.env.example` 里的默认密码 / `SALT` / `ENCRYPTION_KEY` 都是开发占位符，生产环境必须用以下命令重新生成：
>
> ```bash
> echo "LANGFUSE_SALT=$(openssl rand -base64 32)"
> echo "LANGFUSE_ENCRYPTION_KEY=$(openssl rand -hex 32)"
> echo "LANGFUSE_NEXTAUTH_SECRET=$(openssl rand -base64 32)"
> ```
>
> 同时把 `LANGFUSE_DB_PASSWORD` / `LANGFUSE_CLICKHOUSE_PASSWORD` / `LANGFUSE_REDIS_PASSWORD` / `LANGFUSE_MINIO_PASSWORD` 全部换成强密码。完整变量清单见 `.env.example` 的 "Langfuse 自建栈配置" 段。

##### 通用调优

可选调优变量（`LANGFUSE_FLUSH_AT`、`LANGFUSE_SAMPLE_RATE` 等）都已经在 `docker-compose.yml` 中预设直通，只要在 `.env` 追加对应行即可生效。完整列表见 `.env.example` 的 Langfuse 段，或本文第 3 节。

##### 资源开销估算（A-2 自建方案）

| 组件 | 类型 | 典型 RSS | 备注 |
| --- | --- | --- | --- |
| langfuse-db-init | 一次性 | – | 创建 `langfuse` 数据库后立即退出 |
| langfuse-web | 常驻 | 300–500 MB | Next.js |
| langfuse-worker | 常驻 | 200–400 MB | Node.js，Queue consumer |
| langfuse-clickhouse | 常驻 | 500 MB–1 GB | 首次迁移稍高，稳态约 500 MB |
| langfuse-minio | 常驻 | 100–200 MB | |
| （复用）WeKnora-postgres | – | +~50 MB | 多一个 `langfuse` 数据库 |
| （复用）WeKnora-redis | – | +30–80 MB | 共用实例的 DB 1 |
| **新增合计** | | **≈ 1.0–1.5 GB** | 推荐 3 GB+ 可用内存 |

> 和"完全隔离各建一套 pg/redis"方案相比，这里节省了约 **400–500 MB** 内存。代价是 WeKnora 的 pg/redis 容量规划需要为 Langfuse 预留一点余量；Langfuse 写入量并不大（只是元数据 + 任务队列，事件主体走 ClickHouse），实际影响很小。

对单机部署而言，若只想使用 Langfuse Cloud 方案（A-1），**完全不需要**这些容器；原有服务 CPU/内存占用不变。

##### 生产环境下的注意事项

- **WeKnora-redis 的驱逐策略**：Langfuse 建议 `maxmemory-policy noeviction`（避免 Redis 在内存紧张时丢弃队列任务）。如果 WeKnora 的 redis 未配置该策略，建议在 `docker-compose.yml` 的 redis command 中加上 `--maxmemory-policy noeviction`。
- **备份**：`pg_dump -d langfuse` 可独立备份 Langfuse 的元数据；事件数据在 ClickHouse 卷（`langfuse_clickhouse_data`）中。
- **想彻底隔离**（跨机部署、强运维隔离）：可以直接把 `langfuse-web` / `langfuse-worker` 的 `DATABASE_URL` 和 `REDIS_CONNECTION_STRING` 指向任意外部 pg/redis（例如 RDS + ElastiCache）；`langfuse-db-init` 容器可以选择不启动，手动在目标 pg 上 `CREATE DATABASE langfuse` 即可。

#### （B）WeKnora Lite（单机）

在 `.env.lite`（或启动脚本导出的环境变量）里加：

```bash
LANGFUSE_PUBLIC_KEY=pk-lf-xxxxxxxx
LANGFUSE_SECRET_KEY=sk-lf-xxxxxxxx
LANGFUSE_HOST=https://cloud.langfuse.com
```

启动 `weknora-lite`（或 macOS `.app`）后效果同上。

#### （C）Helm Chart 部署

在 `values.yaml` 的 `app.extraEnv` 添加：

```yaml
app:
  extraEnv:
    - name: LANGFUSE_PUBLIC_KEY
      valueFrom:
        secretKeyRef:
          name: langfuse-credentials
          key: public_key
    - name: LANGFUSE_SECRET_KEY
      valueFrom:
        secretKeyRef:
          name: langfuse-credentials
          key: secret_key
    - name: LANGFUSE_HOST
      value: https://cloud.langfuse.com
```

建议把 Secret Key 放到 Kubernetes Secret 中，切勿写进 values.yaml。

#### （D）二进制 / 源码运行

```bash
export LANGFUSE_PUBLIC_KEY="pk-lf-xxxx"
export LANGFUSE_SECRET_KEY="sk-lf-xxxx"
export LANGFUSE_HOST="https://cloud.langfuse.com"
./weknora-server
```

#### （E）本地开发（`docker-compose.dev.yml` + `go run`）

`docker-compose.dev.yml` 只启动基础设施容器（postgres/redis/docreader 等），`app` 走本地 `go run ./cmd/server`。Langfuse 的两种接入方式：

**E-1) 直连 Langfuse Cloud（dev 最常见）**

无需改任何 compose 文件，本地 shell 导出即可：

```bash
export LANGFUSE_PUBLIC_KEY="pk-lf-xxxx"
export LANGFUSE_SECRET_KEY="sk-lf-xxxx"
export LANGFUSE_HOST="https://cloud.langfuse.com"
go run ./cmd/server
```

**E-2) 本地自建栈调试**

dev compose 也支持对称的 `langfuse` profile（复用同一个 dev postgres + redis）：

```bash
# 拉起基础设施 + Langfuse 栈
docker compose -f docker-compose.dev.yml up -d postgres redis docreader
docker compose -f docker-compose.dev.yml --profile langfuse up -d

# 浏览器打开 http://localhost:3000 注册并生成 key

# 本地 app 接入（注意是 localhost，不是 langfuse-web，因为 go run 跑在宿主机）
export LANGFUSE_HOST=http://localhost:3000
export LANGFUSE_PUBLIC_KEY=pk-lf-xxxxxxxx
export LANGFUSE_SECRET_KEY=sk-lf-xxxxxxxx
go run ./cmd/server
```

Dev 相关容器都带 `-dev` 后缀、用独立网络 `WeKnora-network-dev`，和生产 compose **不冲突**。

### 2.3 验证

发起一次知识问答（`POST /api/v1/knowledge-chat/:session_id`）或知识检索（`POST /api/v1/knowledge-search`）。等待 3 秒（或批量大小达到 `flush_at`）后，Langfuse 控制台的 **Traces** 页面会出现对应的 trace：

- 顶层节点：HTTP 请求（带 `userId` / `sessionId`）。
- 子节点依次为 rerank、chat、VLM 等具体模型调用，点击可查看 prompt、响应以及 usage（prompt/completion/total tokens）。
- 流式对话会额外标注 Time-To-First-Token。

## 3. 环境变量参考

| 变量名 | 默认值 | 说明 |
| --- | --- | --- |
| `LANGFUSE_ENABLED` | 自动 | 显式开关。未设置时，只要 `PUBLIC_KEY` + `SECRET_KEY` 都存在就自动启用。支持 `true/false/1/0/yes/no`。 |
| `LANGFUSE_HOST` | `https://cloud.langfuse.com` | Langfuse 实例地址。美区用 `https://us.cloud.langfuse.com`，自建实例填 `https://langfuse.your-domain.com`。 |
| `LANGFUSE_PUBLIC_KEY` | — | 项目 Public Key（`pk-lf-...`）。 |
| `LANGFUSE_SECRET_KEY` | — | 项目 Secret Key（`sk-lf-...`），请走密钥管理工具注入，不要提交到仓库。 |
| `LANGFUSE_RELEASE` | — | 可选，上报到 Langfuse 的版本号，例如 CI 构建号。 |
| `LANGFUSE_ENVIRONMENT` | — | 可选，环境标签（`production` / `staging` / `dev`），方便在 UI 过滤。 |
| `LANGFUSE_FLUSH_AT` | `15` | 批处理大小：缓冲区积累到该数量立即上报。 |
| `LANGFUSE_FLUSH_INTERVAL` | `3s` | 定时刷新间隔。支持 `500ms`、`5s`、`1m` 等 Go duration 写法；纯数字按秒处理。 |
| `LANGFUSE_QUEUE_SIZE` | `2048` | 内存队列容量。队列满时新事件会被静默丢弃（避免拖慢业务）。 |
| `LANGFUSE_REQUEST_TIMEOUT` | `10s` | 单次 HTTP ingest 请求超时。 |
| `LANGFUSE_SAMPLE_RATE` | `1.0` | 采样率 (0..1)。`0` 视为 `1.0`。高流量环境可下调。 |
| `LANGFUSE_DEBUG` | `false` | 打开后会在 WeKnora 日志里打印上报失败的详细原因，排障期间临时开启。 |

## 4. 观测数据说明

| Langfuse 概念 | WeKnora 对应 | 备注 |
| --- | --- | --- |
| Trace | 一次 HTTP 请求（含其触发的所有 asynq 任务） | 对于 `knowledge-chat`、`agent-chat`、`knowledge-search`、`generate_title`、`evaluation`、模型连通性测试等在线请求；以及文件上传/URL 入库/manual/reparse/move/copy、FAQ 导入、知识修改、wiki auto-fix、数据源手工触发等入库请求，HTTP 层都会开启 trace，并把 `trace_id` / `parent_observation_id` 注入 asynq payload。 |
| Span（type=SPAN） | 每个 asynq 任务的执行窗口 / 每次 Agent 执行及其每一轮 / 每次工具调用 | 由 `internal/tracing/langfuse/AsynqMiddleware` 在 `mux.Use` 注册；对每个 handler 自动创建 `asynq.<task_type>` 的 SPAN，并记录 `task_id` / `queue` / `retry` / `payload_bytes`。定时任务（无上游 trace）会退化为 `asynq.<task_type>` 独立 trace。**Agent 相关**：`AgentEngine.Execute` 会开 `agent.execute` 顶层 SPAN，其下每一轮 ReAct 循环开 `agent.round.N` SPAN，每次工具调用开 `agent.tool.<tool_name>` SPAN（参数、输出、耗时、成败、错误都会写入）。 |
| Generation（type=GENERATION） | 每次 chat / embedding / rerank / VLM / ASR 调用 | 若位于 span 下会自动设置 `parentObservationId`，所以 Langfuse UI 呈现 trace → asynq-span → generation 的树状结构；Agent 模式下是 trace → agent.execute → agent.round.N → (chat.completion.stream + agent.tool.X → rerank/embedding...) 的完整树。 |
| Input Tokens | `TokenUsage.PromptTokens` | 来自模型返回的 usage 字段。 |
| Output Tokens | `TokenUsage.CompletionTokens` | 来自模型返回的 usage 字段。 |
| Total Tokens | `TokenUsage.TotalTokens` | 大多数厂商返回；未返回时自动求和。 |
| `userId` | `X-User-ID` / 租户 ID | 未登录时退化为 `tenant:<id>`，方便按租户汇总消耗；enqueue 时会写入 payload，worker 在无上游 trace 的场景也能保留归属。 |
| `sessionId` | URL 中的 `:session_id`（或 `RequestID` 兜底） | 可以在 Langfuse 的 Sessions 视图聚合一整场对话，或按单次异步批次聚合。 |
| Time-To-First-Token | 流式调用首条有效 chunk 的时间 | 通过 `generation-update.completionStartTime` 上报。 |

### 覆盖到的 asynq 任务类型

下表列出当前会在 Langfuse 里自动出现对应 SPAN 的 asynq 任务；每种任务的 payload 均已嵌入 `types.TracingContext`，enqueue 时由 `langfuse.InjectTracing(ctx, &payload)` 从当前 HTTP trace 拷出 `trace_id` / `parent_observation_id`。

| 任务类型常量 | Handler | 典型触发来源 |
| --- | --- | --- |
| `document:process` | `knowledgeService.ProcessDocument` | 文件 / URL / 文本 / file_url 四种入库；reparse；知识库克隆内部重派发 |
| `manual:process` | `knowledgeService.ProcessManualKnowledge` | 手工知识新建 / 更新 |
| `image:multimodal` | `ImageMultimodalService.Handle` | 文档解析时发现图片 |
| `knowledge:post_process` | `KnowledgePostProcessService.Handle` | 文档解析完成后统一调度 summary/question |
| `summary:generation` / `question:generation` | `KnowledgePostProcessService` 子任务 | 由 `knowledge:post_process` 派发 |
| `chunk:extract` | `ChunkExtractor.Handle` | 图谱提取（NEO4J 启用时） |
| `datatable:summary` | `DataTableSummaryService.Handle` | 表格文件解析 |
| `faq:import` | FAQ 批量导入 handler | FAQ 导入 / 批量创建 |
| `knowledge:move` / `knowledge:list_delete` / `index:delete` / `kb:clone` / `kb:delete` | 知识移动 / 批量删除 / 索引清理 / 知识库复制 / 知识库删除 | 对应 HTTP 路由 |
| `wiki:ingest` | `wikiIngestService.ProcessWikiIngest` | Wiki auto-fix / 重建链接 |
| `datasource:sync` | `dataSourceSyncService.Handle` | 数据源手动触发 + 定时调度（定时场景下 trace 为 standalone） |

### 各模型的 usage 处理策略

| 模型类型 | 上报名称 | Token 计量方式 | 备注 |
| --- | --- | --- | --- |
| Chat | `chat.completion` / `chat.completion.stream` | 直接使用模型返回的 `prompt_tokens` / `completion_tokens` / `total_tokens` | 流式请求会记录 TTFT。 |
| Embedding | `embedding.embed` / `embedding.batch_embed` | 模型未返回 usage 时按 `rune_count/4 + 1` 估算 input tokens | 批量接口会上报批量大小和前 5 条文本预览，避免把整批内容塞进 trace。 |
| Rerank | `rerank` | 按 `query + 所有文档` 的 rune 数估算 input tokens | 输出只上报前 10 条 `(index, score)`。 |
| VLM | `vlm.predict` | prompt/result 分别按 `rune/4` 估算 input/output | 不上传原始图片字节；仅记录图片数量与总字节大小。 |
| ASR | `asr.transcribe` | 以 **秒**（`SECONDS`）为计量单位，取转录结果最后一个 segment 的 `end` 作为音频时长 | 便于 Langfuse 按"分钟"结算 Whisper 类 API。 |

> Tip：Langfuse 的 `Settings → Models` 页面可以为自定义模型（本地 Ollama、阿里云百炼等）配置单价（每 1K tokens、每分钟等），Langfuse 会据此自动核算费用。

## 5. 高流量部署建议

- **调高 `LANGFUSE_FLUSH_AT`** 到 50–100，降低 ingest HTTP 调用频率。
- **采样**：把 `LANGFUSE_SAMPLE_RATE=0.1` 只采样 10% 的对话，生产成本与信噪比通常能得到较好的平衡。
- **扩大 `LANGFUSE_QUEUE_SIZE`** 至 8192，防止短时峰值触发事件丢弃。
- 将 Langfuse 实例部署在离 WeKnora 同机房（例如自建 Langfuse + 内网地址），可以显著降低上报延迟。
- 打开 `LANGFUSE_DEBUG=true` 几分钟即可确认链路，生产环境常态下关闭，避免日志噪音。

## 6. 禁用

删除或留空 `LANGFUSE_PUBLIC_KEY` / `LANGFUSE_SECRET_KEY`，或显式设置 `LANGFUSE_ENABLED=false`，再重启服务即可。所有 Langfuse 相关代码路径会回退到 no-op，不会影响其他观测组件（OpenTelemetry、LLM Debug Log）。

## 7. 故障排查

| 现象 | 建议排查步骤 |
| --- | --- |
| 启动日志没有 `[Langfuse] enabled` | 检查 `LANGFUSE_PUBLIC_KEY` / `LANGFUSE_SECRET_KEY` 是否被服务进程读到；容器里可 `env \| grep LANGFUSE` 验证。 |
| 控制台看不到 trace | 打开 `LANGFUSE_DEBUG=true`，观察日志中是否有 `[Langfuse] flush ... failed`。常见原因：`LANGFUSE_HOST` 错误、企业防火墙拦截 HTTPS、Secret Key 轮换后未更新。 |
| 部分 chunk 缺失 | 调大 `LANGFUSE_QUEUE_SIZE`；确认 Langfuse ingest API 没有返回 429/503。 |
| token 数为 0 | 该模型在返回中未提供 usage（常见于部分本地 Ollama / 自建模型）。可在模型侧开启 usage 统计，或在 Langfuse 配置里为该模型提供 tokenizer。 |

## 8. 代码位置

- `internal/tracing/langfuse/` — Langfuse 客户端、异步批量上报、Gin 中间件、**asynq middleware**、Span / Trace resume 实现。
  - `tracer.go` — 暴露 `Trace` / `Span` / `Generation` + `StartTrace` / `StartSpan` / `StartGeneration` / `ResumeTrace`。
  - `asynq.go` — `AsynqMiddleware()` 统一在 mux 上包 handler；`InjectTracing(ctx, payload)` 在 enqueue 侧把 trace/span ID 注入 payload。
  - `middleware.go` — Gin 中间件 + `shouldTrace` 白名单（覆盖 chat / 入库 / FAQ / wiki / 数据源等路径）。
- `internal/types/tracing.go` — `TracingContext` POCO，所有 asynq payload 通过嵌入此结构携带 `lf_trace_id` / `lf_parent_obs_id` / `lf_user_id` / `lf_session_id`。
- `internal/models/chat/langfuse_wrapper.go` — Chat 调用装饰器（含流式）。
- `internal/models/embedding/langfuse_wrapper.go` — Embedding 调用装饰器。
- `internal/models/rerank/langfuse_wrapper.go` — Rerank 调用装饰器。
- `internal/models/vlm/langfuse_wrapper.go` — VLM（视觉语言模型）调用装饰器。
- `internal/models/asr/langfuse_wrapper.go` — ASR（语音识别）调用装饰器。
- `internal/agent/engine.go` — `agent.execute` 顶层 SPAN 和 `agent.round.<N>` 每轮 SPAN。
- `internal/agent/act.go` — `agent.tool.<tool_name>` 工具调用 SPAN（包含参数、输出、耗时、成败）。
- `internal/router/router.go` — 注册 `langfuse.GinMiddleware()`。
- `internal/router/task.go` — 在 asynq mux 上 `mux.Use(langfuse.AsynqMiddleware())`，使所有 handler 自动被 trace。
- `internal/container/container.go` — 初始化 + 资源清理。
- `docker-compose.yml` / `.env.example` / `.env.lite.example` — 预置 `LANGFUSE_*` 环境变量直通。
