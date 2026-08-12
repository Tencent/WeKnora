# API 参考：IM、Embed 与文件服务

路由注册：`internal/router/router.go` 的 `RegisterIMRoutes`、`RegisterIMChannelRoutes`、`RegisterEmbedChannelRoutes`、`RegisterEmbedPublicRoutes`、`serveFilesWithResources`、`servePresignedFiles`、`servePresignedPreview`、`serveResourceGrants`。Handler：`internal/handler/im.go`、`internal/handler/wechat_qrcode.go`、`internal/handler/embed_channel.go`。

## IM 回调（免全局认证）

### GET|POST /api/v1/im/callback/:channel_id

用途：IM 平台（WeChat/Feishu/Slack/Telegram/DingTalk/QQBot/云之家等）事件回调与 URL 验证。注册在认证中间件之前，使用各平台自身的签名验证；验签失败 403，渠道不存在 404。收到消息立即 ACK，异步处理。Handler: `internal/handler/im.go`

响应：200 `{"success":true}` 或平台要求的 ACK 格式。

```bash
curl -X POST $BASE/api/v1/im/callback/ch-1 -H 'Content-Type: application/json' -d '{"event":"..."}'
```

## IM 渠道管理（需认证）

API key：`manage_channels`/full。IM 渠道携带外部 bot 凭证：列表 Viewer+，变更/开关/扫码登录 Admin+。

### POST /api/v1/agents/:id/im-channels

用途：为 Agent 创建 IM 渠道。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `platform` | string | 是 | `wecom/feishu/lark/slack/telegram/dingtalk/mattermost/wechat/qqbot/yunzhijia` |
| `name` | string | 否 | 显示名 |
| `mode` | string | 否 | `websocket`（默认）/`webhook`/`longpoll`（wechat 强制 longpoll） |
| `output_mode` | string | 否 | `stream`（默认）/`full`（wechat 强制 full） |
| `knowledge_base_id` | string | 否 | 关联 KB |
| `credentials` | object | 否 | 平台凭证 |
| `enabled` | bool | 否 | 默认 true |

响应：200 `{"data":{IMChannel}}`；同渠道 bot 已存在返回 409。

```bash
curl -X POST $BASE/api/v1/agents/agent-1/im-channels -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"platform":"feishu","name":"飞书客服"}'
```

### GET /api/v1/agents/:id/im-channels

用途：某 Agent 的 IM 渠道列表（摘要）。权限：Viewer+。

响应：200 `{"data":[IMChannel]}`

```bash
curl $BASE/api/v1/agents/agent-1/im-channels -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/im-channels

用途：全空间 IM 渠道总览（不含凭证）。权限：Viewer+。

响应：200 `{"data":[IMChannel]}`

```bash
curl $BASE/api/v1/im-channels -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/im-channels/:id

用途：更新渠道（局部更新：`name/mode/output_mode/knowledge_base_id/credentials/enabled/agent_id` 均可选）。权限：Admin+。

响应：200 `{"data":{IMChannel}}`

```bash
curl -X PUT $BASE/api/v1/im-channels/ch-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"enabled":false}'
```

### DELETE /api/v1/im-channels/:id

用途：删除渠道。权限：Admin+。

响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/im-channels/ch-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/im-channels/:id/toggle

用途：启停切换。权限：Admin+。无请求体。

响应：200 `{"data":{IMChannel}}`

```bash
curl -X POST $BASE/api/v1/im-channels/ch-1/toggle -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/wechat/qrcode

用途：生成 WeChat 登录二维码（绑定个人微信到空间）。权限：Admin+。无请求体。Handler: `internal/handler/wechat_qrcode.go`

响应：200 `{"data":{"qrcode_url","qrcode"}}`

```bash
curl -X POST $BASE/api/v1/wechat/qrcode -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/wechat/qrcode/status

用途：轮询扫码状态；确认后返回凭证。权限：Admin+。请求体：`{"qrcode":"<标识>"}`（必填）。

响应：200 `{"data":{"status":"pending|scanned|confirmed|expired","credentials":{bot_token,ilink_bot_id,ilink_user_id,baseurl}}}`

```bash
curl -X POST $BASE/api/v1/wechat/qrcode/status -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"qrcode":"qr-1"}'
```

## Embed 渠道管理（需认证）

API key：`manage_channels`/full。Handler: `internal/handler/embed_channel.go`

