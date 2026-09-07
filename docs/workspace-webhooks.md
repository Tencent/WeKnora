# 工作空间知识事件回调

**状态：** 已确认  
**日期：** 2026-09-02  
**对象：** WeKnora 维护者与对接方

WeKnora 应把本工作空间**自己拥有的**知识库、知识、成员变化，推到 Owner 配置的 HTTPS 地址。现在对接方只能轮询列表 API。网页嵌入里的聊天 Webhook 是另一套协议（会话消息、只签 body、不重试），不能复用。

本文随功能一起合入。对接方协议与 JSON 示例见 `website-docs/03-features/22-workspace-webhooks.md`。

---

## 1. 开源项目为什么需要

自托管 WeKnora 经常要接到外部检索、归档或其它产品，这些系统必须跟上知识生命周期：

- 文档的创建 / 解析完成 / 解析失败 / 删除
- 知识库的创建 / 删除（删库是一条事件，不是 N 条文档事件）
- 工作空间成员的加入 / 移除（`tenant_members`）

没有出站事件时，每个对接方都要自己做轮询，会漏掉 worker 侧的 `parse_completed`，并且为了下源文件不得不持有长期 API Key。

**成功标准：** 上传立刻发出 `knowledge.created`（可带下载票，此时不可检索）；worker 发出 `parse_completed`（可以早于 `created`）；单条删除、批量删除、解析失败、建库删库、成员进出都能投到；多回调、HMAC、SSRF 可用；投递失败不回滚业务写入；至少一次投递以 outbox 为真源。

---

## 2. 架构

```text
  用户 / API / 数据源 / asynq worker
           │  业务写入成功
           ▼
  WorkspaceEventSink.Emit
           │  SubscriptionIndex：该空间是否有 enabled 且订阅该 type 的端点
           │  Redis SET（weknora:webhook:sub:{tenant_id}）命中则短路；
           │  miss → DB 重建；未订阅 → return（不写 outbox）
           ▼
           │  INSERT tenant_webhook_outbox（失败重试 3 次，仍不回滚业务）
           ▼
  Dispatcher（同进程试一次 + 每 10s 扫 pending）
           │  只查资源所属工作空间的端点
           │  enabled 且 events 包含该 type
           │  INSERT delivery 认领 (event_id, endpoint_id)
           │  asynq.Enqueue TaskID=wh:{event_id}:{endpoint_id}
           ▼
  QueueWebhook + WorkerPoolWebhook（独立池，对齐 Wiki）
           │  in-flight < 20，否则 ErrWebhookInFlightBusy
           │  现签下载票 → HMAC(timestamp + "." + body) → HTTPS POST
           ▼
  Owner 配置的接收端（每空间最多 5 个 URL）
```

原则：

1. 业务写入不同步等待出站 HTTP。**Emit = 通过订阅门控后写 outbox**；未配置（或未订阅该 type）的租户不产生 outbox 行。
2. 没有 outbox 行不许声称至少一次。asynq 只是投递器。接收方按包络 `id` 去重（不是 exactly-once）。
3. 审计给人看，webhook 给机器。
4. 订阅索引用 Redis 缓存 enabled 端点的 events 并集；端点 Create/Update/Delete 后 `DEL` 并 warm。Redis 不可用时降级查 DB。`DispatchTest` 绕过门控。

不采用：

| 方案 | 原因 |
|------|------|
| Gin 中间件 | `parse_completed` 发生在 worker（`finalizeIndexedKnowledgeState`、`FinalizeSubtask` 且 `promoted==true`）。HTTP 拦截会漏。 |
| RAG `EventBus` | 每请求一份，服务问答 SSE；出错会打断回答。 |
| `tenants` JSONB / KV | 多端点、密钥、过滤、投递历史需要独立表；密钥不能出现在 `GET /tenants/:id`。 |
| 包装整个 `KnowledgeRepository` | 30+ 方法，以后每加方法都要转调。 |
| `QueueMaintenance`（`"low"`） | 与删库/clone 同池；10s HTTP 重试会拖维护任务。 |
| 内存总线当真源 | HTTP 与 worker 跨进程。真源是 outbox + sweep。 |
| 复用 Embed HMAC | Embed 只签 body、不重试。工作空间签 `timestamp + "." + raw_body`。两套函数分开。 |
| 旁路 `GET /knowledge/:id/download` | 该路由是 Contributor+ / `KBAccessWrite`。把 `/api/v1/knowledge/` 加白名单会放开整个知识 API。 |

---

## 3. P0 投递范围

只投给**资源所属工作空间**的端点。包络 `tenant_id` = 知识/库行 `tenant_id` = 票内 `tenant_id`。

