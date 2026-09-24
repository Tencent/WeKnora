# API 参考：会话、消息与聊天

创建和管理会话，读取消息与临时附件，并通过 SSE 获取知识问答或智能体回答。

会话为“用户私有”资源，handler 内部强制归属校验；路由层为 Viewer+。API key：会话/聊天需 `chat` capability（或 full-access）；消息搜索需 `message_history`；知识检索需 `retrieve`。

## 会话（/api/v1/sessions）

### POST /api/v1/sessions

用途：创建会话。Handler: `internal/handler/session/handler.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `title` | string | 否 | 标题 |
| `description` | string | 否 | 描述 |
| `project_dir` | string | 否 | 仅桌面版：把会话绑定到用户已批准的本机项目目录（绝对路径，须与批准列表完全一致），否则返回 400 |

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

停止时，尚未送达智能体的追加消息（见下文[向运行中的回答追加消息](#steer)）会一并丢弃，不会自动开始新一轮。

### POST /api/v1/sessions/:session_id/fork

用途：从某条历史消息分叉出新会话，源会话保持不变。只有会话归属人可以分叉。Handler: `internal/handler/session/fork.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `message_id` | string | 是 | 分叉点。用户消息：复制它之前的历史，客户端通常把该问题预填到输入框；助手消息：复制到这条回答为止的历史 |
| `title` | string | 否 | 新会话标题，默认“源标题（分支）” |

复制的消息保留原时间线和生成文件；新会话记录 `parent_session_id` 与 `forked_from_message_id`。源会话绑定沙箱时，系统会给沙箱做快照，新会话首次使用沙箱时从分叉点对应轮次的工作区状态启动。无法携带工作区时分叉仍然成功，但返回 `degraded: true` 和原因：

| `reason` | 含义 |
| --- | --- |
| `NO_CHECKPOINT` | 分叉点之前的轮次没有工作区检查点 |
| `SANDBOX_REPLACED` | 检查点属于会话已更换的旧沙箱 |
| `SANDBOX_GONE` | 源会话当前没有可快照的沙箱 |
| `SNAPSHOT_UNSUPPORTED` | 沙箱后端不支持快照或快照失败 |

响应：200 `{"success":true,"data":{"session_id":"新会话 ID","degraded":false}}`。源会话正在生成、或分叉点是未完成的回答时返回 409（`code: FORK_SOURCE_BUSY`）；会话或消息不存在返回 404；分叉点不是用户或助手消息返回 400。

```bash
curl -X POST $BASE/api/v1/sessions/s-1/fork -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"message_id":"m-3"}'
```

### POST /api/v1/sessions/:session_id/rewind

用途：把当前会话原地回退到某条消息。只有会话归属人可以回退。Handler: `internal/handler/session/rewind.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `message_id` | string | 是 | 回退点。用户消息：删除它本身及之后的消息；助手消息：保留这条回答，删除之后的消息 |

被删除消息的生成文件、追问建议和聊天历史索引会一并清理。会话绑定沙箱时，系统同时把 `/workspace` 重置到保留历史中最后一轮的检查点；工作区重置失败时整个操作失败，消息不会被删除。

响应：200 `{"success":true,"data":{"deleted_messages":N,"workspace_reset":true,"reason":""}}`。`workspace_reset` 为 false 时，`reason` 说明只回退了对话的原因：`NO_SANDBOX`（会话未绑定沙箱）或 `NO_CHECKPOINT`（保留的历史没有检查点）。

| 状态码 | `code` | 含义 |
| --- | --- | --- |
| 409 | `REWIND_SOURCE_BUSY` | 会话正在生成，或回退点是未完成的回答 |
| 409 | `REWIND_NO_CHECKPOINT` | 沙箱仍在，但保留的历史找不到可用检查点；为避免文件领先于对话而拒绝 |
| 409 | `REWIND_SANDBOX_REPLACED` | 检查点属于已更换的旧沙箱 |
| 404 | — | 会话或消息不存在 |
| 500 | — | 工作区重置失败，可重试 |

```bash
curl -X POST $BASE/api/v1/sessions/s-1/rewind -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"message_id":"m-3"}'
```

### 向运行中的回答追加消息（steer） {#steer}

智能推理模式的回答生成期间，同一会话再次调用 `agent-chat` 会返回 409。此时可用以下接口把新消息排入当前轮。只有会话归属人可调用；快速问答没有可注入的执行循环，不支持追加。Handler: `internal/handler/session/steer.go`

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| POST | `/api/v1/sessions/:session_id/steer` | 追加一条消息 |
| GET | `/api/v1/sessions/:id/steer` | 列出当前轮尚未送达的排队消息，用于刷新页面后恢复队列；返回 `assistant_message_id` 与 `items[]`（`steer_id`、`content`、`delivery`、`mentioned_items`），无运行中的轮次时 `items` 为空 |
| DELETE | `/api/v1/sessions/:id/steer/:steer_id` | 撤回一条排队消息 |
| POST | `/api/v1/sessions/:session_id/steer/:steer_id/inject` | 把一条 `after` 消息改为 `inject` |

POST 请求体：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query` | string | 是 | 消息内容，最多 10000 字符 |
| `delivery` | string | 否 | `after`（默认）：当前轮结束后作为下一轮问题发出；`inject`：在当前轮下一次迭代边界（包括即将给出最终回答前）送达智能体，智能体据此调整后继续 |
| `mentioned_items` | []object | 否 | @提及项，格式同聊天请求 |
| `steer_id` | string | 否 | 客户端生成的 UUID，用于重试去重；同一 ID 对应不同内容返回 409 |
| `expected_assistant_message_id` | string | 否 | 客户端看到的运行中助手消息 ID；运行已切换时返回 409，客户端应重试 |
| `channel` | string | 否 | 来源渠道 |

