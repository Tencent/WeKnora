# API 参考：Agent、MCP 与技能

管理智能体、MCP 服务及其凭据、技能和资源收藏。智能体的工具范围与调用审批配置通过本组接口维护。

## Agent（/api/v1/agents）

读：Viewer+（API key `read_agents`/`manage_agents`/`chat`/full）；写：创建者 OR Admin+（API key `manage_agents`/full）；内置 Agent（`is_builtin=true`）始终 Admin+。

### GET /api/v1/agents/placeholders

用途：提示词占位符定义（须先于 `/:id` 注册）。权限：Viewer+。

响应：200 `{"success":true,"data":{"all":{...},"system_prompt":{...},"agent_system_prompt":{...},"context_template":{...},"rewrite_system_prompt":{...},"rewrite_prompt":{...},"fallback_prompt":{...}}}`

```bash
curl $BASE/api/v1/agents/placeholders -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/agents/type-presets

用途：智能推理 Agent 类型预设（rag-qa / wiki-qa / hybrid / custom 等）。权限：Viewer+。

响应：200 `{"success":true,"data":[{type,system_prompt,allowed_tools,kb_compatibility}]}`

```bash
curl $BASE/api/v1/agents/type-presets -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/agents

用途：创建自定义 Agent。权限：Contributor+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是（`binding:"required"`） | 名称 |
| `description` | string | 否 | 描述 |
| `avatar` | string | 否 | 头像/emoji |
| `config` | object | 否 | Agent 配置（`types.CustomAgentConfig`，见下） |

`config` 主要字段：`agent_mode`（`quick-answer`/`smart-reasoning`）、`agent_type`（`rag-qa/wiki-qa/hybrid-rag-wiki/data-analysis/custom`）、`system_prompt`、`model_id`、`temperature`（0-2，非法返回 code 2103）、`max_iterations`（1-20，非法返回 code 2102）、`allowed_tools`（智能推理必填至少一个，code 2101）、`mcp_selection_mode`/`mcp_services`、`skills_selection_mode`、`kb_selection_mode`/`knowledge_bases`、`web_search_enabled`、`question_suggestions` 等（完整定义见 `internal/types/custom_agent.go`）。

响应：201 `{"success":true,"data":{id,name,description,avatar,is_builtin,created_by,config,creator_name,...}}`

```bash
curl -X POST $BASE/api/v1/agents -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"售后助手","config":{"agent_mode":"quick-answer","kb_selection_mode":"selected","knowledge_bases":["kb-1"]}}'
```

### GET /api/v1/agents

用途：Agent 列表（含内置）。权限：Viewer+。查询参数：`creator`（`mine`/`others`，可选）。

响应：200 `{"success":true,"data":[Agent],"disabled_own_agent_ids":[...]}`

```bash
curl $BASE/api/v1/agents -H "X-API-Key: $API_KEY"
```

### GET /api/v1/agents/:id

用途：Agent 详情。权限：Viewer+。

响应：200 `{"success":true,"data":{Agent}}`

```bash
curl $BASE/api/v1/agents/agent-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/agents/:id

用途：更新 Agent。权限：创建者 OR Admin+。请求体：`name/description/avatar/config`（均可选）。Agent 已分享到组织时，新加入知识库范围的知识库须是调用者可分享的（知识库创建者或 Admin+，改为 `all` 需要 Admin+），否则 403。

响应：200 `{"success":true,"data":{Agent}}`

```bash
curl -X PUT $BASE/api/v1/agents/agent-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"description":"更新描述"}'
```

### DELETE /api/v1/agents/:id

用途：删除 Agent。权限：创建者 OR Admin+。

响应：200 `{"success":true,"message":"Agent deleted successfully"}`

```bash
curl -X DELETE $BASE/api/v1/agents/agent-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/agents/:id/copy

用途：复制 Agent（副本归调用者）。权限：Contributor+。无请求体。

响应：201 `{"success":true,"data":{新 Agent}}`