### POST /api/v1/agents/:id/embed-channels

用途：为 Agent 创建 Web 嵌入渠道。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 否 | 名称 |
| `enabled` | bool | 否 | 默认 true |
| `allowed_origins` | []string | 是 | 至少一个来源（精确 URL / `*.domain`；生产禁止 `*`） |
| `welcome_message` | string | 否 | 欢迎语 |
| `rate_limit_per_minute` | int | 否 | 每 IP/分钟，默认 30 |
| `rate_limit_per_day` | int | 否 | 渠道/天，默认 10000 |
| `primary_color` / `page_title` / `widget_position` | string | 否 | 外观（position: `bottom-right` 默认等四角） |
| `header_title_mode` | string | 否 | `channel`（默认）/`session` |
| `show_suggested_questions` | bool | 否 | 默认 true |
| `allow_web_search` / `allow_file_upload` | bool | 否 | 默认 false |
| `default_locale` | string | 否 | `zh-CN/en-US/ko-KR/ru-RU`/空（跟随浏览器） |
| `webhook_url` / `webhook_secret` | string | 否 | 访客事件 webhook |
| `agent_id` | string | 否 | 绑定 Agent |

响应：201 `{"success":true,"data":{embedChannelResponse 含 publish_token}}`

```bash
curl -X POST $BASE/api/v1/agents/agent-1/embed-channels -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"官网客服","allowed_origins":["https://example.com"]}'
```

### GET /api/v1/agents/:id/embed-channels

用途：某 Agent 的嵌入渠道列表。权限：Viewer+。

响应：200 `{"success":true,"data":[embedChannelResponse]}`

```bash
curl $BASE/api/v1/agents/agent-1/embed-channels -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/embed-channels

用途：全空间嵌入渠道列表（不含 publish token）。权限：Viewer+。

响应：200 `{"success":true,"data":[embedChannelResponse]}`

```bash
curl $BASE/api/v1/embed-channels -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/embed-channels/:channel_id

用途：渠道详情（含 publish token，用于复制部署代码）。权限：Viewer+。

响应：200 `{"success":true,"data":{embedChannelResponse}}`

```bash
curl $BASE/api/v1/embed-channels/ec-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/embed-channels/:channel_id

用途：更新渠道（字段同创建，均可选）。权限：Admin+。

响应：200 `{"success":true,"data":{embedChannelResponse}}`

```bash
curl -X PUT $BASE/api/v1/embed-channels/ec-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"enabled":false}'
```

### DELETE /api/v1/embed-channels/:channel_id

用途：删除渠道。权限：Admin+。

响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/embed-channels/ec-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/embed-channels/:channel_id/rotate-token

用途：轮换 publish token（旧 token 失效）。权限：Admin+。无请求体。

响应：200 `{"success":true,"data":{embedChannelResponse 含新 publish_token}}`

```bash
curl -X POST $BASE/api/v1/embed-channels/ec-1/rotate-token -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/embed-channels/:channel_id/preview-session

用途：签发管理端预览用短时效 session token（无需 publish token）。权限：Viewer+。

响应：200 `{"success":true,"data":{"session_token","expires_in"}}`；渠道禁用 403。