响应中的 `status`：

| `status` | 含义 |
| --- | --- |
| `queued` | 已排队，返回 `steer_id`、`delivery` 与 `assistant_message_id` |
| `new_run` | 当前没有运行中的轮次，客户端应改为正常调用 `agent-chat` 发送 |
| `already_injected` | 该消息已经送达智能体（重试、撤回或改为注入时可能出现），无法撤回 |
| `deleted` / `gone` | 仅 DELETE：已撤回（`removed` 表示是否确实删除了一条）/ 当前没有运行中的轮次 |

每轮最多同时排队 10 条未送达消息，超出返回 400。当前轮正常结束后，第一条未送达的消息由服务端直接作为下一轮的问题启动，其余消息按原投递方式转入该轮；这一轮没有对应的 `agent-chat` 连接，可用 `GET /steer` 返回的 `assistant_message_id` 调用 continue-stream 接收。用户停止生成时，未送达的消息全部丢弃。已送达的消息写入会话历史，并通过 `user_message_injected` 事件通知客户端。查询运行状态失败时返回 503，可重试。

```bash
curl -X POST $BASE/api/v1/sessions/s-1/steer -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"query":"只看 2024 年的数据","delivery":"inject"}'
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

响应：200 SSE（`text/event-stream`，事件格式见总览“流式接口协议”）。重放已存储的事件时，同一事件 ID 下连续的未完成 `answer`/`thinking`/`reflection` 增量会合并为一帧，按事件 ID 累积内容的客户端得到的文本不变；实时推送部分不合并。

```bash
curl -N "$BASE/api/v1/sessions/continue-stream/s-1?message_id=m-1" -H "Authorization: Bearer $TOKEN"
```

## 沙箱图形桌面 {#sandbox-desktop}

这些路由由 `internal/router/routes_chat.go` 注册，尚未进入 Swagger。仅 Cube/E2B 桌面模板支持；部署和代理要求见[沙箱部署](../06-development/04-sandbox-deployment.md)。

### POST /api/v1/sessions/:session_id/sandbox/desktop-ticket

签发两分钟有效的一次性 WebSocket 票据。需要会话属主的有效登录 Bearer access token，不能只用 API Key。JWT 只放在本次 POST 的认证头中。

```bash
curl -X POST "$BASE/api/v1/sessions/$SESSION_ID/sandbox/desktop-ticket" \
  -H "Authorization: Bearer $TOKEN"
