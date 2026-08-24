# API 参考：会话、消息与聊天

路由注册：`internal/router/router.go` 的 `RegisterSessionRoutes`、`RegisterChatRoutes`、`RegisterMessageRoutes`。Handler：`internal/handler/session/`（handler.go、qa.go、stream.go、title.go、temporary_document.go）、`internal/handler/message.go`、`internal/handler/message_suggestion.go`。

会话为“用户私有”资源，handler 内部强制归属校验；路由层为 Viewer+。API key：会话/聊天需 `chat` capability（或 full-access）；消息搜索需 `message_history`；知识检索需 `retrieve`。

## 会话（/api/v1/sessions）

### POST /api/v1/sessions

用途：创建会话。Handler: `internal/handler/session/handler.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `title` | string | 否 | 标题 |
| `description` | string | 否 | 描述 |

响应：201 `{"success":true,"data":{Session}}`（`id,title,description,tenant_id,user_id,is_pinned,last_request_state,created_at,...`）

```bash
curl -X POST $BASE/api/v1/sessions -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' -d '{"title":"新对话"}'
```

### GET /api/v1/sessions

用途：会话列表。Handler: `internal/handler/session/handler.go`

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `page` / `page_size` | int | 否 | 分页 |
| `keyword` | string | 否 | 标题模糊搜索 |
| `source` | string | 否 | 来源过滤（web/embed/api/feishu/wechat/slack/...） |
| `agent_id` | string | 否 | 按 Agent 过滤（IM 会话） |

响应：200 `{"success":true,"data":[SessionListItem],"total","page","page_size"}`

```bash
curl "$BASE/api/v1/sessions?page=1" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/sessions/:id

用途：会话详情。

响应：200 `{"success":true,"data":{Session}}`

```bash
curl $BASE/api/v1/sessions/s-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/sessions/:id

用途：更新会话（标题/描述/置顶）。请求体：`title`、`description`、`is_pinned`（均可选）。

响应：200 `{"success":true,"data":{Session}}`

```bash
curl -X PUT $BASE/api/v1/sessions/s-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"title":"重命名"}'
```

### DELETE /api/v1/sessions/:id

用途：删除会话。

响应：200 `{"success":true,"message":"Session deleted successfully"}`

```bash
curl -X DELETE $BASE/api/v1/sessions/s-1 -H "Authorization: Bearer $TOKEN"
```

### DELETE /api/v1/sessions/batch

用途：批量删除会话。请求体：`{"ids":["s-1"],"delete_all":false}`（二选一：`ids` 或 `delete_all:true`）。

响应：200 `{"success":true,"message":"Sessions deleted successfully"}`

```bash
curl -X DELETE $BASE/api/v1/sessions/batch -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"ids":["s-1","s-2"]}'
```

### DELETE /api/v1/sessions/:id/messages

用途：清空会话消息。

响应：200 `{"success":true,"message":"Session messages cleared successfully"}`

```bash
curl -X DELETE $BASE/api/v1/sessions/s-1/messages -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/sessions/:session_id/generate_title

用途：根据上下文消息生成会话标题。Handler: `internal/handler/session/title.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `messages` | []Message | 是（`binding:"required"`） | 用作上下文的消息 |

响应：200 `{"success":true,"data":"生成的标题"}`

```bash
curl -X POST $BASE/api/v1/sessions/s-1/generate_title -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"messages":[{"role":"user","content":"介绍下产品"}]}'
```

### POST /api/v1/sessions/:session_id/stop

用途：停止正在生成的回答。Handler: `internal/handler/session/stream.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `message_id` | string | 是（`binding:"required"`） | 助手消息 ID |

响应：200 `{"success":true,"message":"Generation stopped"}`

```bash
curl -X POST $BASE/api/v1/sessions/s-1/stop -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"message_id":"m-1"}'
```

### POST /api/v1/sessions/:session_id/pin 与 DELETE /api/v1/sessions/:id/pin

用途：置顶 / 取消置顶会话。无请求体。Handler: `internal/handler/session/handler.go`

响应：200 `{"success":true,"is_pinned":true|false}`