```bash
curl -X POST $BASE/api/v1/agents/agent-1/copy -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/agents/:id/suggested-questions

用途：Agent 起始建议问题（注册在组外以避免与 `/agents/:id/shares` 冲突）。权限：Viewer+；API key `read_agents`/`manage_agents`/`chat`/full。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `knowledge_base_ids` | string | 否 | 逗号分隔 KB |
| `knowledge_ids` | string | 否 | 逗号分隔知识 ID |
| `tag_scopes` | string | 否 | JSON 数组的标签范围 |
| `limit` | int | 否 | 上限 30 |

响应：200 `{"success":true,"data":{"questions":[{question,source,knowledge_base_id}]}}`

```bash
curl "$BASE/api/v1/agents/agent-1/suggested-questions?limit=6" -H "X-API-Key: $API_KEY"
```

## MCP 服务（/api/v1/mcp-services）

空间级外部工具服务集成。读：Viewer+；写/测试/审批策略：Admin+。API key：`manage_mcp_services`/full。Handler: `internal/handler/mcp_service.go`

### POST /api/v1/mcp-services

用途：创建 MCP 服务。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 名称 |
| `description` | string | 否 | 旧版描述，兼容保留；管理界面统一编辑 `usage_instructions` |
| `usage_instructions` | string | 否 | 服务用途、适用场景和关键约束，模型据此判断何时使用该服务；管理界面在第二步要求填写 |
| `enabled` | bool | 否 | 启用 |
| `transport_type` | string | 是 | `sse` / `http-streamable`；`stdio` 出于安全原因被拒绝 |
| `url` | *string | 否 | 服务 URL（SSE/HTTP） |
| `headers` | map[string]string | 否 | HTTP 头 |
| `auth_config` | object | 否 | `auth_type`(`api_key/bearer/oauth`)、`api_key_header`、`custom_headers`、`scopes`、`auth_server_metadata_url`（密钥走 credentials 子资源） |
| `advanced_config` | object | 否 | `{timeout,retry_count,retry_delay}`，默认 30 秒 / 3 次 / 1 秒；`timeout` 大于 60 秒时也会延长 Agent 单次调用该服务工具的等待窗口 |
| `stdio_config` / `env_vars` | object | 否 | 仅为兼容旧数据保留；stdio 已禁用，不生效 |

响应：200 `{"success":true,"data":{MCPServiceResponse}}`（含 `credentials:{api_key:{configured},token:{configured}}`；已同步工具目录的服务还带 `catalog:{tool_count,stale,synced_at}`）

```bash
curl -X POST $BASE/api/v1/mcp-services -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"github","transport_type":"sse","url":"https://mcp.example.com/sse"}'
```

### GET /api/v1/mcp-services

用途：MCP 服务列表。权限：Viewer+。响应：200 `{"success":true,"data":[MCPServiceResponse]}`

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `agent_id` | string | 否 | 与 `agent_source_tenant_id` 同时传入时，列出该共享智能体可 @ 的 MCP 服务 |
| `agent_source_tenant_id` | int | 否 | 共享智能体的来源空间 ID，与对话请求的同名参数一致 |

两个参数都传时走共享智能体路径：只返回该智能体在来源空间以 `selected` 模式指定且已启用的服务（`all`/`none` 模式返回空列表），且每项只含 ID、名称、说明、使用说明、传输类型、启用状态和工具目录摘要，不含 URL、请求头、认证配置等连接细节；调用者无权使用该智能体时返回 403。只传其一或都不传时，列出调用者自己空间的服务。

```bash
curl $BASE/api/v1/mcp-services -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/mcp-services/:id

用途：详情。权限：Viewer+。响应：200 `{"success":true,"data":{MCPServiceResponse}}`

