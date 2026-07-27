# API 总览

本文档基于源码 `internal/router/router.go`（路由唯一事实来源）、`internal/handler/`、`internal/middleware/` 与 `internal/errors/` 整理，描述 WeKnora HTTP API 的通用约定。

## Base URL 与版本前缀

- 所有业务 API 挂载在 `/api/v1` 前缀下（`router.go` 中 `r.Group("/api/v1")`）。
- 健康检查：`GET /health`（无需认证），返回 `{"status":"ok"}`。
- Swagger UI：`GET /swagger/*any`，仅在非 `release` 模式（`GIN_MODE != release`）下注册。
- 认证之外的特殊路径：`GET|HEAD /r/:token`（短时效资源授权 URL）、`GET /files`（认证后文件代理）、`GET|HEAD /api/v1/files/presigned`（HMAC 签名 URL，无需认证）、`GET /api/v1/files/presigned-preview`（Admin 诊断）。

```
BASE=http://localhost:8080
```

## 认证方式

认证由 `internal/middleware/auth.go` 的 `Auth` 中间件统一处理，按以下顺序尝试：

### 1. JWT Bearer（Web 用户）

```
Authorization: Bearer <access_token>
```

- 通过 `POST /api/v1/auth/login`（或 register / auto-setup / OIDC）获得 `token` 与 `refresh_token`；`POST /api/v1/auth/refresh` 换发新 token。
- 可选请求头 `X-Tenant-ID: <tenant_id>`：在 JWT 指向的空间之外切换目标空间（须为该空间活跃成员，或具备 `CanAccessAllTenants` 跨空间超管属性）。畸形或 `0` 值直接返回 400。
- 若 JWT 未解析出任何空间且接口非“无空间可用”白名单（如 `/auth/me`、`/me/invitations` 等），返回 409 `{"code":"TENANT_REQUIRED"}`。

### 2. API Key（机器主体）

```
X-API-Key: <api_key>
```

- 空间级（workspace）key：在 `POST /api/v1/tenants/:id/api-keys` 创建，绑定到单一空间；携带 `X-Tenant-ID` 指向其它空间会得到 403。
- 平台级（platform）key：在 `POST /api/v1/system/admin/api-keys` 创建，必须携带 `X-Tenant-ID` 选择目标空间（`/system/admin/*`、`/tenants/all|search`、`POST /tenants` 除外），否则返回 409 `TENANT_REQUIRED`。
- 授权模型（`internal/middleware/api_key_gate.go`，默认拒绝）：每个 `/api/v1` 路由必须显式声明 API key 策略，未声明的路由对任何 key 一律 403。
  - `full_access` key：空间内全权（等效 Owner 的机器形态）。
  - 受限（scoped）key：按 capability 放行，并受 `knowledge_base_ids` 白名单约束。Capability 常量见 `internal/types/tenant_api_key.go`：`retrieve`、`ingest`、`chat`、`read_agents`、`manage_kbs`、`manage_agents`、`message_history`、`manage_models`、`manage_mcp_services`、`manage_datasources`、`manage_channels`、`manage_vector_stores`、`manage_storage_backends`、`manage_web_search`、`run_evaluations`、`manage_members`、`manage_spaces`、`manage_tenant_settings`；平台能力：`system_tenants_read/manage`、`system_settings_read/manage`、`system_runtime_read/manage`、`system_audit_read`。
- 外部用户主体（可选，按空间 `api-principal-config` 配置）：
  - `direct` 模式：`X-External-User-ID: <外部用户ID>`（≤128 字符）。
  - `signed_token` 模式：`X-External-User-Token: <HS256 JWT>`，要求 `aud=weknora`、`exp`（生存期 ≤24h）、`tenant_id` claim 与目标空间一致、`sub` 为外部用户 ID。

### 3. Embed publish token（匿名嵌入端）

`/api/v1/embed/:channel_id/*` 公开路由使用独立的 `EmbedAuth` 中间件（`internal/middleware/embed_auth.go`）：

```
Authorization: Embed <publish_token 或 session_token>
```

- `POST /embed/:channel_id/exchange` 用 publish token 换取短时效 session token；会话级操作还需 `X-Embed-Session: <sig>`（创建会话时返回的签名句柄）。
- IM 回调路由（`/api/v1/im/callback/:channel_id`）注册在全局认证中间件之前，使用各 IM 平台自身的签名验证。

### 认证流程图

