# 会话管理 API

[返回目录](./README.md)

会话（Session）是纯粹的对话容器，仅存储基础信息（标题、描述、置顶状态等）。所有与知识库、模型、检索策略相关的配置均在查询时由 Custom Agent 提供，不再存储在会话中。

| 方法   | 路径                                       | 描述                          |
| ------ | ------------------------------------------ | ----------------------------- |
| POST   | `/sessions`                                | 创建会话                      |
| DELETE | `/sessions/batch`                          | 批量删除会话                  |
| GET    | `/sessions/:id`                            | 获取会话详情                  |
| GET    | `/sessions`                                | 获取当前租户的会话列表        |
| PUT    | `/sessions/:id`                            | 更新会话                      |
| DELETE | `/sessions/:id`                            | 删除会话                      |
| DELETE | `/sessions/:id/messages`                   | 清空会话消息                  |
| POST   | `/sessions/:session_id/generate_title`     | 生成会话标题                  |
| POST   | `/sessions/:session_id/stop`               | 停止生成                      |
| POST   | `/sessions/:session_id/pin`                | 置顶会话                      |
| DELETE | `/sessions/:id/pin`                        | 取消置顶会话                  |
| GET    | `/sessions/continue-stream/:session_id`    | 继续未完成的流式响应          |

> **路由命名说明**：置顶接口的 POST 与 DELETE 使用了不同的路径参数名（POST 用 `:session_id`，DELETE 用 `:id`）。这是由于 gin 路由器为每个 HTTP 方法维护独立的 radix tree，且既有树中的通配符命名不同，必须保留以避免注册时的 `wildcard conflicts` panic。两者语义上都指会话 ID。

## POST `/sessions` - 创建会话

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "title": "我的新对话",
    "description": "关于 AI 的讨论"
}'
```

**请求参数**:

| 字段          | 类型   | 必填 | 描述     |
| ------------- | ------ | ---- | -------- |
| `title`       | string | 否   | 会话标题 |
| `description` | string | 否   | 会话描述 |

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "411d6b70-9a85-4d03-bb74-aab0fd8bd12f",
        "title": "我的新对话",
        "description": "关于 AI 的讨论",
        "tenant_id": 1,
        "user_id": "u-001",
        "is_pinned": false,
        "created_at": "2026-03-27T12:26:19.611616+08:00",
        "updated_at": "2026-03-27T12:26:19.611616+08:00",
        "deleted_at": null
    }
}
```

> 通过 API-Key 调用时 `user_id` 可能为空，此时会话以租户级可见。

## DELETE `/sessions/batch` - 批量删除会话

支持两种模式：按 ID 列表批量删除，或删除当前租户的所有会话。

**请求 - 按 ID 列表删除**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/sessions/batch' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "ids": [
        "411d6b70-9a85-4d03-bb74-aab0fd8bd12f",
        "ceb9babb-1e30-41d7-817d-fd584954304b"
    ]
}'
```

**请求 - 删除所有会话**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/sessions/batch' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "delete_all": true
}'
```

**请求参数**:

| 字段         | 类型     | 必填 | 描述                                                       |
| ------------ | -------- | ---- | ---------------------------------------------------------- |
| `ids`        | string[] | 否   | 要删除的会话 ID 列表（`delete_all` 为 `false` 时必填）     |
| `delete_all` | bool     | 否   | 设为 `true` 时删除当前租户的所有会话，忽略 `ids` 字段      |

**响应**:

```json
{
    "success": true,
    "message": "Sessions deleted successfully"
}
```

`delete_all=true` 时 `message` 为 `"All sessions deleted successfully"`。

## GET `/sessions/:id` - 获取会话详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/ceb9babb-1e30-41d7-817d-fd584954304b' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**路径参数**:

| 字段 | 类型   | 必填 | 描述    |
| ---- | ------ | ---- | ------- |
| `id` | string | 是   | 会话 ID |

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "ceb9babb-1e30-41d7-817d-fd584954304b",
        "title": "模型优化策略",
        "description": "",
        "tenant_id": 1,
        "user_id": "u-001",
        "is_pinned": true,
        "pinned_at": "2026-04-01T09:12:33.123456+08:00",
        "created_at": "2026-03-27T10:24:38.308596+08:00",
        "updated_at": "2026-03-27T10:25:41.317761+08:00",
        "deleted_at": null
    }
}
```

会话不存在时返回 `404`。

## GET `/sessions` - 获取当前租户的会话列表

获取当前租户的会话列表，支持分页、关键字搜索、按来源 / Agent 过滤。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions?page=1&page_size=10&keyword=AI&source=web' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**查询参数**:

| 字段        | 类型   | 必填 | 描述                                                              |
| ----------- | ------ | ---- | ----------------------------------------------------------------- |
| `page`      | int    | 否   | 页码（默认 1）                                                    |
| `page_size` | int    | 否   | 每页数量（默认 10）                                               |
| `keyword`   | string | 否   | 按标题模糊匹配（ILIKE `%keyword%`）                               |
| `source`    | string | 否   | 来源过滤：`web`（无 IM 映射）或 IM 平台名，如 `feishu`、`wechat`、`slack` |
| `agent_id`  | string | 否   | 按 Agent 过滤（仅对 IM 会话生效）                                 |

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "id": "411d6b70-9a85-4d03-bb74-aab0fd8bd12f",
            "title": "我的新对话",
            "description": "",
            "tenant_id": 1,
            "user_id": "u-001",
            "is_pinned": true,
            "pinned_at": "2026-04-01T09:12:33.123456+08:00",
            "created_at": "2026-03-27T12:26:19.611616+08:00",
            "updated_at": "2026-03-27T12:26:19.611616+08:00",
            "deleted_at": null,
            "im_platform": "feishu",
            "im_chat_id": "oc_xxx",
            "im_agent_id": "agent-001"
        }
    ],
    "total": 1,
    "page": 1,
    "page_size": 10
}
```

> 列表项始终包含置顶状态字段，IM 来源相关字段（`im_platform`、`im_chat_id`、`im_thread_id`、`im_user_id`、`im_agent_id`、`im_channel_id`）仅对 IM 创建的会话填充，Web 会话省略。

## PUT `/sessions/:id` - 更新会话

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/sessions/411d6b70-9a85-4d03-bb74-aab0fd8bd12f' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "title": "WeKnora 技术讨论",
    "description": "关于 WeKnora 架构的讨论"
}'
```

**路径参数**:

| 字段 | 类型   | 必填 | 描述    |
| ---- | ------ | ---- | ------- |
| `id` | string | 是   | 会话 ID |

**请求参数**:

| 字段          | 类型   | 必填 | 描述     |
| ------------- | ------ | ---- | -------- |
| `title`       | string | 否   | 会话标题 |
| `description` | string | 否   | 会话描述 |

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "411d6b70-9a85-4d03-bb74-aab0fd8bd12f",
        "title": "WeKnora 技术讨论",
        "description": "关于 WeKnora 架构的讨论",
        "tenant_id": 1,
        "user_id": "u-001",
        "is_pinned": false,
        "created_at": "2026-03-27T12:26:19.611616+08:00",
        "updated_at": "2026-03-27T14:20:56.738424+08:00",
        "deleted_at": null
    }
}
```

会话不存在时返回 `404`。

## DELETE `/sessions/:id` - 删除会话

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/sessions/411d6b70-9a85-4d03-bb74-aab0fd8bd12f' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**路径参数**:

| 字段 | 类型   | 必填 | 描述    |
| ---- | ------ | ---- | ------- |
| `id` | string | 是   | 会话 ID |

**响应**:

```json
{
    "success": true,
    "message": "Session deleted successfully"
}
```

## DELETE `/sessions/:id/messages` - 清空会话消息

删除会话中的所有消息，同时清除 LLM 上下文和聊天历史知识库条目。会话本身保留。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/sessions/ceb9babb-1e30-41d7-817d-fd584954304b/messages' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**路径参数**:

| 字段 | 类型   | 必填 | 描述    |
| ---- | ------ | ---- | ------- |
| `id` | string | 是   | 会话 ID |

**响应**:

```json
{
    "success": true,
    "message": "Session messages cleared successfully"
}
```

## POST `/sessions/:session_id/generate_title` - 生成会话标题