P0 **不**扇出到组织成员空间，**不**发 `kb.share_*`。仍保留 `data.owner_tenant_id`（P0 与包络 `tenant_id` 相同），便于以后扇出时不用改字段名。票始终绑定知识行的 `TenantID`。

---

## 4. P0 事件目录

接收方**只按**顶层 `type` 分支。

| `type` | 何时发出 | 接收方典型动作 |
|--------|----------|----------------|
| `knowledge.created` | 知识行插入成功（文件 / URL / 手工 / passage / clone） | 目录占位；可下载；**不可检索** |
| `knowledge.parse_completed` | `parse_status` **转入** `completed`（含重解析）。流水线会把 `enable_status` 设为 `enabled`。**不是**用户点了启用 | upsert 为可检索；可早于 `created` |
| `knowledge.parse_failed` | 变为 `failed` | 标记失败 |
| `knowledge.deleted` | **单条**知识行软删成功 | 按 `knowledge_id` 删除 |
| `knowledge.batch_deleted` | `DeleteKnowledgeList` 实际删到 `len>1`。每条最多 100 个 id，**禁止省略** | 按 `knowledge_ids` 删除 |
| `kb.created` | 建库成功且 `!is_temporary` | 建库节点 |
| `kb.deleted` | 知识库软删成功（清理已入队） | 丢掉该库及库内知识。**只投拥有空间** |
| `rbac.member_added` | `tenant_members` 插入新行 | 给该用户开通本空间 |
| `rbac.member_removed` | 成员被移除或自己离开（`data.reason`） | 收回该用户；库和文档还在 |
| `webhook.test` | 设置页点「发送测试」 | 只验通 |

不要发：

- FAQ 容器壳（`Type == faq`，`ensureFAQKnowledge` / `getOrCreateFAQKnowledge`）
- 临时知识库及其文档
- `EnsureOwner`（注册/建空间时的幂等 Owner）
- HTTP 只入队删除任务时（等行真正删掉再发）
- `ProcessKBDelete` 里的文档删除（已有 `kb.deleted`）
- 已经是 `completed`、只改了其它列的 Update
- `FinalizeSubtask` 且 `promoted==false`

`kb.created`：非临时库在 `repo.CreateKnowledgeBase` 成功之后发（`CreateKnowledgeBase`、`DuplicateKnowledgeBase`、`CopyKnowledgeBase` 新建行）。拷进**已有**库不发。

删库 = **一条** `kb.deleted`，`cascade_knowledge=true`。`ProcessKBDelete` 必须继续走仓储，不得改成 `knowledgeService.DeleteKnowledgeList`。

`knowledge.batch_deleted`：先按 `knowledge_base_id` 分组，再每 100 条切一片。各片 `knowledge_ids` 并集 = 本次实际删掉的全部 id。`truncated` 恒为 `false`。同一次调用不要再为每条 id 发 `knowledge.deleted`。

跨库移动（`moveKnowledgeReparse`）：`deleted`（旧库 id）+ `created`（新库 id）。接收方看 `knowledge_base_id`；迟到的 `deleted` 不得清掉已经 upsert 到新库的行。

P0 允许乱序。去重键是包络 `id`。需要排序时用 `time`。

---

## 5. 包络

字段集合固定，始终出现：

`spec_version` / `id` / `type` / `time` / `tenant_id` / `actor_user_id` / `request_id` / `data`

`spec_version` 为 `"1"`。按 `type` 分支。`data.resource` 为 `knowledge` | `knowledge_base` | `member` | `webhook`。同一 `resource` 下 `data` 的 key 集合固定，空值也会出现。

worker 完成的事件里，`actor_user_id` / `request_id` 常为空串。

---

## 6. HTTP 投递与 HMAC

WeKnora 作为 HTTP **客户端**：

```http
POST /hooks/weknora HTTP/1.1
Content-Type: application/json
User-Agent: WeKnora-Workspace-Webhook/1.0
X-WeKnora-Event: knowledge.created
X-WeKnora-Delivery: dlv_...
X-WeKnora-Timestamp: 1756624860
X-WeKnora-Signature: sha256=...
```

| Header | 用途 |
|--------|------|
| `X-WeKnora-Event` | 等于 body `type`，可不解析 body 就分流 |
| `X-WeKnora-Delivery` | 本次投递；**asynq 重试时不变**。业务去重用 body `id` |
| `X-WeKnora-Timestamp` | Unix 秒，参与签名 |
| `X-WeKnora-Signature` | `sha256=` + hex(HMAC-SHA256(密钥, **`timestamp + "." + 原始 body`**)) |

必须用**原始字节**验签。不要先 parse JSON 再序列化。`|now - timestamp| > 300` 秒则拒绝。

接收方：验签 → 按 `id` 入队 → **10 秒内返回 2xx**。不要在 webhook handler 里同步下大文件。响应 body 忽略。