```mermaid
flowchart TD
    A["客户端请求"] --> B{"路径在免认证白名单?<br/>(login/register/oidc/presigned...)"}
    B -- "是" --> H["直接进入 Handler"]
    B -- "否" --> C{"Authorization: Bearer <JWT>?"}
    C -- "有效" --> D{"X-Tenant-ID 请求头?"}
    D -- "无" --> E["使用 JWT 内 tenant_id"]
    D -- "有" --> F{"IsTenantAccessible?<br/>(成员/跨空间超管)"}
    F -- "否" --> G["403 Forbidden"]
    F -- "是" --> E
    E --> R{"resolveTenantRole<br/>(成员表 → 超管 → 孤儿空间自愈 → EnableRBAC 兜底)"}
    R -- "无角色且 RBAC 强制" --> G
    R -- "得到角色" --> P["注入 tenant/user/role 上下文"]
    C -- "无/无效" --> K{"X-API-Key?"}
    K -- "无" --> U["401 Unauthorized"]
    K -- "有" --> L{"key 类型"}
    L -- "platform key" --> M{"X-Tenant-ID?"}
    M -- "缺失且非平台白名单路由" --> V["409 TENANT_REQUIRED"]
    M -- "有" --> P2["注入平台机器主体 + 目标空间"]
    L -- "workspace key" --> N{"X-Tenant-ID 与 key 空间一致?"}
    N -- "不一致" --> G
    N -- "一致/未携带" --> P3["注入空间机器主体<br/>(可选外部用户主体 Header)"]
    P --> Q["RBAC 角色守卫 (rbac.go)"]
    P2 --> S["APIKeyGate: 路由策略<br/>(full_access / capability / KB 白名单, 默认拒绝)"]
    P3 --> S
    Q --> H
    S --> H
```

## 角色与权限模型（RBAC）

`internal/middleware/rbac.go` + `internal/middleware/access.go`：

| 角色 | 说明 |
| --- | --- |
| `owner` | 空间所有者：空间生命周期、API key、成员管理 |
| `admin` | 空间管理员：模型/基础设施/渠道等空间级配置 |
| `contributor` | 贡献者：可创建 KB/Agent，可修改**自己创建**的资源 |
| `viewer` | 只读成员：读取与会话使用 |
| SystemAdmin | 平台级管理员（`User.IsSystemAdmin`），独立于空间角色，守卫 `/system/admin/*`，始终强制 |

- 文档中“Viewer+ / Contributor+ / Admin+ / Owner”表示最低角色要求；“创建者 OR Admin+”对应 `RequireOwnershipOrRole`（Contributor 只能改自己创建的 KB/Agent/内容）。
- `cfg.Tenant.EnableRBAC=false` 时角色守卫只记录日志不拦截（rollout fail-open）；SystemAdmin 守卫不受此开关影响。
- KB 级访问守卫 `KBAccessRead/Write`（`internal/middleware/kb_access.go`）：解析“自有 / 组织共享 / 经共享 Agent 可见”三类访问，并把请求上下文的 tenant 重写为 KB 属主空间。
- API key 主体会短路 JWT 角色守卫，其真实权限完全由 APIKeyGate（capability + KB 白名单）决定。
- 被拒绝的请求会写入审计日志（`middleware.AuditServiceProvider`，1 分钟滑动窗口去重）。

## 通用响应格式与错误码

多数 handler 返回：

```json
{ "success": true, "data": { ... } }
```

列表类接口常见附加字段：`total`、`page`、`page_size`。少数例外：`/system/admin/*` 的部分读取接口直接返回原始行/数组（不含包装），`/system/info` 等使用 `{"code":0,"msg":"success","data":...}`。

错误统一由 `internal/middleware/error_handler.go` 输出（`internal/errors/errors.go` 的 `AppError`）：

```json
{ "success": false, "error": { "code": 1003, "message": "...", "details": null } }
```

中间件层（认证/RBAC）直接返回 `{"error": "..."}`（部分带 `"code"` 字符串，如 `TENANT_REQUIRED`）。

| 错误码 | 含义 | HTTP |
| --- | --- | --- |
| 1000 | ErrBadRequest 请求错误 | 400 |
| 1001 | ErrUnauthorized 未认证 | 401 |
| 1002 | ErrForbidden 无权限 | 403 |
| 1003 | ErrNotFound 资源不存在 | 404 |
| 1004 | ErrMethodNotAllowed | 405 |
| 1005 | ErrConflict 冲突 | 409 |
| 1006 | ErrTooManyRequests 限流/配额 | 429 |
| 1007 | ErrInternalServer 内部错误 | 500 |
| 1008 | ErrServiceUnavailable 暂不可用 | 503 |
| 1009 | ErrTimeout 超时 | — |
| 1010 | ErrValidation 参数校验失败 | 400 |
| 2000-2005 | 空间类：不存在/已存在/停用/名称必填/状态非法/自助创建被禁用 | 404/409/403/… |
| 2100-2103 | Agent 类：缺思考模型/缺允许工具/迭代次数非法(1-20)/温度非法(0-2) | 400 |
| 2200-2201 | VectorStore 绑定非法 / 当前不可用 | 400 |

另有非编码错误：`types.StorageQuotaExceededError`（存储配额超限）、`types.DuplicateKnowledgeError`（重复文件/URL，上传接口返回 409 且 `data` 携带已存在的 Knowledge）。