```bash
curl -X POST $BASE/api/v1/sessions/s-1/pin -H "Authorization: Bearer $TOKEN"
curl -X DELETE $BASE/api/v1/sessions/s-1/pin -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/sessions/continue-stream/:session_id

用途：断线续传活跃流（重放历史事件 + 100ms 轮询新增量）。Handler: `internal/handler/session/stream.go`

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `message_id` | string | 是 | 要续传的助手消息 ID |

响应：200 SSE（`text/event-stream`，事件格式见总览“流式接口协议”）。

```bash
curl -N "$BASE/api/v1/sessions/continue-stream/s-1?message_id=m-1" -H "Authorization: Bearer $TOKEN"
```

## 会话附件（临时文档）

Handler: `internal/handler/session/temporary_document.go`

### POST /api/v1/sessions/:session_id/attachments

用途：上传会话级临时文档（异步解析）。multipart 字段：`file`（必填）、`agent_id`（可选，决定解析引擎/ASR 模型）、`parser_engine`（可选）。

响应：202 `{"success":true,"data":{TemporaryDocument}}`（`id,session_id,file_name,file_type,file_size,status(uploaded/processing/ready/failed),resource_ref,...`）

```bash
curl -X POST $BASE/api/v1/sessions/s-1/attachments -H "Authorization: Bearer $TOKEN" -F 'file=@notes.pdf'
```

### GET /api/v1/sessions/:id/attachments

用途：附件列表。

响应：200 `{"success":true,"data":[TemporaryDocument]}`

```bash
curl $BASE/api/v1/sessions/s-1/attachments -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/sessions/:id/attachments/:attachment_id

用途：附件详情（含解析状态）。

响应：200 `{"success":true,"data":{TemporaryDocument}}`

```bash
curl $BASE/api/v1/sessions/s-1/attachments/a-1 -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/sessions/:id/attachments/:attachment_id/preview

用途：附件原文件预览。

响应：200 文件流（`Content-Disposition: inline|attachment`，`Cache-Control: private`）。

```bash
curl $BASE/api/v1/sessions/s-1/attachments/a-1/preview -H "Authorization: Bearer $TOKEN" -o preview.pdf
```

### DELETE /api/v1/sessions/:id/attachments/:attachment_id

用途：删除附件。

响应：204 No Content

```bash
curl -X DELETE $BASE/api/v1/sessions/s-1/attachments/a-1 -H "Authorization: Bearer $TOKEN"
```

## 回答建议（Suggestions）

Handler: `internal/handler/message_suggestion.go`

### GET /api/v1/sessions/:id/messages/:message_id/suggestions

用途：读取某助手消息的追问建议。

响应：200 `{"success":true,"data":{MessageSuggestionSet}}`（`status(generating/ready/suppressed/failed),questions:[{id,text,category,source,knowledge_base_ids}],allow_regenerate,...`）

```bash
curl $BASE/api/v1/sessions/s-1/messages/m-1/suggestions -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/sessions/:session_id/messages/:message_id/suggestions

用途：确保生成建议（幂等触发）。请求体：`{"regenerate":true}`（可选，强制重新生成）。

响应：200（就绪）或 202（生成中）`{"success":true,"data":{MessageSuggestionSet|null}}`

```bash
curl -X POST $BASE/api/v1/sessions/s-1/messages/m-1/suggestions -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}'
```

### POST /api/v1/sessions/:session_id/suggestion-events

用途：上报建议交互事件（埋点）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `suggestion_set_id` | string | 是（`binding:"required"`） | 建议集 ID |
| `question_id` | string | 否 | click/regenerate 时必填 |
| `event_type` | string | 是（`binding:"required"`） | `impression/click/dismiss/regenerate` |

响应：204 No Content

```bash
curl -X POST $BASE/api/v1/sessions/s-1/suggestion-events -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"suggestion_set_id":"ss-1","event_type":"impression"}'
```

## 聊天与检索

Handler: `internal/handler/session/qa.go`。API key：聊天需 `chat`/full；`knowledge-search` 需 `retrieve`/full。

### POST /api/v1/knowledge-chat/:session_id

用途：知识库问答（SSE 流式）。