```bash
curl $BASE/api/v1/mcp-services/mcp-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/mcp-services/:id

用途：部分更新（map 语义；`auth_config` 中不可携带 api_key/token）。权限：Admin+。字段同创建（均可选）。

提交 `usage_instructions` 时必须为去除首尾空白后非空的字符串，最长 16000 字符。仅修改连接或启用状态时可省略该字段，原值保持不变。

响应：200 `{"success":true,"data":{MCPServiceResponse}}`

```bash
curl -X PUT $BASE/api/v1/mcp-services/mcp-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"enabled":false}'
```

### POST /api/v1/mcp-services/:id/usage-instructions/generate

用途：根据已同步、未过期的 MCP 工具目录生成精简使用说明。权限：Admin+；API key 需要 `manage_mcp_services` 或 full。

请求：`{"language":"zh-CN"}`。支持 `zh-CN`、`en-US`、`ja-JP`、`ko-KR`、`ru-RU`，默认中文。

优先使用空间默认的可用对话模型，否则使用首个可用对话模型。输入包括服务名称、服务端说明和已启用工具的名称、描述；OAuth 目录沿用当前用户的授权范围。不会连接 MCP、调用工具或自动保存生成结果。

响应：200 `{"success":true,"data":{"usage_instructions":"按模块和时间范围查询远程日志；已有查询 ID 时读取对应日志。"}}`。生成目标为 2–3 句简短说明，最多 500 字符；用户可编辑后通过 PUT 保存。目录未同步、过期、无启用工具或无可用对话模型时返回 400。

### DELETE /api/v1/mcp-services/:id

用途：删除。权限：Admin+。响应：200 `{"success":true,"message":"MCP service deleted successfully"}`

```bash
curl -X DELETE $BASE/api/v1/mcp-services/mcp-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/mcp-services/:id/test

用途：连接测试（探测外部服务）。权限：Admin+。响应：200 `{"success":true,"data":{"success","message","oauth_required","tools":[...],"resources":[...]}}`

```bash
curl -X POST $BASE/api/v1/mcp-services/mcp-1/test -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/mcp-services/:id/metadata

用途：读取持久工具目录，不连接上游。权限：Viewer+；OAuth 目录按当前有效授权主体隔离。

响应：200 `{"success":true,"data":null}` 表示未同步；已同步时 `data` 为目录快照，包含服务端信息、instructions、tools 和同步时间。连接配置变更后的快照标记 `stale:true`，不能用于加载运行时工具。

```bash
curl $BASE/api/v1/mcp-services/mcp-1/metadata -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/mcp-services/:id/metadata/refresh

用途：显式连接上游、完整拉取并原子更新工具目录。静态认证目录需 Admin+；OAuth 用户可同步自己的目录（Viewer+）。API Key 需要 MCP 管理能力。

响应为更新后的目录快照。失败保留原快照；连接在刷新期间变化返回 409，目录无效/过大或上游同步失败返回 400，元数据存储不可用返回 503。不会覆盖人工使用说明和单工具启用/审批策略。

```bash
curl -X POST $BASE/api/v1/mcp-services/mcp-1/metadata/refresh -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/mcp-services/:id/tools

用途：工具列表。权限：Viewer+。响应：200 `{"success":true,"data":[{name,description,inputSchema,require_approval}]}`

```bash
curl $BASE/api/v1/mcp-services/mcp-1/tools -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/mcp-services/:id/resources

用途：资源列表。权限：Viewer+。响应：200 `{"success":true,"data":[{uri,name,description,mimeType}]}`

```bash
curl $BASE/api/v1/mcp-services/mcp-1/resources -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/mcp-services/:id/credentials

用途：设置密钥（`api_key`/`token`，指针字段，省略保留）。权限：Admin+。Handler: `internal/handler/mcp_credentials.go`

响应：200 `{"success":true,"data":{"fields":{"api_key":{"configured"},"token":{"configured"}}}}`

```bash
curl -X PUT $BASE/api/v1/mcp-services/mcp-1/credentials -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"token":"ghp_..."}'
```

### DELETE /api/v1/mcp-services/:id/credentials/:field

用途：删除凭证字段（`api_key` 或 `token`）。权限：Admin+。响应：204。

```bash
curl -X DELETE $BASE/api/v1/mcp-services/mcp-1/credentials/token -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/mcp-services/:id/tool-approvals

用途：工具启停与人工审批策略列表。权限：Viewer+。响应：200 `{"success":true,"data":[{service_id,tool_name,require_approval,enabled,...}]}`