```

响应：200 `{"success":true,"data":{"ticket":"<opaque-ticket>","expires_in":120}}`。

### GET /api/v1/sessions/:id/sandbox/desktop

WebSocket 握手使用 `?ticket=<opaque-ticket>`，不走普通 JWT 中间件。票据绑定用户、空间、会话与原 access token，使用一次即失效；用过、过期、未知票据统一拒绝。代理日志不能记录 ticket query。

每会话同时仅允许一条中继。沙箱未绑定、暂停、不支持桌面或启动失败等状态可能先完成 WebSocket upgrade，再以 `SANDBOX_NOT_BOUND`、`SANDBOX_PAUSED`、`DESKTOP_UNSUPPORTED`、`DESKTOP_START_FAILED` 等 close reason 断开，客户端应读取关闭原因。

### POST /api/v1/sessions/:session_id/sandbox/desktop/activity

会话属主上报键鼠活动，响应 200 `{"success":true}`。仅当服务端 RFB parser 降级时用于续期；parser 正常时忽略，不应通过轮询延长沙箱寿命。

## 会话附件（临时文档）

Handler: `internal/handler/session/temporary_document.go`

### POST /api/v1/sessions/:session_id/attachments

用途：上传会话级临时文档（异步解析）。multipart 字段：`file`（必填）、`agent_id`（可选，决定解析引擎/ASR 模型）、`parser_engine`（可选；使用共享智能体时忽略，由智能体的解析规则决定）。

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
| `query` | string | 是（`binding:"required"`） | 用户问题；为空、仅含空白、含控制字符或非法 UTF-8 时返回 400 |
| `knowledge_base_ids` | []string | 否 | 检索的 KB |
| `knowledge_ids` | []string | 否 | 限定知识文件 |
| `agent_enabled` | bool | 否 | 是否启用 Agent 模式 |
| `agent_id` | string | 否 | 自定义 Agent ID |
| `agent_source_tenant_id` | uint64 | 否 | 共享智能体的来源空间，同名共享智能体来自多个空间时用于区分 |
| `reasoning_effort` | string | 否 | 本轮思考强度：`off`、`auto`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`（`none`/`false` 视为 `off`，`true`/`on` 视为 `auto`）；省略时沿用智能体配置。只作用于本轮，不修改智能体；模型不支持所选档位时自动调整到相近档位。非法值返回 400 |
| `web_search_enabled` | bool | 否 | 联网搜索；只在智能体本身开启联网搜索时生效 |
| `local_browser_enabled` | bool | 否 | 本轮允许使用已连接的本机浏览器；只能用于智能推理智能体的 `agent-chat`，否则返回 400。见[本机浏览器](../05-clients/09-local-browser.md) |
| `summary_model_id` | string | 否 | 总结模型；使用共享智能体时忽略，始终使用智能体配置的模型 |
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
| `question_origin` | object | 否 | 用户点选的建议问题来自哪里：`knowledge_base_id`、可选 `knowledge_id`。仅智能推理模式使用，智能体会先检索该来源；来源不在本轮检索范围内时忽略，不会扩大范围 |

所选 `reasoning_effort` 会写入会话的 `last_request_state.reasoning_effort`，重新打开会话时前端据此恢复。

响应：200 SSE 流，`event: message` + `data: StreamResponse`（见总览），以 `complete` 事件结束。回答因模型单次输出上限被截断时，`answer` 事件带 `data.truncated: true`，截断前已生成的内容照常保留。

```bash
curl -N -X POST $BASE/api/v1/knowledge-chat/s-1 -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"query":"退款政策是什么?","knowledge_base_ids":["kb-1"]}'
```

### POST /api/v1/agent-chat/:session_id

用途：Agent 问答（SSE 流式，含 `thinking/tool_call/tool_result/tool_approval_required/mcp_oauth_required` 等事件）。请求体同上。