HTTP：2xx 成功；408/429/5xx 重试（MaxRetry=5）；其它 4xx 立刻 `SkipRetry` 标失败。

`events` 必须是非空数组，元素为当时已登记的 `type`。空数组**不是**订阅全部。测试按钮绕过过滤。

---

## 7. 源文件下载票

文件类事件可带 `data.download.ticket`（5 分钟）。这**不是**空间 API Key。

```http
GET /api/v1/files/knowledge-download/:id
X-WeKnora-Download-Ticket: wdt1....
```

过期后 1 小时内可换票：

```http
POST /api/v1/files/knowledge-download/:id/renew
X-WeKnora-Download-Ticket: <旧票>
```

`noAuthAPI`：GET/HEAD 用**前缀**；POST **只**放行 `.../renew` 后缀。不要把 `/api/v1/knowledge/` 加白名单。本票面只认票，带 JWT / API Key 但不带票仍 401。现网 `GET /knowledge/:id/download` 不改（仍要 Contributor+）。

票用 HMAC 绑定 `purpose=knowledge_download` + `knowledge_id` + 归属 `tenant_id` + 过期时间，密钥为 `SystemHMACKey()`。**每次出站 POST 前现签**，重试拿到新票。票不写入投递表。

| `knowledge_type` | `download.available` | 怎么取内容 |
|------------------|----------------------|------------|
| `file` / `file_url` | 已配 `SYSTEM_AES_KEY` 时为 `true` | 票 + `path` |
| `url` | `false`，`reason=not_a_file` | `data.source` |
| `manual` / `faq` / `passage` | `false`，`reason=not_a_file` | 不属于本契约 |
| 删除类事件 | `false`，`reason=deleted` | 不要再 download |
| `kb.deleted` | **没有** `data.download` | 按库 id 清空 |

---

## 8. 可靠性

| 问题 | 做法 |
|------|------|
| 丢失 | 先写 outbox。只有 outbox INSERT 失败（库挂）才丢，打指标 `webhook_outbox_insert_failed`。 |
| 同时 POST 上限（每空间 ≤20） | 返回 `ErrWebhookInFlightBusy`。busy **禁止** `SkipRetry`（会归档，而 outbox 已是 `processed`）。Webhook Server 的 `IsFailure` **只对 busy 为 false**（不耗 MaxRetry，延迟 3s）。**禁止**写成恒 false，否则 5xx 永远到不了 MaxRetry。`delivery_id` 入队时生成，重试不变。 |
| 部分入队 / 双投 | `asynq.TaskID("wh:"+event_id+":"+endpoint_id)`，`ErrTaskIDConflict` 视为已入队。投递表 `UNIQUE(event_id, endpoint_id)`。认领成功但 Enqueue 失败：两行都保持 `pending`，sweep 只重试 Enqueue。全部端点认领并入队后才把 outbox 标 `processed`。当时 **0 个** enabled 端点 → 直接 `processed`（后加的端点不补历史）。 |
| Lite（无 Redis） | 门控降级为每次 `ListEnabledByTenant`；仍只对有订阅的事件写 outbox。`RegisterSyncHandlers` 注册 `webhook:deliver`；同进程 POST；进程内 ticker 扫表。不声称跨进程至少一次。 |

---

## 9. 表

三张表：PostgreSQL versioned migration（合入时用当时空号）+ SQLite 镜像（同样用当时空号）。用 `COMMENT ON COLUMN`，不要 MySQL 列内 `COMMENT`。

| 表 | 职责 | 生命周期 |
|----|------|----------|
| `tenant_webhook_outbox` | 领域事件（至少一次的真源） | `processed` 后约保留 7 天 |
| `tenant_webhook_endpoints` | URL、加密密钥、订阅 type | 软删；每空间最多 5 条有效行 |
| `tenant_webhook_deliveries` | 末次出站 POST，供排障 | 每端点最近 50 条；**不存 JSON body** |

应用层约束：

- 生产 URL 必须 `https`；本地允许 loopback `http`；写入和每次 POST 前 `ValidateURLForSSRF`。
- 密钥至少 16 字符；AES-GCM 落库；API 只回 `has_secret`。
- deliveries 不对 endpoints 建外键（软删后仍要能查历史）。

---

## 10. 管理 API 与设置页

路径前缀 `/api/v1/tenants/:id/event/...`，挂 `PathTenantMatch`。JWT：Owner+。API Key：`manage_tenant_settings`。

| 方法 | 路径 |
|------|------|
| GET | `/event/webhooks` |
| POST | `/event/webhooks` |
| PATCH | `/event/webhooks/:hook_id` |
| DELETE | `/event/webhooks/:hook_id` |
| POST | `/event/webhooks/:hook_id/test` |
| GET | `/event/webhooks/:hook_id/deliveries` |
| GET | `/event/types` |