```bash
curl -X POST $BASE/api/v1/embed-channels/ec-1/preview-session -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/embed-channels/:channel_id/stats

用途：渠道用量统计。权限：Viewer+。

响应：200 `{"success":true,"data":{"session_count":N}}`

```bash
curl $BASE/api/v1/embed-channels/ec-1/stats -H "Authorization: Bearer $TOKEN"
```

## Embed 公开路由（/api/v1/embed/:channel_id，EmbedAuth）

认证：`Authorization: Embed <publish_token|session_token>`；会话级操作附加 `X-Embed-Session: <sig>`。限流与 Origin 校验见总览。Handler: `internal/handler/embed_channel.go`；中间件：`internal/middleware/embed_auth.go`

以下 `$ET` 表示 Embed token 头：`-H "Authorization: Embed $EMBED_TOKEN"`。

### POST /api/v1/embed/:channel_id/exchange

用途：用 publish token 换取短时效 session token（session token 不可再次 exchange）。无请求体。

响应：200 `{"success":true,"data":{"session_token","expires_in"}}`

```bash
curl -X POST $BASE/api/v1/embed/ec-1/exchange -H "Authorization: Embed $PUBLISH_TOKEN"
```

### GET /api/v1/embed/:channel_id/config

用途：渠道公开配置（无密钥）。

响应：200 `{"success":true,"data":{channel_id,name,display_title,knowledge_base_ids,agent_id,agent_name,welcome_message,primary_color,widget_position,allow_web_search,allow_file_upload,default_locale,...}}`

```bash
curl $BASE/api/v1/embed/ec-1/config -H "Authorization: Embed $EMBED_TOKEN"
```

### GET /api/v1/embed/:channel_id/suggested-questions

用途：起始建议问题。查询参数：`limit`（≤12）。

响应：200 `{"success":true,"data":{"questions":[...]}}`

```bash
curl "$BASE/api/v1/embed/ec-1/suggested-questions?limit=6" -H "Authorization: Embed $EMBED_TOKEN"
```

### GET /api/v1/embed/:channel_id/chunks/:chunk_id

用途：查看引用分块（内容脱敏；越权 403）。

响应：200 `{"success":true,"data":{chunk}}`

```bash
curl $BASE/api/v1/embed/ec-1/chunks/c-1 -H "Authorization: Embed $EMBED_TOKEN"
```

### POST /api/v1/embed/:channel_id/sessions

用途：创建访客会话，返回会话 ID 与签名句柄。无请求体。

响应：201 `{"success":true,"data":{"id":"<session_id>","sig":"<签名>"}}`

```bash
curl -X POST $BASE/api/v1/embed/ec-1/sessions -H "Authorization: Embed $EMBED_TOKEN"
```

### POST /api/v1/embed/:channel_id/knowledge-chat/:session_id

用途：访客知识问答（SSE；payload 会被渠道约束改写后委托给 KnowledgeQA）。需 `X-Embed-Session`。请求体同 `/knowledge-chat`（`query` 必填）。

```bash
curl -N -X POST $BASE/api/v1/embed/ec-1/knowledge-chat/s-1 \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG" \
  -H 'Content-Type: application/json' -d '{"query":"营业时间?"}'
```

### POST /api/v1/embed/:channel_id/agent-chat/:session_id

用途：访客 Agent 问答（SSE）。需 `X-Embed-Session`。请求体同上。

```bash
curl -N -X POST $BASE/api/v1/embed/ec-1/agent-chat/s-1 \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG" \
  -H 'Content-Type: application/json' -d '{"query":"帮我下单"}'
```

### GET /api/v1/embed/:channel_id/messages/:session_id/load

用途：加载访客会话消息（委托 `LoadMessages`，查询参数 `limit/before_time`）。需 `X-Embed-Session`。

响应：200 `{"success":true,"data":[Message]}`

```bash
curl "$BASE/api/v1/embed/ec-1/messages/s-1/load?limit=20" \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG"
```

### POST /api/v1/embed/:channel_id/sessions/:session_id/stop

用途：停止生成（委托 StopSession；请求体 `{"message_id":"..."}`）。需 `X-Embed-Session`。

```bash
curl -X POST $BASE/api/v1/embed/ec-1/sessions/s-1/stop \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG" \
  -H 'Content-Type: application/json' -d '{"message_id":"m-1"}'
```

### GET|POST /api/v1/embed/:channel_id/sessions/:session_id/messages/:message_id/suggestions

用途：读取 / 触发生成消息建议（渠道关闭建议时返回 `suppressed`）。需 `X-Embed-Session`。

响应：200 `{"success":true,"data":{"status","questions":[...]}}`

```bash
curl $BASE/api/v1/embed/ec-1/sessions/s-1/messages/m-1/suggestions \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG"
```

### POST /api/v1/embed/:channel_id/sessions/:session_id/suggestion-events

用途：上报建议交互事件（委托 RecordEvent，字段同认证版）。需 `X-Embed-Session`。响应：204。

```bash
curl -X POST $BASE/api/v1/embed/ec-1/sessions/s-1/suggestion-events \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG" \
  -H 'Content-Type: application/json' -d '{"suggestion_set_id":"ss-1","event_type":"impression"}'
```

### POST /api/v1/embed/:channel_id/sessions/:session_id/events

用途：转发访客事件到渠道 webhook。需 `X-Embed-Session`。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `type` | string | 是 | `message_sent` / `message_received` |
| `query` / `content` | string | 否 | 用户问题 / 机器人回复 |

响应：200 `{"success":true}`；不支持的类型 400。

```bash
curl -X POST $BASE/api/v1/embed/ec-1/sessions/s-1/events \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG" \
  -H 'Content-Type: application/json' -d '{"type":"message_sent","query":"你好"}'