- 工具执行失败以 `tool_result` 事件返回（`data.success: false`，`data.error` 为原因），智能体会继续处理；`error` 事件只表示整轮执行失败。
- 智能体中途接收追加消息时发出 `user_message_injected`；命令执行过程中以 `command_output` 更新工具卡片输出。
- 同一会话已有智能推理回答在生成时返回 409，此时应改用 [steer 接口](#steer)追加消息。

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

## 会话生成文件

以下接口要求 Viewer+，API Key 需 chat 或 full-access，并按会话归属校验。不存在或不可访问的会话返回 404。

| 方法 | 路径 | 响应 |
| --- | --- | --- |
| GET | `/api/v1/sessions/:id/artifacts` | 200 `{success:true,data:[Artifact]}`，汇总会话文件 |
| GET | `/api/v1/sessions/:id/messages/:message_id/artifacts` | 同上，仅本条消息文件 |
| GET | `/api/v1/sessions/:id/messages/:message_id/artifacts/:index/download` | 200 文件流，Content-Disposition: attachment |
| DELETE | `/api/v1/sessions/:id/messages/:message_id/artifacts/:index` | 200 `{success:true,data:{file_name,deleted}}`，删除该文件 |

Artifact 字段：index、handle（可选 resource:// 引用）、file_name、file_type、file_size、source_path、mod_time、created_at。响应不返回底层对象存储 URL。下载 index 从 0 开始，必须使用对应消息列表的索引，不能拿会话汇总索引直接拼消息下载地址。非法索引返回 400，越界或文件不存在返回 404。

```bash
curl "$BASE/api/v1/sessions/session-1/messages/message-1/artifacts" \
  -H "Authorization: Bearer $TOKEN"
curl "$BASE/api/v1/sessions/session-1/messages/message-1/artifacts/0/download" \
  -H "Authorization: Bearer $TOKEN" -o result.pdf
```

### 删除生成的文件

删除会回收对象存储中的字节，**不可恢复**。与下载不同，删除只对会话归属人开放：通过共享智能体获得的只读访问可以下载文件，但不能删除 —— 不属于自己的会话一律按 404 处理（不区分「不存在」和「无权限」，与其余会话接口一致）。已删除的文件返回 404，重复删除同样返回 404。

字节只有在没有任何其他持有者时才会真正回收：同一份文件被存入知识库、被后续回答重新引用，或者所在会话被分叉出副本，都会让它保留下来。回收失败不影响删除结果（接口仍返回 200），文件在各处列表中都已消失。

删除后该文件在列表接口和产物库中都不再出现，但它在消息中的**位置会被保留**：`index` 就是下载地址，如果后面的文件依次前移，已有的下载链接就会指向错的文件。同一原因，沙箱里的同名文件不会在下一轮采集时被重新收录 —— 它的 mtime 并没有因为用户删除而改变。

| 参数 | 说明 |
| --- | --- |
| `all_versions` | 连同本会话中同一 `source_path` 的所有历史版本一并删除。布尔值，默认 `false` |

```bash
curl -X DELETE "$BASE/api/v1/sessions/session-1/messages/message-1/artifacts/0" \
  -H "Authorization: Bearer $TOKEN"
```

### 跨会话产物列表

`GET /api/v1/artifacts` 列出当前用户网页对话中的所有生成文件，供首页侧栏「产物」页使用。范围与会话列表的 `source=web` 一致：本人会话及历史上无归属的租户级网页会话；IM 渠道、网页挂件（embed）和 API Key 会话一律不含，即使 IM 会话在库中没有归属人。同一会话中 `source_path` 相同的文件视为同一文件的多个版本，只返回最新一版，`version_count` 给出版本数。已删除的会话或消息中的文件不返回。权限要求与上表相同。

| 参数 | 说明 |
| --- | --- |
| `keyword` | 按文件名过滤，不区分大小写 |
| `file_types` | 逗号分隔的扩展名，如 `.pdf,.pptx`（可省略点号） |
| `page` / `page_size` | 分页，`page_size` 最大 100，默认 20 |

响应 `{success, data:[LibraryArtifact], total, page, page_size}`，按生成时间倒序。LibraryArtifact 字段：session_id、session_title、message_id、index、handle（可选）、file_name、file_type、file_size、source_path、created_at、version_count。下载时用其中的 session_id、message_id、index 调用上表的下载接口。

```bash
curl "$BASE/api/v1/artifacts?file_types=.pptx,.pdf&keyword=报告&page=1&page_size=30" \
  -H "Authorization: Bearer $TOKEN"
```

`DELETE /api/v1/artifacts` 删除产物库中的一个文件，用 query 参数 `session_id`、`message_id`、`index` 定位，语义与上面的会话内删除一致。区别只在于 `all_versions` 缺省时视为 `true`（显式传值时两个接口的解析规则相同）：产物库一行代表一个文件（`version_count` 给出版本数）而不是某一次生成，只删最新一版会让这一行继续留在列表里、显示上一版。传 `all_versions=false` 可只删当前这一版。

```bash
curl -X DELETE "$BASE/api/v1/artifacts?session_id=session-1&message_id=message-1&index=0" \
  -H "Authorization: Bearer $TOKEN"
```

### 回答中的图片和文件引用

`GET /api/v1/sessions/:id/messages/:message_id/files?file_path=...` 是消息级鉴权代理。file_path 传该消息引用的资源句柄或受支持的存储引用，客户端应 URL 编码。后端校验消息访问权、资源与消息的绑定及知识库/共享 Agent 的当前访问权；任意文件路径不能凭会话 ID 访问。授权撤销后旧消息引用也会被拒绝。适用于共享 Agent、组织共享库回答图片与消息产物，详见[文件访问](../03-features/21-file-access.md)。

### 每轮用量

消息返回持久化的 usage，Agent 完成事件携带 turn_usage；包含本轮各用途模型调用聚合结果。工具调用自身并不都产生 Token，用量以提供商返回或后端已采集的记录为准。字段见[可观测性](../03-features/16-observability.md)。

## 实现参考

路由注册：`internal/router/routes_chat.go` 的 `RegisterSessionRoutes`、`RegisterChatRoutes`、`RegisterMessageRoutes`（由 `internal/router/router.go` 调用）。Handler：`internal/handler/session/`（handler.go、qa.go、stream.go、title.go、temporary_document.go、fork.go、rewind.go、steer.go、artifact_*.go）、`internal/handler/message.go`、`internal/handler/message_suggestion.go`。