请求体（KnowledgeQA/AgentQA 共用）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query` | string | 是（`binding:"required"`） | 用户问题 |
| `knowledge_base_ids` | []string | 否 | 检索的 KB |
| `knowledge_ids` | []string | 否 | 限定知识文件 |
| `agent_enabled` | bool | 否 | 是否启用 Agent 模式 |
| `agent_id` | string | 否 | 自定义 Agent ID |
| `web_search_enabled` | bool | 否 | 联网搜索 |
| `summary_model_id` | string | 否 | 总结模型 |
| `mcp_service_ids` | []string | 否 | @提及的 MCP 服务 |
| `skill_names` | []string | 否 | @提及的技能 |
| `tag_ids` | []string | 否 | 标签过滤 |
| `mentioned_items` | []object | 否 | @提及项（type/kb_id/kb_name/service_id/skill_name） |
| `disable_title` | bool | 否 | 禁用自动标题 |
| `images` | []object | 否 | 图片（`data` base64 / `url` / `caption`） |
| `attachment_uploads` | []object | 否 | 内联附件（`data` base64、`file_name`、`file_size`） |
| `attachment_ids` | []string | 否 | 已上传的会话附件 ID |
| `channel` | string | 否 | 来源渠道 |
| `suggestion_attribution` | object | 否 | 点击建议的归因信息 |

响应：200 SSE 流，`event: message` + `data: StreamResponse`（见总览），以 `complete` 事件结束。

```bash
curl -N -X POST $BASE/api/v1/knowledge-chat/s-1 -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"query":"退款政策是什么?","knowledge_base_ids":["kb-1"]}'
```

### POST /api/v1/agent-chat/:session_id

用途：Agent 问答（SSE 流式，含 `thinking/tool_call/tool_result/tool_approval_required/mcp_oauth_required` 等事件）。请求体同上。

```bash
curl -N -X POST $BASE/api/v1/agent-chat/s-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"query":"分析上季度数据","agent_id":"agent-1"}'
```

### POST /api/v1/knowledge-search

用途：无会话知识检索（非流式）。Handler: `internal/handler/session/qa.go` 的 `SearchKnowledge`。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query` | string | 是（`binding:"required"`） | 查询 |
| `knowledge_base_id` | string | 否 | 单 KB（兼容旧版） |
| `knowledge_base_ids` | []string | 否 | 多 KB |
| `knowledge_ids` | []string | 否 | 限定文件 |
| `tag_ids` | []string | 否 | 标签过滤 |
| `mentioned_items` | []object | 否 | 带 KB 范围的标签提及 |

响应：200 `{"success":true,"data":[SearchResult]}`（`id,content,knowledge_id,knowledge_title,score,chunk_type,knowledge_base_id,...`）

```bash
curl -X POST $BASE/api/v1/knowledge-search -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' -d '{"query":"部署要求","knowledge_base_ids":["kb-1"]}'
```

## 消息（/api/v1/messages）

Handler: `internal/handler/message.go`

### POST /api/v1/messages/search

用途：聊天历史搜索。权限：Viewer+；API key `message_history`/full。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query` | string | 是（`binding:"required"`） | 查询 |
| `mode` | string | 否 | `keyword/vector/hybrid`（默认 hybrid） |
| `limit` | int | 否 | 默认 20 |
| `session_ids` | []string | 否 | 限定会话 |

响应：200 `{"success":true,"data":{"total":N,"results":[{session_id,message_id,role,content,created_at,score}]}}`

```bash
curl -X POST $BASE/api/v1/messages/search -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"query":"报价"}'
```

### GET /api/v1/messages/chat-history-stats

用途：聊天历史索引统计。权限：Viewer+；API key `message_history`/full。

响应：200 `{"success":true,"data":{indexed_message_count,knowledge_base_size,last_indexed_at,...}}`

```bash
curl $BASE/api/v1/messages/chat-history-stats -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/messages/:session_id/load

用途：加载会话消息（时间游标向前翻页）。权限：Viewer+；API key `chat`/full。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `limit` | int | 否 | 默认 20 |
| `before_time` | string | 否 | RFC3339/RFC3339Nano 时间戳 |

响应：200 `{"success":true,"data":[Message]}`（`id,session_id,role,content,is_completed,images,attachments,agent_steps,...`）

```bash
curl "$BASE/api/v1/messages/s-1/load?limit=20" -H "X-API-Key: $API_KEY"
```

### DELETE /api/v1/messages/:session_id/:id

用途：删除单条消息。权限：Viewer+（handler 校验会话归属）；API key `chat`/full。

响应：200 `{"success":true,"message":"Message deleted successfully"}`

```bash
curl -X DELETE $BASE/api/v1/messages/s-1/m-1 -H "Authorization: Bearer $TOKEN"
```