```bash
curl $BASE/api/v1/mcp-services/mcp-1/tool-approvals -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/mcp-services/:id/tool-approvals/:tool_name

用途：更新某工具的 enabled（启停）和 require_approval（人工审批）。权限：Admin+。两者至少提供一个，省略的字段保持原值；无记录默认启用且不要求审批。

响应：200 `{"success":true}`

```bash
curl -X PUT $BASE/api/v1/mcp-services/mcp-1/tool-approvals/create_issue \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"require_approval":true}'
```

## MCP OAuth

Handler: `internal/handler/mcp_oauth.go`

### GET /api/v1/mcp-oauth/callback

用途：第三方 OAuth 授权回调（免认证，靠单次 `state` 参数认证；注册在 `/mcp-services` 组之外）。查询参数：`code`、`state`、`error`。

响应：302 重定向到前端（成功 `#mcp_oauth_result=success`，失败 `#mcp_oauth_error=<code>`）。

```bash
curl -i "$BASE/api/v1/mcp-oauth/callback?code=xxx&state=yyy"
```

### POST /api/v1/mcp-services/:id/oauth/authorize-url

用途：生成用户级授权 URL。权限：Viewer+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `redirect_uri` | string | 是 | 后端回调 URL（绝对地址） |
| `frontend_redirect` | string | 否 | 回调后前端跳转（默认 `/`） |

响应：200 `{"success":true,"data":{"authorization_url","authorization_attempt"}}`

```bash
curl -X POST $BASE/api/v1/mcp-services/mcp-1/oauth/authorize-url -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"redirect_uri":"'$BASE'/api/v1/mcp-oauth/callback"}'
```

### GET /api/v1/mcp-services/:id/oauth/status

用途：查询本人授权状态。权限：Viewer+。查询参数：`authorization_attempt`（可选）。

响应：200 `{"success":true,"data":{"authorized","state":"authorized|pending","refresh_available","expires_at"}}`

```bash
curl $BASE/api/v1/mcp-services/mcp-1/oauth/status -H "Authorization: Bearer $TOKEN"
```

### DELETE /api/v1/mcp-services/:id/oauth/token

用途：吊销本人 OAuth token。权限：Viewer+。响应：204。

```bash
curl -X DELETE $BASE/api/v1/mcp-services/mcp-1/oauth/token -H "Authorization: Bearer $TOKEN"
```

## MCP Server 端点（/api/v1/mcp-endpoints） {#mcp-server-端点}

管理当前空间对外发布的 MCP 端点，外部 MCP 客户端连接 `/mcp/:endpoint_id`。读：Viewer+；写：Admin+。API key：`manage_channels`/full。Handler: `internal/handler/mcp_endpoint.go`。用途与工具说明见[MCP 集成](../03-features/08-mcp.md#供外部客户端调用)。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/mcp-endpoints` | 端点列表（不含令牌） |
| GET | `/mcp-endpoints/tools` | 工具目录：`{groups,tools:[{name,group,destructive}],default_tools}` |
| POST | `/mcp-endpoints` | 创建；201，响应含一次性 `token` |
| GET | `/mcp-endpoints/:endpoint_id` | 详情 |
| PUT | `/mcp-endpoints/:endpoint_id` | 部分更新，省略的字段保持原值 |
| DELETE | `/mcp-endpoints/:endpoint_id` | 删除，使用该端点的客户端立即失效 |
| POST | `/mcp-endpoints/:endpoint_id/rotate-token` | 轮换令牌，响应含新 `token`，旧令牌立即失效 |

请求字段（创建和更新相同，均可选）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `name` | string | 名称，创建时必填 |
| `description` | string | 说明 |
| `enabled` | bool | 默认 true；停用后连接返回 403 |
| `knowledge_base_ids` | string[] | 可访问的知识库，空数组表示空间内全部 |
| `tools` | string[] | 暴露的工具，至少一个；创建时省略则使用 `default_tools`（全部只读工具） |
| `default_agent_id` | string | `ask` 使用的 Agent，空为内置快速问答；内部内置 Agent 不可选 |
| `rate_limit_per_minute` | int | 每分钟工具调用上限，0 或省略为 60，最大 6000 |

响应 `data` 为 `{id,tenant_id,name,description,enabled,token_hint,knowledge_base_ids,tools,default_agent_id,rate_limit_per_minute,path,last_used_at,created_at,updated_at}`，创建和轮换时额外带 `token`。使用受限 API Key 调用时，端点的知识库和工具所需能力不能超出该 Key 的范围，否则 403。

```bash
curl -X POST $BASE/api/v1/mcp-endpoints -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"产品文档助手","knowledge_base_ids":["kb-1"],"tools":["search_knowledge","read_document","ask"]}'
```

## Agent 运行时交互（/api/v1/agent）

对话中的人工审批与 OAuth 恢复；权限均 Viewer+（发起会话的人才有上下文），API key 默认拒绝。

### POST /api/v1/agent/tool-approvals/:pending_id

用途：裁决待审批的工具调用。Handler: `internal/handler/mcp_service.go` 的 `ResolveToolApproval`。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `decision` | string | 是（`binding:"required"`） | `approve` / `reject` |
| `modified_args` | JSON | 否 | 修改后的工具参数 |
| `reason` | string | 否 | 理由 |

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/agent/tool-approvals/p-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"decision":"approve"}'
```

### POST /api/v1/agent/mcp-oauth-resolutions/:pending_id

用途：恢复因 MCP OAuth 暂停的 Agent 运行。Handler: `internal/handler/mcp_oauth.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `service_id` | string | 是（`binding:"required"`） | MCP 服务 ID |
| `decision` | string | 否 | `authorize`（默认）/ `cancel` |

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/agent/mcp-oauth-resolutions/p-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"service_id":"mcp-1"}'
```

### POST /api/v1/agent/mcp-oauth-resolutions/:pending_id/cancel

用途：取消暂停中的 OAuth 流程。无请求体。

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/agent/mcp-oauth-resolutions/p-1/cancel -H "Authorization: Bearer $TOKEN"
```