配置入口：**设置 → 空间 → 事件回调**（仅 Owner）。不要放进「发布集成」Tab。折叠区：验签代码 + 各事件 JSON 示例（中性文件名，不用业务语料）。

CRUD 写审计：`webhook.endpoint_created|updated|deleted`。

---

## 11. Asynq

新增 `WorkerPoolWebhook` / `QueueWebhook="webhook"` / `SharedWeight=0`，独立 Server，mux 注册方式对齐 Wiki（`RunAsynqServer` 注册全部 type；本 Server **只拉** webhook 队列）。不要并进 `DefaultUpstreamWorkerConcurrency`。不要另建「只含 webhook handler」的 mux（会漏 dead-letter / langfuse 中间件）。

Webhook Server **必须**自带 `IsFailure` / `RetryDelayFunc`。配置项：`asynq.webhook_concurrency` / `WEKNORA_ASYNQ_WEBHOOK_CONCURRENCY`。sweep / prune 走 `QueueWebhook`，不要挂 `QueueMaintenance`。

若上游已经加了更多池，本功能只**追加** webhook 池，不要写死「一共 N 个池」。

---

## 12. 安全

1. SSRF 客户端，重定向再校验。
2. 生产 HTTPS；仅 loopback 允许 http；内网 URL 默认关闭。
3. HMAC 签 `timestamp + "." + raw_body`；密钥创建后不回显。
4. 端点 CRUD 为 Owner+ 且 `PathTenantMatch`。
5. 载荷无文件正文、无空间 API Key。文件走 5 分钟票 + 换票。
6. in-flight 满了延迟再投、不丢。`kb.deleted` 不扇出文档事件。
7. 票绑定 knowledge id + 归属空间 + purpose。日志脱敏。票面路由**不**把 JWT 旁路挂到 `/knowledge/:id/download`。

---

## 13. 本 PR 不做

后续能力（不是本次）：

- 组织共享扇出与 `kb.share_*`
- `knowledge.updated` / 取消解析 / `kb.updated` / 角色变更事件
- 变更游标 / exactly-once / 同一 `knowledge_id` 有序投递
- 双密钥轮换、投递失败红点
- 与 Embed 聊天 Webhook 或 IM 入站 webhook 合并
- 在 webhook 里放文件正文、检索结果、JWT 或 `X-API-Key`
- 改 `GET /knowledge/:id/download`

另外也不做：包装整个 `KnowledgeRepository`、Gin 拦截发事件、并入 RAG EventBus、把端点存在 tenant JSONB、`events=[]` 表示订阅全部。

全量底账用现有列表 API（`GET /knowledge-bases`、`GET /knowledge-bases/:id/knowledge`），不要拿 Webhook 当全量同步。

---

## 14. 测试（必须保持绿）

- HMAC 签 `timestamp+"."+body`；Embed 仍只签 body
- 空 `events` → 400；未订阅 type 不入队
- `kb.deleted` 产生 0 条 `knowledge.deleted`
- `FinalizeSubtask` 只发一次 `parse_completed`；已 completed 的 Update 不重发
- 票过期 / 错 id / 错 purpose / 无票 → 401；renew POST 在 noAuth；现网下载无票仍要 Contributor
- `WorkerPoolWebhook` 在 `validPools`；`TypeWebhookDeliver` 在 `queueDefinitions`
- in-flight busy 返回 `ErrWebhookInFlightBusy` 且不 `SkipRetry`；`IsFailure` **仅** busy 为 false
- TaskID 冲突 + `UNIQUE(event_id, endpoint_id)` 不双投
- 同库一次删 101 条 → 2 条 `batch_deleted`，id 并集 101，`truncated=false`
- `EnsureOwner` / 临时库 / FAQ 容器壳不发

手工：未配置端点时上传无出站；发送测试；上传 PDF + 票下载 + 换票；`parse_completed`；单删与批量删；删库；成员加减；错密钥 401 且知识状态不变。

---

## 15. 模块

```text
internal/workspaceevent/                 # 合法 type；订阅门控 + Emit 写 outbox
internal/types/workspace_webhook.go
internal/utils/webhook_hmac.go           # SignWebhookTimestampBody
internal/utils/download_ticket.go
internal/application/repository/webhook_*.go
internal/application/service/webhook_{emit,dispatch,deliver,endpoint}.go
internal/handler/webhook_endpoint.go
internal/handler/knowledge_download_ticket.go
internal/router/routes_tenant_webhook.go
internal/container/webhook_housekeeping.go
frontend/src/views/settings/EventWebhookSettings.vue
website-docs/03-features/22-workspace-webhooks.md
```

`Emit` 注入到**已经会写库**的 service。不要从 Gin 中间件或 GORM 钩子发事件。
