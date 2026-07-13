# IM 渠道 API

[返回目录](./README.md)

IM 渠道接口用于管理企业微信、飞书、Slack、Telegram、钉钉、Mattermost、微信、QQ 机器人等 IM 平台的渠道配置和回调。

## 说明

- **渠道 CRUD** 需要认证（`X-API-Key` 或 `Authorization: Bearer`）。
- **回调端点** 使用各平台自身的签名验证，不需要 WeKnora 认证。
- 完整集成流程见 [../IM集成开发文档.md](../IM集成开发文档.md)。

## 端点一览

### 渠道管理（需要认证）

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| POST | `/agents/:id/im-channels` | 为智能体创建 IM 渠道 |
| GET | `/agents/:id/im-channels` | 获取智能体的 IM 渠道列表 |
| GET | `/im-channels` | 获取租户下所有 IM 渠道（跨智能体） |
| PUT | `/im-channels/:id` | 更新 IM 渠道 |
| DELETE | `/im-channels/:id` | 删除 IM 渠道 |
| POST | `/im-channels/:id/toggle` | 切换渠道启用/停用状态 |

### 回调（不需要 WeKnora 认证）

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/im/callback/:channel_id` | IM 平台回调（URL 验证） |
| POST | `/im/callback/:channel_id` | IM 平台回调（接收消息） |

---

## POST `/agents/:id/im-channels` - 创建 IM 渠道

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 智能体 ID |

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `platform` | string | 是 | 平台标识：`wecom` / `feishu` / `slack` / `telegram` / `dingtalk` / `mattermost` / `wechat` / `qqbot` |
| `name` | string | 否 | 渠道名称 |
| `mode` | string | 否 | 连接模式：`websocket` / `webhook` / `longpoll`（平台默认值自动填充） |
| `output_mode` | string | 否 | 输出模式：`stream` / `full`（平台默认值自动填充） |
| `knowledge_base_id` | string | 否 | 绑定的知识库 ID |
| `credentials` | object | 否 | 平台凭证（JSON 对象，各平台格式不同） |
| `enabled` | bool | 否 | 是否启用（默认 `true`） |

**请求**（飞书渠道示例）:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/im-channels' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "platform": "feishu",
    "name": "客服机器人",
    "knowledge_base_id": "kb-00000001",
    "credentials": {
        "app_id": "cli_xxx",
        "app_secret": "xxx"
    }
}'
```

**响应**（200 OK）:

```json
{
    "data": {
        "id": "imch-00000001",
        "tenant_id": 1,
        "agent_id": "agent-00000001",
        "platform": "feishu",
        "name": "客服机器人",
        "mode": "websocket",
        "output_mode": "stream",
        "knowledge_base_id": "kb-00000001",
        "enabled": true
    }
}
```

**错误**: 平台标识不合法 → 400；同一平台重复绑定 → 409。

---

## GET `/agents/:id/im-channels` - 获取智能体的 IM 渠道列表

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 智能体 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/im-channels' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）: `data` 为渠道摘要数组（不含凭证敏感字段）。

---

## GET `/im-channels` - 获取租户下所有 IM 渠道

跨智能体概览页使用，不含凭证信息。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/im-channels' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）: `data` 为渠道数组（不含 `credentials` 字段）。

---

## PUT `/im-channels/:id` - 更新 IM 渠道

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 渠道 ID |

**参数说明（请求体）**——所有字段可选：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `name` | string | 渠道名称 |
| `mode` | string | 连接模式 |
| `output_mode` | string | 输出模式 |
| `knowledge_base_id` | string | 绑定的知识库 ID |
| `credentials` | object | 平台凭证 |
| `enabled` | bool | 是否启用 |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/im-channels/imch-00000001' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "name": "客服机器人 V2",
    "enabled": true
}'
```

**响应**（200 OK）: 返回更新后的渠道对象。

---

## DELETE `/im-channels/:id` - 删除 IM 渠道

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 渠道 ID |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/im-channels/imch-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true
}
```

---

## POST `/im-channels/:id/toggle` - 切换渠道启用/停用状态

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 渠道 ID |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/im-channels/imch-00000001/toggle' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）: 返回切换后的渠道对象。

---

## GET/POST `/im/callback/:channel_id` - IM 平台回调

**不需要 WeKnora 认证**。使用各平台自身的签名/Token 验证。

- `GET`：用于平台 URL 验证（如企业微信/飞书的 URL 有效性校验）。
- `POST`：用于接收平台推送的消息事件。

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `channel_id` | string | 渠道 ID |

---

## 枚举值

### IM 平台 (Platform)

| 值 | 说明 |
| ---- | ---- |
| `wecom` | 企业微信 |
| `feishu` | 飞书 |
| `slack` | Slack |
| `telegram` | Telegram |
| `dingtalk` | 钉钉 |
| `mattermost` | Mattermost |
| `wechat` | 微信公众号/小程序 |
| `qqbot` | QQ 机器人 |

### 连接模式 (Mode)

| 值 | 说明 |
| ---- | ---- |
| `websocket` | WebSocket 长连接（默认，飞书/Slack/Telegram） |
| `webhook` | Webhook 回调（Mattermost 默认） |
| `longpoll` | 长轮询（微信专用） |

### 输出模式 (Output Mode)

| 值 | 说明 |
| ---- | ---- |
| `stream` | 流式输出（默认） |
| `full` | 完整输出（微信专用） |
