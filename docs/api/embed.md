# Web Embed API

[返回目录](./README.md)

Web Embed 接口用于管理网页嵌入式聊天组件的渠道配置和公开访问端点。

## 说明

- **管理接口**（渠道 CRUD）需要认证（`X-API-Key` 或 `Authorization: Bearer`）。
- **公开接口**（`/embed/:channel_id/*`）使用 Publish Token 鉴权（`EmbedAuth` 中间件），不需要 WeKnora 用户认证。
- 公开接口的访问需通过渠道配置中的 `allowed_origins` 校验。

## 端点一览

### 渠道管理（需要认证）

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| POST | `/agents/:id/embed-channels` | 为智能体创建 Embed 渠道 |
| GET | `/agents/:id/embed-channels` | 获取智能体的 Embed 渠道列表 |
| GET | `/embed-channels` | 获取租户下所有 Embed 渠道 |
| GET | `/embed-channels/:channel_id` | 获取 Embed 渠道详情 |
| PUT | `/embed-channels/:channel_id` | 更新 Embed 渠道 |
| DELETE | `/embed-channels/:channel_id` | 删除 Embed 渠道 |
| POST | `/embed-channels/:channel_id/rotate-token` | 轮换 Publish Token |
| POST | `/embed-channels/:channel_id/preview-session` | 创建预览会话 |
| GET | `/embed-channels/:channel_id/stats` | 获取渠道统计 |

### 公开端点（Publish Token 鉴权）

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| POST | `/embed/:channel_id/exchange` | 交换 Embed Session Token |
| GET | `/embed/:channel_id/config` | 获取 Embed 组件配置 |
| GET | `/embed/:channel_id/suggested-questions` | 获取建议问题 |
| GET | `/embed/:channel_id/chunks/:chunk_id` | 获取分块内容 |
| POST | `/embed/:channel_id/sessions` | 创建匿名会话 |
| POST | `/embed/:channel_id/knowledge-chat/:session_id` | 基于知识库问答（SSE） |
| POST | `/embed/:channel_id/agent-chat/:session_id` | 基于 Agent 问答（SSE） |
| GET | `/embed/:channel_id/messages/:session_id/load` | 加载历史消息 |
| POST | `/embed/:channel_id/sessions/:session_id/stop` | 停止生成 |
| POST | `/embed/:channel_id/sessions/:session_id/events` | 转发 Webhook 事件 |
| POST | `/embed/:channel_id/sessions/:session_id/mcp-oauth-resolutions/:pending_id` | 处理 MCP OAuth |
| POST | `/embed/:channel_id/sessions/:session_id/mcp-oauth-resolutions/:pending_id/cancel` | 取消 MCP OAuth |
| POST | `/embed/:channel_id/sessions/:session_id/mcp-services/:id/oauth/authorize-url` | 获取 MCP OAuth 授权 URL |
| GET | `/embed/:channel_id/sessions/:session_id/mcp-services/:id/oauth/status` | 检查 MCP OAuth 状态 |
| POST | `/embed/:channel_id/sessions/:session_id/tool-approvals/:pending_id` | 处理工具审批 |
| GET | `/embed/:channel_id/files` | 获取嵌入消息中的文件/图片 |

---

## 渠道管理接口