## 分页规范

`internal/handler/list_pagination.go`：

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `page` | int | 否 | 页码，默认 1，必须 ≥1 |
| `page_size` | int | 否 | 每页条数，默认 20，范围 1-100 |

超范围或非法值返回校验错误（code 1010）。列表响应携带 `total/page/page_size`。部分接口使用游标分页：审计日志（`after_id`+`limit`，响应带 `next_cursor`）、系统运行时任务（`cursor`+`page_size`，响应带 `next_cursor/has_more`）、Wiki index/log（`cursor`+`limit`）。

## 流式接口协议（SSE）

聊天类接口（`POST /api/v1/knowledge-chat/:session_id`、`POST /api/v1/agent-chat/:session_id`、`GET /api/v1/sessions/continue-stream/:session_id`，以及 embed 端对应路由）返回 Server-Sent Events：

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

每个事件为 `event: message`，`data:` 为 `types.StreamResponse` JSON：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 请求 ID |
| `response_type` | string | `answer` / `references` / `thinking` / `tool_call` / `tool_result` / `reflection` / `session_title` / `agent_query` / `tool_approval_required` / `tool_approval_resolved` / `mcp_oauth_required` / `mcp_oauth_resolved` / `error` / `complete` |
| `content` | string | 增量文本 |
| `done` | bool | 该类型事件是否结束 |
| `knowledge_references` | []SearchResult | `references` 事件携带的引用 |
| `tool_calls` | []LLMToolCall | 工具调用事件 |
| `session_id` / `assistant_message_id` | string | `agent_query` 事件携带 |
| `usage` | TokenUsage | `prompt_tokens/completion_tokens/total_tokens/cache_*` |
| `finish_reason` | string | 结束原因 |

流以 `response_type:"complete"`（`done:true`）终止；出错时以 `response_type:"error"`（`done:true`）终止。`continue-stream` 采用重放 + 100ms 轮询追增量的续传语义（`?message_id=` 必填）。

## 限流说明

| 面 | 限制 | 来源 |
| --- | --- | --- |
| 公开分享链接接口（`/auth/invitations/lookup`、`/auth/register-by-invite`） | 每 IP 30 次/分钟（两个端点共享额度），超限 429（code 1006） | `internal/middleware/auth_public_ratelimit.go` |
| Embed 公开路由 | 每 (channel, IP) `rate_limit_per_minute`（默认 30）/分钟；channel 级 `rate_limit_per_minute*20`（下限 120）/分钟；channel 级 `rate_limit_per_day`（默认 10000）/天；超限 429 | `internal/middleware/embed_auth.go` |
| 反代信任 | 仅信任 `WEKNORA_TRUSTED_PROXIES`（默认回环+内网段）的 `X-Forwarded-For`，防止伪造 IP 绕过限流 | `router.go` `trustedProxies()` |

其余业务接口无全局限流；自助创建空间等配额类拒绝同样使用 429（code 1006）。

## API 分组导航

| 分组 | 文档 | 主要前缀 |
| --- | --- | --- |
| 认证与用户 | [02-api-auth.md](./02-api-auth.md) | `/auth`、`/me/invitations` |
| 租户（空间）与成员 | [02-api-tenant.md](./02-api-tenant.md) | `/tenants` |
| 组织与共享 | [02-api-org.md](./02-api-org.md) | `/organizations`、`/shared-*`、`/knowledge-bases/:id/shares`、`/agents/:id/shares` |
| 知识库与知识 | [02-api-knowledge.md](./02-api-knowledge.md) | `/knowledge-bases`、`/knowledge`、`/chunks`、`/knowledge-bases/:id/tags`、`/chunker/preview` |
| FAQ 与 Wiki | [02-api-faq-wiki.md](./02-api-faq-wiki.md) | `/knowledge-bases/:id/faq`、`/faq`、`/knowledgebase/:kb_id/wiki` |
| 会话、消息与聊天 | [02-api-chat.md](./02-api-chat.md) | `/sessions`、`/messages`、`/knowledge-chat`、`/agent-chat`、`/knowledge-search` |
| 模型、初始化与系统 | [02-api-model-system.md](./02-api-model-system.md) | `/models`、`/initialization`、`/system`、`/system/admin`、`/evaluation`、`/weknoracloud` |
| 基础设施与数据源 | [02-api-infra.md](./02-api-infra.md) | `/vector-stores`、`/storage-backends`、`/web-search-providers`、`/datasource` |
| Agent、MCP 与技能 | [02-api-agent-mcp.md](./02-api-agent-mcp.md) | `/agents`、`/mcp-services`、`/agent`、`/skills`、`/user/favorites` |
| IM、Embed 与文件服务 | [02-api-channels.md](./02-api-channels.md) | `/im`、`/im-channels`、`/wechat`、`/embed-channels`、`/embed`、`/files`、`/r/:token` |
