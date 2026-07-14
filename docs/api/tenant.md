# 租户管理 API

[返回目录](./README.md)

包含两组接口：
- 租户 CRUD（`/tenants`、`/tenants/:id`）：当前认证用户对自己所属租户进行管理；跨租户访问需要管理员权限。
- 跨租户接口（`/tenants/all`、`/tenants/search`）：**需要服务端启用 `EnableCrossTenantAccess` 且当前用户具备 `CanAccessAllTenants` 权限**，否则返回 403。
- 租户 KV 配置（`/tenants/kv/:key`）：当前租户级别的通用配置项，**`tenant_id` 从认证上下文中获取，不在 URL 中传入**。

| 方法   | 路径                       | 描述                                              |
| ------ | -------------------------- | ------------------------------------------------- |
| GET    | `/tenants/all`             | 获取所有租户列表（需跨租户权限）                  |
| GET    | `/tenants/search`          | 分页搜索租户（需跨租户权限）                      |
| POST   | `/tenants`                 | 创建新租户                                        |
| GET    | `/tenants/:id`             | 获取指定租户信息                                  |
| PUT    | `/tenants/:id`             | 更新租户信息                                      |
| DELETE | `/tenants/:id`             | 删除租户                                          |
| GET    | `/tenants/:id/api-keys`    | 列出租户 API Key（Owner）                         |
| POST   | `/tenants/:id/api-keys`    | 创建带角色的 API Key（Owner）                  |
| DELETE | `/tenants/:id/api-keys/:key_id` | 吊销指定 API Key（Owner）                   |
| GET    | `/tenants/:id/api-principal-config` | 获取 API Key 用户身份配置（Owner）          |
| PUT    | `/tenants/:id/api-principal-config` | 更新 API Key 用户身份配置（Owner）          |
| GET    | `/tenants`                 | 获取当前用户可见的租户列表                        |
| GET    | `/tenants/kv/:key`         | 获取当前租户的 KV 配置（tenant 由认证上下文确定） |
| PUT    | `/tenants/kv/:key`         | 更新当前租户的 KV 配置（tenant 由认证上下文确定） |

## GET `/tenants/all` - 获取所有租户列表

获取系统中所有租户列表，需要跨租户权限。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants/all' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-An7_t_izCKFIJ4iht9Xjcjnj_MC48ILvwezEDki9ScfIa7KA'
```

**响应**:

```json
{
    "data": {
        "items": [
            {
                "id": 10001,
                "name": "weknora-1",
                "description": "weknora tenants 1",
                "status": "active",
                "business": "wechat",
                "created_at": "2025-08-11T20:37:28.39698+08:00",
                "updated_at": "2025-08-11T20:37:28.405693+08:00"
            },
            {
                "id": 10002,
                "name": "weknora-2",
                "description": "weknora tenants 2",
                "status": "active",
                "business": "wechat",
                "created_at": "2025-08-11T20:52:58.05679+08:00",
                "updated_at": "2025-08-11T20:52:58.060495+08:00"
            }
        ]
    },
    "success": true
}
```

## GET `/tenants/search` - 搜索租户

按关键词搜索租户，需要跨租户权限。

**查询参数**:
- `keyword`: 搜索关键词（可选）
- `tenant_id`: 按租户ID筛选（可选）
- `page`: 页码（默认 1）
- `page_size`: 每页条数（默认 20）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants/search?keyword=weknora&page=1&page_size=10' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-An7_t_izCKFIJ4iht9Xjcjnj_MC48ILvwezEDki9ScfIa7KA'
```

**响应**:

```json
{
    "data": {
        "items": [
            {
                "id": 10002,
                "name": "weknora",
                "description": "weknora tenants",
                "status": "active",
                "business": "wechat",
                "created_at": "2025-08-11T20:52:58.05679+08:00",
                "updated_at": "2025-08-11T20:52:58.060495+08:00"
            }
        ],
        "total": 1,
        "page": 1,
        "page_size": 10
    },
    "success": true
}
```

## POST `/tenants` - 创建新租户

创建一个新的租户。**默认不会**自动发放 API Key；请在创建后通过 `POST /tenants/:id/api-keys` 创建密钥。从旧版本升级时，原有 `tenants.api_key` 会迁移到 `tenant_api_keys` 表并继续可用，直至被吊销。

> **兼容旧行为（可选）**：如需恢复旧版「创建租户即下发默认 API Key」的行为，可将系统设置 `tenant.auto_create_api_key` 置为 `true`（或设置环境变量 `WEKNORA_TENANT_AUTO_CREATE_API_KEY=true`）。开启后，创建租户会自动生成一个 `full_access` 权限的 API Key，并在响应体 `data.api_key` 中返回其明文 token（仅本次创建响应返回，请妥善保存）。默认 `false`。