```

### MCP OAuth 与工具审批（访客侧）

以下路由均需 `X-Embed-Session`，委托到对应认证版 handler（`internal/handler/mcp_oauth.go`、`internal/handler/mcp_service.go`）：

| 方法+路径 | 用途 |
| --- | --- |
| `POST /api/v1/embed/:channel_id/sessions/:session_id/mcp-oauth-resolutions/:pending_id` | 恢复 OAuth 暂停的运行（体：`service_id` 必填，`decision` 可选） |
| `POST /api/v1/embed/:channel_id/sessions/:session_id/mcp-oauth-resolutions/:pending_id/cancel` | 取消 OAuth 流程 |
| `POST /api/v1/embed/:channel_id/sessions/:session_id/mcp-services/:id/oauth/authorize-url` | 生成授权 URL（体：`redirect_uri` 必填） |
| `GET /api/v1/embed/:channel_id/sessions/:session_id/mcp-services/:id/oauth/status` | 查询授权状态 |
| `POST /api/v1/embed/:channel_id/sessions/:session_id/tool-approvals/:pending_id` | 工具审批（体：`decision` 必填） |

```bash
curl -X POST $BASE/api/v1/embed/ec-1/sessions/s-1/tool-approvals/p-1 \
  -H "Authorization: Embed $EMBED_TOKEN" -H "X-Embed-Session: $SIG" \
  -H 'Content-Type: application/json' -d '{"decision":"approve"}'
```

### GET /api/v1/embed/:channel_id/files

用途：访客侧图片代理（机器人回复内嵌图片；EmbedAuth 注入渠道空间，handler 强制同空间路径）。查询参数：`file_path`（必填）。

响应：200 文件流。

```bash
curl "$BASE/api/v1/embed/ec-1/files?file_path=local://1/exports/chart.png" \
  -H "Authorization: Embed $EMBED_TOKEN" -o chart.png
```

## 文件服务

实现于 `internal/router/router.go`（非 handler 包）。

### GET /files

用途：认证后的统一文件代理（本地/MinIO/COS/TOS 等）。权限：任意已认证空间成员；API key 需非 KB 受限（full-access 或全空间 retrieve，`middleware.AllowFileServeAPIKey()`）；路径强制同空间（`ValidateStoragePathTenant`）。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file_path` | string | 是 | `provider://...` 路径（禁止 `..`；跨空间 403） |

响应：200 文件流（`X-Content-Type-Options: nosniff`；非白名单类型强制 `Content-Disposition: attachment`）。

```bash
curl "$BASE/files?file_path=local://1/docs/a.png" -H "Authorization: Bearer $TOKEN" -o a.png
```

### GET|HEAD /api/v1/files/presigned

用途：HMAC 签名 URL 文件访问（IM 平台内嵌图片；免认证，验签+过期校验，`SYSTEM_AES_KEY` 参与签名）。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file_path` | string | 是 | 存储路径 |
| `tenant_id` | uint64 | 是 | 空间 ID |
| `expires` | string | 是 | Unix 过期时间 |
| `sig` | string | 是 | HMAC 签名 |

响应：200 文件流（HEAD 仅返回头）；签名无效/过期 403。

```bash
curl "$BASE/api/v1/files/presigned?file_path=local://1/x.png&tenant_id=1&expires=1790000000&sig=abc" -o x.png
```

### GET /api/v1/files/presigned-preview

用途：诊断端点：返回给定路径将生成的预签名 HTTP URL。权限：Admin+，显式拒绝 API key（`DenyAPIKeyPrincipal`）。查询参数：`file_path`（必填）。

响应：200 `{"file_path","provider","url","rewritten":bool,"hint"}`

```bash
curl "$BASE/api/v1/files/presigned-preview?file_path=local://1/x.png" -H "Authorization: Bearer $TOKEN"
```

### GET|HEAD /r/:token

用途：短时效资源授权 URL（IM 等无法携带认证头的客户端）。免认证，token 即能力凭证；无效/过期 404。

响应：200 文件流（`Cache-Control: private, max-age=300`）。

```bash
curl $BASE/r/abc123 -o file.png
```