## 技能、沙箱与个人变量

`GET /api/v1/skills?sandbox_config_id=...` 返回指定配置下可用技能的名称/说明及 skills_available；传 `agent_id` + `agent_source_tenant_id` 时按共享智能体的来源空间和其沙箱配置返回，详见[沙箱与技能 API](02-api-sandbox-skills.md#沙箱内技能)。目录收录、安装、模板、进度、文件与个人变量的完整接口见[沙箱与技能 API](02-api-sandbox-skills.md)。

智能体 config 增加 `sandbox_config_id`；与 skills_selection_mode、selected_skills 一起决定可用技能。shell/文件工具按后端能力注册，旧 read_skill / execute_skill_script 不再注册。

## 长期记忆

智能体 config 的 `memory_enabled` 为 nil 时继承空间，false 禁用本智能体的记忆读写。个人管理、主题/文档偏好、导出与立即整理见[长期记忆 API](02-api-memory.md)，使用步骤见[跨会话长期记忆](../03-features/23-memory.md)。

## 用户收藏（/api/v1/user/favorites）

按用户维度存储（非资源创建者维度）；权限均 Viewer+，仅 JWT（API key 默认拒绝）。Handler: `internal/handler/user_resource_favorite.go`

### GET /api/v1/user/favorites

用途：收藏列表。查询参数：`type`（必填，`kb` 或 `agent`）。

响应：200 `{"success":true,"data":[{type,id,created_at}]}`

```bash
curl "$BASE/api/v1/user/favorites?type=kb" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/user/favorites

用途：添加收藏。请求体：`{"type":"kb|agent","id":"<资源ID>"}`（均必填）。

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/user/favorites -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"type":"kb","id":"kb-1"}'
```

### DELETE /api/v1/user/favorites/:type/:id

用途：取消收藏。路径参数：`type`、`id`。

响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/user/favorites/kb/kb-1 -H "Authorization: Bearer $TOKEN"
```

## 实现参考

路由注册：由 `internal/router/router.go` 调用，`RegisterCustomAgentRoutes`、`RegisterSkillRoutes`、`RegisterUserFavoriteRoutes` 定义在 `routes_agent.go`，`RegisterMCPServiceRoutes`（含 MCP OAuth 与 `/agent` 运行时交互）在 `routes_infra.go`，`RegisterMCPEndpointRoutes` 与公开的 `/mcp/:endpoint_id` 在 `routes_mcp_endpoint.go`。Handler：`internal/handler/custom_agent.go`、`internal/handler/mcp_service.go`、`internal/handler/mcp_credentials.go`、`internal/handler/mcp_oauth.go`、`internal/handler/mcp_endpoint.go`、`internal/handler/skill_handler.go`、`internal/handler/user_resource_favorite.go`。