**参数说明（请求体）**:

| 字段              | 类型   | 必填 | 说明                                                   |
| ----------------- | ------ | ---- | ------------------------------------------------------ |
| name              | string | 是   | 租户名称                                               |
| description       | string | 否   | 租户描述                                               |
| business          | string | 否   | 业务标识（如 `wechat`）                                |
| retriever_engines | object | 否   | 检索引擎组合配置（`engines` 数组：每项含 `retriever_type` 与 `retriever_engine_type`） |
| storage_quota     | int    | 否   | 存储配额（字节）                                       |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants' \
--header 'Content-Type: application/json' \
--data '{
    "name": "weknora",
    "description": "weknora tenants",
    "business": "wechat",
    "retriever_engines": {
        "engines": [
            {
                "retriever_type": "keywords",
                "retriever_engine_type": "postgres"
            },
            {
                "retriever_type": "vector",
                "retriever_engine_type": "postgres"
            }
        ]
    }
}'
```

**响应**（默认，不含 API Key）:

```json
{
    "data": {
        "id": 10000,
        "name": "weknora",
        "description": "weknora tenants",
        "status": "active",
        "retriever_engines": {
            "engines": [
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "keywords"
                },
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "vector"
                }
            ]
        },
        "business": "wechat",
        "storage_quota": 10737418240,
        "storage_used": 0,
        "created_at": "2025-08-11T20:37:28.396980093+08:00",
        "updated_at": "2025-08-11T20:37:28.396980301+08:00",
        "deleted_at": null
    },
    "success": true
}
```

当开启 `tenant.auto_create_api_key`（或 `WEKNORA_TENANT_AUTO_CREATE_API_KEY=true`）时，响应的 `data` 中会额外包含 `api_key` 字段（`full_access` 密钥的明文 token）：

```json
{
    "data": {
        "id": 10000,
        "name": "weknora",
        "description": "weknora tenants",
        "api_key": "sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG",
        "status": "active",
        "business": "wechat",
        "storage_quota": 10737418240,
        "storage_used": 0,
        "created_at": "2025-08-11T20:37:28.396980093+08:00",
        "updated_at": "2025-08-11T20:37:28.396980301+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## GET `/tenants/:id` - 获取指定租户信息

获取指定 ID 的租户详情。只能访问自己所属租户；访问其他租户需要跨租户权限，否则返回 403。

**路径参数**:

| 字段 | 类型 | 说明    |
| ---- | ---- | ------- |
| id   | int  | 租户 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants/10000' \
--header 'X-API-Key: sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": 10000,
        "name": "weknora",
        "description": "weknora tenants",
        "api_key": "sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG",
        "status": "active",
        "retriever_engines": {
            "engines": [
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "keywords"
                },
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "vector"
                }
            ]
        },
        "business": "wechat",
        "storage_quota": 10737418240,
        "storage_used": 0,
        "created_at": "2025-08-11T20:37:28.39698+08:00",
        "updated_at": "2025-08-11T20:37:28.405693+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## PUT `/tenants/:id` - 更新租户信息

更新指定租户的基础信息。访问规则同 `GET /tenants/:id`。

**路径参数**:

| 字段 | 类型 | 说明    |
| ---- | ---- | ------- |
| id   | int  | 租户 ID |

**参数说明（请求体）**: 与 `POST /tenants` 相同字段；未传字段保持原值。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/tenants/10000' \
--header 'X-API-Key: sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG' \
--header 'Content-Type: application/json' \
--data '{
    "name": "weknora new",
    "description": "weknora tenants new",
    "status": "active",
    "retriever_engines": {
        "engines": [
            {
                "retriever_engine_type": "postgres",
                "retriever_type": "keywords"
            },
            {
                "retriever_engine_type": "postgres",
                "retriever_type": "vector"
            }
        ]
    },
    "business": "wechat",
    "storage_quota": 10737418240
}'
```

**响应**:

```json
{
    "data": {
        "id": 10000,
        "name": "weknora new",
        "description": "weknora tenants new",
        "api_key": "sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG",
        "status": "active",
        "retriever_engines": {
            "engines": [
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "keywords"
                },
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "vector"
                }
            ]
        },
        "business": "wechat",
        "storage_quota": 10737418240,
        "storage_used": 0,
        "created_at": "2025-08-11T20:37:28.39698+08:00",
        "updated_at": "2025-08-11T20:49:02.13421034+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## DELETE `/tenants/:id` - 删除租户

删除指定租户。访问规则同 `GET /tenants/:id`。

**路径参数**:

| 字段 | 类型 | 说明    |
| ---- | ---- | ------- |
| id   | int  | 租户 ID |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/tenants/10000' \
--header 'X-API-Key: sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "Tenant deleted successfully",
    "success": true
}
```

## 租户 API Key 管理（`tenant_api_keys`）

自 scoped API Key 改造后，密钥以独立记录存储，支持：

- **role**：`viewer`（只读 + 语义检索 POST）、`contributor`（知识库写入）、`admin`（租户级管理，不含 `/api-keys` 管理面）
- **knowledge_base_ids**：可选，将 Key 限制在指定知识库
- **吊销**：`DELETE /tenants/:id/api-keys/:key_id`
- **过期**：创建时可选 `expires_at_unix`

认证上下文中的租户角色与 Key 的 `role` 一致（`viewer` / `contributor` / `admin`）。路由级 API Key 鉴权与 KB 访问守卫在 `X-API-Key` 认证后强制执行。

## API Key Principal：隔离边界与安全说明

`api-principal-config` 控制 `X-API-Key` 请求如何映射为终端 **Principal**。请先理解以下边界，再选择模式。

### Principal 隔离范围（当前实现）

Principal **仅**用于按终端用户隔离以下能力：

- **对话 Session**（创建、列表、读取按外部用户分开；`仅租户` 模式仍共用租户级 Session）
- **MCP OAuth** 访问令牌（同一租户下不同外部用户各自授权，token 互不共用）
- 对话内 MCP OAuth 提示、MCP 工具审批等与终端用户绑定的流程

Principal **不会**缩小 API Key 的 HTTP 路由权限：路由访问由 Key 的 `role` 控制；租户内 RBAC 角色与 `role` 一致。知识库、Agent 等资源的细粒度访问另受 KB 守卫约束。

### 模式与安全假设

| mode | 适用场景 | 安全假设 |
| ---- | -------- | -------- |
| `tenant` | 无 per-user MCP 需求 | 全租户共用一个 MCP OAuth 身份 |
| `direct_header` | 仅可信服务端到服务端 | 用户 ID 来自调用方请求头，**可被持有 API Key 的任意调用方伪造**（冒充其他外部用户并共用/劫持其 MCP OAuth 授权）。面向终端用户或不可信客户端时**禁止**使用；若必须使用，请开启 `require_direct_header` 并确保 API Key 仅保存在可信后端 |
| `signed_token` | 面向终端用户的集成（**推荐**） | 由业务后端使用 `hmac_secret` 为外部用户签发短期 HS256 JWT；无效或缺失 token 返回 401，**不回退**为租户级 Principal |

`direct_header` 模式下，若未携带用户 ID 请求头：`require_direct_header=false` 时回退为租户级 Principal；`require_direct_header=true` 时返回 401。

## GET `/tenants/:id/api-principal-config` - 获取 API Key 用户身份配置

返回租户级 API Key 请求如何映射为终端 Principal 的配置。**需要 Owner 权限**。

**响应字段**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| mode | string | `tenant` / `direct_header` / `signed_token` |
| direct_header_name | string | 直接传用户 ID 时的请求头名，默认 `X-External-User-ID` |
| signed_token_header_name | string | 签名 token 模式请求头名，默认 `X-External-User-Token` |
| require_direct_header | bool | `direct_header` 模式下是否强制要求用户 ID 请求头 |
| has_hmac_secret | bool | 是否已配置 HMAC secret（不返回明文） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants/10000/api-principal-config' \
--header 'Authorization: Bearer <token>'
```

**响应**:

```json
{
  "success": true,
  "data": {
    "mode": "signed_token",
    "direct_header_name": "X-External-User-ID",
    "signed_token_header_name": "X-External-User-Token",
    "require_direct_header": false,
    "has_hmac_secret": true
  }
}
```

## PUT `/tenants/:id/api-principal-config` - 更新 API Key 用户身份配置

更新 API Key 请求的 Principal 映射方式。**需要 Owner 权限**。

**请求体**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| mode | string | 必填，`tenant` / `direct_header` / `signed_token` |
| direct_header_name | string | 可选 |
| signed_token_header_name | string | 可选 |
| require_direct_header | bool | 可选，`direct_header` 模式下缺 header 是否 401 |
| hmac_secret | string | 可选，`signed_token` 模式 HMAC 密钥；省略则保留现有值 |

`signed_token` 模式首次启用时必须提供 `hmac_secret`。

