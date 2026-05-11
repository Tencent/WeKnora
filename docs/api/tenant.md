# 租户管理 API

[返回目录](./README.md)

包含两组接口：
- 租户 CRUD（`/tenants`、`/tenants/:id`、`/tenants/:id/api-key`）：当前认证用户对自己所属租户进行管理；跨租户访问需要管理员权限。
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
| POST   | `/tenants/:id/api-key`     | 重置租户 API Key                                  |
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

创建一个新的租户，服务端会自动生成租户 ID 与 API Key。

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

## POST `/tenants/:id/api-key` - 重置租户 API Key

为指定租户生成新的 API Key，旧 Key 立即失效。访问规则同 `GET /tenants/:id`。

**路径参数**:

| 字段 | 类型 | 说明    |
| ---- | ---- | ------- |
| id   | int  | 租户 ID |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/tenants/10000/api-key' \
--header 'X-API-Key: sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "api_key": "sk-IKtd9JGV4-aPGQ6RiL8YJu9Vzb3-ae4lgFkjFJZmhvUn2mLu"
    },
    "success": true
}
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