根据消息内容自动生成会话标题。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/ceb9babb-1e30-41d7-817d-fd584954304b/generate_title' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
  "messages": [
    {
      "role": "user",
      "content": "你好，我想了解关于人工智能的知识"
    },
    {
      "role": "assistant",
      "content": "人工智能是计算机科学的一个分支..."
    }
  ]
}'
```

**路径参数**:

| 字段         | 类型   | 必填 | 描述    |
| ------------ | ------ | ---- | ------- |
| `session_id` | string | 是   | 会话 ID |

**请求参数**:

| 字段       | 类型      | 必填 | 描述                         |
| ---------- | --------- | ---- | ---------------------------- |
| `messages` | Message[] | 是   | 用作标题生成上下文的消息列表 |

**响应**:

```json
{
    "success": true,
    "data": "人工智能基础知识"
}
```

## POST `/sessions/:session_id/stop` - 停止生成

停止当前正在进行的助手回复生成任务。后端会向流中追加一条 `stop` 事件，由活跃的 SSE 处理协程感知并发起取消。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/7c966c74-610e-4516-8d5b-05e14b2e4ee0/stop' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "message_id": "ebbf7e53-dfe6-44d5-882f-36a4104910b5"
}'
```

**路径参数**:

| 字段         | 类型   | 必填 | 描述    |
| ------------ | ------ | ---- | ------- |
| `session_id` | string | 是   | 会话 ID |

**请求参数**:

| 字段         | 类型   | 必填 | 描述                    |
| ------------ | ------ | ---- | ----------------------- |
| `message_id` | string | 是   | 要停止生成的助手消息 ID |

**响应**:

```json
{
    "success": true,
    "message": "Generation stopped"
}
```

若消息已完成（无需停止）：

```json
{
    "success": true,
    "message": "Message already completed"
}
```

> 消息不属于当前会话返回 `403`；消息或会话不存在返回 `404`。

## POST `/sessions/:session_id/pin` - 置顶会话

将指定会话置顶（用户维度）。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/sessions/ceb9babb-1e30-41d7-817d-fd584954304b/pin' \
--header 'X-API-Key: sk-xxxxx'
```

**路径参数**:

| 字段         | 类型   | 必填 | 描述    |
| ------------ | ------ | ---- | ------- |
| `session_id` | string | 是   | 会话 ID |

**响应**:

```json
{
    "success": true,
    "is_pinned": true
}
```

会话不存在或对当前用户不可见时返回 `404`。

## DELETE `/sessions/:id/pin` - 取消置顶会话

取消指定会话的置顶。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/sessions/ceb9babb-1e30-41d7-817d-fd584954304b/pin' \
--header 'X-API-Key: sk-xxxxx'
```

**路径参数**:

| 字段 | 类型   | 必填 | 描述    |
| ---- | ------ | ---- | ------- |
| `id` | string | 是   | 会话 ID |

**响应**:

```json
{
    "success": true,
    "is_pinned": false
}
```

> 同上，`POST /pin` 与 `DELETE /pin` 路径参数名不同，但语义上都是会话 ID。

## GET `/sessions/continue-stream/:session_id` - 继续未完成的流式响应

用于在 SSE 连接断开后重新连接正在进行的流式响应：先回放该消息已产生的所有事件，再继续推送后续事件，直至 `complete`。

**路径参数**:

| 字段         | 类型   | 必填 | 描述    |
| ------------ | ------ | ---- | ------- |
| `session_id` | string | 是   | 会话 ID |

**查询参数**:

| 字段         | 类型   | 必填 | 描述                                                                            |
| ------------ | ------ | ---- | ------------------------------------------------------------------------------- |
| `message_id` | string | 是   | 从 `/messages/:session_id/load` 接口中获取的 `is_completed` 为 `false` 的消息 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/continue-stream/ceb9babb-1e30-41d7-817d-fd584954304b?message_id=b8b90eeb-7dd5-4cf9-81c6-5ebcbd759451' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应格式**:

服务器端事件流（Server-Sent Events），事件结构与 `/knowledge-chat/:session_id`、`/agent-chat/:session_id` 返回结果一致。若该消息当前在流中已无事件返回 `404 No stream events found`；若消息记录不存在返回 `404 Incomplete message not found`。