外部用户 JWT 要求：HS256 签名、`aud=weknora`、包含 `sub` 与 `tenant_id`、有效期不超过 24 小时。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/tenants/10000/api-principal-config' \
--header 'Authorization: Bearer <token>' \
--header 'Content-Type: application/json' \
--data '{
  "mode": "direct_header",
  "direct_header_name": "X-External-User-ID",
  "require_direct_header": true
}'
```

## GET `/tenants` - 获取租户列表

返回当前认证上下文对应的租户（普通用户为单条；管理员仍只返回自身租户）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "items": [
            {
                "id": 10002,
                "name": "weknora",
                "description": "weknora tenants",
                "api_key": "sk-An7_t_izCKFIJ4iht9Xjcjnj_MC48ILvwezEDki9ScfIa7KA",
                "status": "active",
                "retriever_engines": {
                    "engines": [
                        {
                            "retriever_engine_type": "postgres",
                            "retriever_type": "keywords"
                        },
                        {
                            "retriever_engine_type": "postgres",
                            "retriever_type": "vector"
                        }
                    ]
                },
                "business": "wechat",
                "storage_quota": 10737418240,
                "storage_used": 0,
                "created_at": "2025-08-11T20:52:58.05679+08:00",
                "updated_at": "2025-08-11T20:52:58.060495+08:00",
                "deleted_at": null
            }
        ]
    },
    "success": true
}
```

## GET `/tenants/kv/:key` - 获取租户 KV 配置

获取当前租户的 KV 配置项。**租户 ID 从认证上下文中获取**（即由 `X-API-Key` / Bearer Token 对应的租户决定），URL 中不需要也不接受 tenant_id。

**路径参数**:

| 字段 | 类型   | 说明                                           |
| ---- | ------ | ---------------------------------------------- |
| key  | string | 配置键名（见下方支持的 key 列表，不支持的键返回 400） |

**支持的 key 值**:

| key                    | 说明                          |
| ---------------------- | ----------------------------- |
| `agent-config`         | Agent 配置（最大迭代次数、温度、System Prompt、可用工具等） |
| `web-search-config`    | 网页搜索配置                 |
| `conversation-config`  | 普通模式会话/对话配置        |
| `prompt-templates`     | 系统提示词模板（只读，按用户语言本地化） |
| `parser-engine-config` | 解析引擎配置（如 MinerU）    |
| `storage-engine-config`| 存储引擎配置（Local/MinIO/COS） |
| `chat-history-config`  | 聊天历史索引配置             |
| `retrieval-config`     | 全局检索配置                 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants/kv/agent-config' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应（以 `agent-config` 为例）**:

```json
{
    "data": {
        "max_iterations": 10,
        "allowed_tools": ["knowledge_search", "web_search"],
        "temperature": 0.3,
        "system_prompt": "...",
        "use_custom_system_prompt": false,
        "available_tools": [
            { "name": "knowledge_search", "label": "知识库检索", "description": "..." }
        ],
        "available_placeholders": [
            { "name": "web_search_status", "label": "联网搜索状态", "description": "..." }
        ]
    },
    "success": true
}
```

失败时（不支持的键）：

```json
{ "success": false, "error": "unsupported key" }
```

## PUT `/tenants/kv/:key` - 更新租户 KV 配置

更新当前租户的 KV 配置项。**租户 ID 从认证上下文中获取**，请求体结构按 `key` 不同而异。`prompt-templates` 为只读，不支持 PUT。

**路径参数**:

| 字段 | 类型   | 说明                          |
| ---- | ------ | ----------------------------- |
| key  | string | 配置键名（见 GET 接口的支持列表，`prompt-templates` 除外） |

**请求（以 `agent-config` 为例）**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/tenants/kv/agent-config' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "max_iterations": 20,
    "temperature": 0.3,
    "system_prompt": ""
}'
```

**响应**:

```json
{
    "data": {
        "max_iterations": 20,
        "allowed_tools": ["knowledge_search", "web_search"],
        "temperature": 0.3,
        "system_prompt": "",
        "use_custom_system_prompt": false
    },
    "message": "Agent configuration updated successfully",
    "success": true
}
```

**约束**:

- `agent-config`: `max_iterations` 取值范围 `(0, 30]`；`temperature` 取值范围 `[0, 2]`。
- `web-search-config`: `max_results` 取值范围 `[1, 50]`。
- `conversation-config`: 包含多项阈值校验（如 `keyword_threshold` / `vector_threshold` ∈ `[0, 1]`，`rerank_threshold` ∈ `[-10, 10]`，`temperature` ∈ `[0, 2]`，`max_completion_tokens` ∈ `[1, 100000]` 等）。
- `retrieval-config`: `embedding_top_k` / `rerank_top_k` ∈ `[0, 200]`；阈值范围同上。
- `storage-engine-config`: `default_provider` 必须在 `STORAGE_ALLOW_LIST` 允许的列表内。
- `chat-history-config`: 启用且设置了 `embedding_model_id` 而尚未关联知识库时，会自动创建一个隐藏知识库并将其 ID 写入配置。