### POST `/agents/:id/embed-channels` - 创建 Embed 渠道

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 智能体 ID |

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `name` | string | 是 | 渠道名称 |
| `enabled` | bool | 否 | 是否启用（默认 true） |
| `allowed_origins` | string[] | 是* | 允许的来源域名列表（生产环境必填，不能使用 `*`） |
| `welcome_message` | string | 否 | 欢迎消息 |
| `rate_limit_per_minute` | int | 否 | 每分钟速率限制 |
| `rate_limit_per_day` | int | 否 | 每日速率限制 |
| `primary_color` | string | 否 | 主题色（hex，如 `#3B82F6`） |
| `page_title` | string | 否 | 页面标题 |
| `header_title_mode` | string | 否 | 标题栏模式 |
| `show_suggested_questions` | bool | 否 | 是否显示建议问题 |
| `widget_position` | string | 否 | 挂件位置：`bottom-right` / `bottom-left` |
| `allow_web_search` | bool | 否 | 是否允许联网搜索 |
| `allow_memory` | bool | 否 | 是否允许记忆 |
| `allow_file_upload` | bool | 否 | 是否允许文件上传 |
| `default_locale` | string | 否 | 默认语言（如 `zh-CN`、`en`） |
| `webhook_url` | string | 否 | Webhook 通知 URL |
| `webhook_secret` | string | 否 | Webhook 签名密钥 |
| `agent_id` | string | 否 | 关联智能体 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/embed-channels' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "name": "客服挂件",
    "allowed_origins": ["https://example.com"],
    "welcome_message": "你好！我可以帮你解答问题。",
    "primary_color": "#3B82F6",
    "widget_position": "bottom-right"
}'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "id": "emb-00000001",
        "name": "客服挂件",
        "publish_token": "pk-xxxxx",
        "allowed_origins": ["https://example.com"],
        "enabled": true,
        "created_at": "2025-08-12T10:00:00+08:00"
    }
}
```

---

### GET `/agents/:id/embed-channels` - 获取智能体的 Embed 渠道列表

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 智能体 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/embed-channels' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）: `data` 为渠道列表数组。

---

### GET `/embed-channels` - 获取租户下所有 Embed 渠道

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/embed-channels' \
--header 'X-API-Key: sk-xxxxx'
```

---

### GET `/embed-channels/:channel_id` - 获取渠道详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/embed-channels/emb-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

---

### PUT `/embed-channels/:channel_id` - 更新渠道

**请求体**: 同创建，所有字段可选。

---

### DELETE `/embed-channels/:channel_id` - 删除渠道

---

### POST `/embed-channels/:channel_id/rotate-token` - 轮换 Publish Token

轮换后旧 Token 立即失效。

**响应**:

```json
{
    "success": true,
    "data": {
        "publish_token": "pk-new-xxxxx"
    }
}
```

---

### POST `/embed-channels/:channel_id/preview-session` - 创建预览会话

用于在管理端预览 Embed 效果。返回临时预览 Token。

---

### GET `/embed-channels/:channel_id/stats` - 获取渠道统计

返回访问量、消息数等统计数据。

---

## 公开端点

公开端点使用 `EmbedAuth` 鉴权。请求需要在 URL 路径中携带 `channel_id`，鉴权中间件自动验证 Publish Token（通过请求头 `X-Embed-Session` 或 URL 查询参数）。

### POST `/embed/:channel_id/exchange` - 交换 Session Token

用 Publish Token 交换短时效的 Session Token，后续接口使用 Session Token。

**请求体**: 通常为空或包含外部用户标识。

**响应**: 返回 Session Token 和过期信息。

---

### GET `/embed/:channel_id/config` - 获取组件配置

返回前端 Embed 组件初始化所需的配置（主题色、欢迎消息、建议问题等）。

---

### POST `/embed/:channel_id/knowledge-chat/:session_id` - 基于知识库问答

SSE 流式响应，同 `/knowledge-chat/:session_id` 的行为。区别在于认证方式。

---

### POST `/embed/:channel_id/agent-chat/:session_id` - 基于 Agent 问答

SSE 流式响应，同 `/agent-chat/:session_id` 的行为。

---

### GET `/embed/:channel_id/messages/:session_id/load` - 加载历史消息

同 `/messages/:session_id/load` 的行为。

---

### GET `/embed/:channel_id/files` - 获取文件/图片

通过 `file_path` 查询参数获取嵌入消息中的图片等文件。

**查询参数**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `file_path` | string | 是 | 文件路径（如 `minio://bucket/path/to/file.png`） |
