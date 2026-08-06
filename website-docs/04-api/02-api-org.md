# API 参考：组织与共享

路由注册：`internal/router/router.go` 的 `RegisterOrganizationRoutes`。Handler：`internal/handler/organization.go`。

组织（Organization）以“空间（tenant）”为成员单位。组织组路由的 API key 策略为 `manage_spaces` 或 full-access；KB/Agent 分享管理仅 full-access key 可用。

## 组织管理（/api/v1/organizations）

### POST /api/v1/organizations

用途：创建组织。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 组织名称 |
| `description` | string | 否 | 描述 |
| `avatar` | string | 否 | 头像 URL |
| `searchable` | bool | 否 | 是否可被搜索发现 |
| `require_approval` | bool | 否 | 加入是否需审批 |
| `member_limit` | int | 否 | 成员空间数上限 |
| `invite_code_validity_days` | int | 否 | 邀请码有效期（天） |

响应：201 `{"success":true,"data":{OrganizationResponse}}`

```bash
curl -X POST $BASE/api/v1/organizations -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"研发组织"}'
```

### GET /api/v1/organizations

用途：列出我所在的组织。权限：Viewer+。

响应：200 `{"success":true,"data":{"organizations":[...],"total":N,"resource_counts":{"knowledge_bases":{"by_organization":{}},"agents":{"by_organization":{}}}}}`

```bash
curl $BASE/api/v1/organizations -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/organizations/preview/:code

用途：按邀请码预览组织（不加入）。权限：Viewer+。路径参数：`code` 邀请码。

响应：200 `{"success":true,"data":{id,name,description,avatar,member_count,share_count,agent_share_count,is_already_member,require_approval,created_at}}`

```bash
curl $BASE/api/v1/organizations/preview/ABC123 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/organizations/join

用途：凭邀请码加入组织。权限：Admin+。请求体：`{"invite_code":"..."}`（必填）。

响应：200 `{"success":true,"data":{OrganizationResponse}}`

```bash
curl -X POST $BASE/api/v1/organizations/join -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"invite_code":"ABC123"}'
```

### POST /api/v1/organizations/join-request

用途：提交加入申请（需审批的组织）。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `invite_code` | string | 是 | 邀请码 |
| `message` | string | 否 | 申请附言 |
| `role` | string | 否 | 期望角色 |

响应：200 `{"success":true,"data":{JoinRequest}}`

```bash
curl -X POST $BASE/api/v1/organizations/join-request -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"invite_code":"ABC123","message":"申请加入"}'
```

### GET /api/v1/organizations/search

用途：搜索可发现（searchable）的组织。权限：Viewer+。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `q` | string | 否 | 关键字 |
| `limit` | int | 否 | 默认 20，上限 100 |

响应：200 `{"success":true,"data":[SearchableOrganization],"total":N}`

```bash
curl "$BASE/api/v1/organizations/search?q=研发" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/organizations/join-by-id

用途：按组织 ID 加入可发现组织（无需邀请码）。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `organization_id` | string | 是 | 目标组织 ID |
| `message` | string | 否 | 附言 |
| `role` | string | 否 | 期望角色 |

响应：200 `{"success":true,"data":{OrganizationResponse}}`

```bash
curl -X POST $BASE/api/v1/organizations/join-by-id -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"organization_id":"org-1"}'
```

### GET /api/v1/organizations/:id

用途：组织详情。权限：Viewer+。

响应：200 `{"success":true,"data":{OrganizationResponse}}`

```bash
curl $BASE/api/v1/organizations/org-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/organizations/:id

用途：更新组织（服务层校验调用者空间为组织 owner）。权限：Admin+。请求体字段同创建（均可选）。

响应：200 `{"success":true,"data":{OrganizationResponse}}`

```bash
curl -X PUT $BASE/api/v1/organizations/org-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"description":"更新描述"}'
```

### DELETE /api/v1/organizations/:id

用途：删除组织。权限：Admin+（服务层要求组织 owner）。

响应：200 `{"success":true,"message":"Organization deleted successfully"}`

```bash
curl -X DELETE $BASE/api/v1/organizations/org-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/organizations/:id/leave

用途：本空间退出组织。权限：Admin+。无请求体。

响应：200 `{"success":true,"message":"Left organization successfully"}`

```bash
curl -X POST $BASE/api/v1/organizations/org-1/leave -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/organizations/:id/request-upgrade

用途：申请提升本空间在组织内的角色。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `requested_role` | string | 是 | 期望的组织角色（`viewer/editor/admin`） |
| `message` | string | 否 | 附言 |

响应：200 `{"success":true,"data":{JoinRequest}}`

```bash
curl -X POST $BASE/api/v1/organizations/org-1/request-upgrade -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"requested_role":"editor"}'
```

### POST /api/v1/organizations/:id/invite-code

用途：生成组织邀请码。权限：Admin+（服务层要求组织 admin）。无请求体。

响应：200 `{"success":true,"data":{"invite_code":"..."}}`

```bash
curl -X POST $BASE/api/v1/organizations/org-1/invite-code -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/organizations/:id/search-tenants

用途：搜索可邀请的空间（返回按空间分组的候选）。权限：Admin+。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `q` | string | 是 | 空间名关键字 |
| `limit` | int | 否 | 默认 10，上限 50 |

响应：200 `{"success":true,"data":[{"tenant_id","tenant_name"}]}`

```bash
curl "$BASE/api/v1/organizations/org-1/search-tenants?q=demo" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/organizations/:id/search-users

用途：已废弃别名，行为同 `search-tenants`（返回空间分组结果）。权限：Admin+。参数同上。

```bash
curl "$BASE/api/v1/organizations/org-1/search-users?q=demo" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/organizations/:id/invite

用途：直接邀请空间加入组织。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `tenant_id` | uint64 | 二选一 | 目标空间 ID（推荐） |
| `user_id` | string | 二选一 | 兼容路径：用户 ID（解析为其空间） |
| `representative_user_id` | string | 否 | 该空间的代表用户 |
| `role` | string | 是 | 组织内角色 |

响应：200 `{"success":true,"message":"Member added successfully"}`

```bash
curl -X POST $BASE/api/v1/organizations/org-1/invite -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"tenant_id":2,"role":"viewer"}'
```

### GET /api/v1/organizations/:id/members

用途：组织成员（空间）列表。权限：Viewer+。

响应：200 `{"success":true,"data":{"members":[{id,user_id,representative_user_id,role,tenant_id,tenant_name,username,email,avatar,joined_at}],"total":N}}`

```bash
curl $BASE/api/v1/organizations/org-1/members -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/organizations/:id/members/:tenant_id

用途：修改成员空间的组织角色。权限：Admin+。路径参数 `tenant_id` 为成员空间 ID。请求体：`{"role":"editor"}`（必填，`viewer/editor/admin`）。

响应：200 `{"success":true,"message":"Member role updated successfully"}`

```bash
curl -X PUT $BASE/api/v1/organizations/org-1/members/2 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"role":"editor"}'
```

### DELETE /api/v1/organizations/:id/members/:tenant_id

用途：移除成员空间（含自移除）。权限：Admin+。

响应：200 `{"success":true,"message":"Member removed successfully"}`

```bash
curl -X DELETE $BASE/api/v1/organizations/org-1/members/2 -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/organizations/:id/join-requests

用途：加入申请队列。权限：Admin+。

响应：200 `{"success":true,"data":{"requests":[{id,user_id,username,email,message,request_type,prev_role,requested_role,status,created_at,reviewed_at}],"total":N}}`

```bash
curl $BASE/api/v1/organizations/org-1/join-requests -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/organizations/:id/join-requests/:request_id/review

用途：审批加入/升级申请。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `approved` | bool | 是 | 通过/拒绝 |
| `message` | string | 否 | 审批意见 |
| `role` | string | 否 | 通过时授予的角色 |

响应：200 `{"success":true,"message":"Review completed"}`

```bash
curl -X PUT $BASE/api/v1/organizations/org-1/join-requests/req-1/review \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"approved":true}'
```

### GET /api/v1/organizations/:id/shares

用途：查看共享到该组织的 KB 列表。权限：Viewer+。

响应：200 `{"success":true,"data":{"shares":[KnowledgeBaseShareResponse],"total":N}}`

```bash
curl $BASE/api/v1/organizations/org-1/shares -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/organizations/:id/agent-shares

用途：查看共享到该组织的 Agent 列表。权限：Viewer+。

响应：200 `{"success":true,"data":{"shares":[AgentShareResponse],"total":N}}`

```bash
curl $BASE/api/v1/organizations/org-1/agent-shares -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/organizations/:id/shared-knowledge-bases

用途：组织空间视图：组织内全部共享 KB（含我自己的）。权限：Viewer+。

响应：200 `{"success":true,"data":[...含 is_mine、source_from_agent 标记...],"total":N}`

```bash
curl $BASE/api/v1/organizations/org-1/shared-knowledge-bases -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/organizations/:id/shared-agents

用途：组织空间视图：组织内全部共享 Agent。权限：Viewer+。

响应：200 `{"success":true,"data":[SharedAgentInfo],"total":N}`

```bash
curl $BASE/api/v1/organizations/org-1/shared-agents -H "Authorization: Bearer $TOKEN"
```

## KB 分享（/api/v1/knowledge-bases/:id/shares）

API key：仅 full-access。Handler: `internal/handler/organization.go`

### POST /api/v1/knowledge-bases/:id/shares

用途：把 KB 分享到组织。权限：KB 创建者 OR Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `organization_id` | string | 是 | 目标组织 |
| `permission` | string | 是 | 共享权限（组织角色语义，如 `viewer/editor`） |

响应：201 `{"success":true,"data":{KBShare}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/shares -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"organization_id":"org-1","permission":"viewer"}'
```

### GET /api/v1/knowledge-bases/:id/shares

用途：查看该 KB 的分享列表。权限：Viewer+。

响应：200 `{"success":true,"data":{"shares":[KnowledgeBaseShareResponse],"total":N}}`

```bash
curl $BASE/api/v1/knowledge-bases/kb-1/shares -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledge-bases/:id/shares/:share_id

用途：修改分享权限。权限：KB 创建者 OR Admin+。请求体：`{"permission":"editor"}`（必填）。

响应：200 `{"success":true,"message":"Share permission updated successfully"}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/shares/s-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"permission":"editor"}'
```

### DELETE /api/v1/knowledge-bases/:id/shares/:share_id

用途：取消分享。权限：KB 创建者 OR Admin+。

响应：200 `{"success":true,"message":"Share removed successfully"}`

```bash
curl -X DELETE $BASE/api/v1/knowledge-bases/kb-1/shares/s-1 -H "Authorization: Bearer $TOKEN"
```

## Agent 分享（/api/v1/agents/:id/shares）

API key：仅 full-access。Handler: `internal/handler/organization.go`

### POST /api/v1/agents/:id/shares

用途：把 Agent 分享到组织。权限：Agent 创建者 OR Admin+。请求体同 KB 分享（`organization_id` + `permission`，必填）。

响应：201 `{"success":true,"data":{AgentShare}}`

```bash
curl -X POST $BASE/api/v1/agents/agent-1/shares -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"organization_id":"org-1","permission":"viewer"}'
```

### GET /api/v1/agents/:id/shares

用途：查看该 Agent 的分享列表。权限：Agent 创建者 OR Admin+。

响应：200 `{"success":true,"data":{"shares":[AgentShareResponse],"total":N}}`

```bash
curl $BASE/api/v1/agents/agent-1/shares -H "Authorization: Bearer $TOKEN"
```

### DELETE /api/v1/agents/:id/shares/:share_id

用途：取消 Agent 分享。权限：Agent 创建者 OR Admin+。

响应：200 `{"success":true,"message":"Share removed successfully"}`

```bash
curl -X DELETE $BASE/api/v1/agents/agent-1/shares/s-1 -H "Authorization: Bearer $TOKEN"
```

## 共享资源聚合视图

### GET /api/v1/shared-knowledge-bases

用途：列出通过组织共享给我的 KB（去除属主侧向量库元数据）。权限：Viewer+；API key 需 `manage_spaces` 或 full-access。

响应：200 `{"success":true,"data":[...],"total":N}`

```bash
curl $BASE/api/v1/shared-knowledge-bases -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/shared-agents

用途：列出通过组织共享给我的 Agent。权限：Viewer+；API key 同上。

响应：200 `{"success":true,"data":[SharedAgentInfo],"total":N}`。`SharedAgentInfo` 含 `source_tenant_id`（来源空间）、`org_name`、`shared_by_username`、`permission`，以及 `web_search_ready`——只返回「来源空间的联网搜索是否可用」这一个布尔位，不下发来源空间的 provider 配置（会泄露配置），也不拿接收方空间的 provider ID 去比对（会误报不可用）。

使用共享 Agent 调用其它接口时，若同名 Agent 被多个空间共享，可带 `agent_source_tenant_id` 指明来源空间；该值会与共享关系逐一校验，非法或无权限时直接报错，不会静默回退到别的来源。

```bash
curl $BASE/api/v1/shared-agents -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/shared-agents/disabled

用途：设置“本空间禁用某共享 Agent”（影响整个空间的会话下拉）。权限：Admin+；API key 同上。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `agent_id` | string | 是（`binding:"required"`） | 共享 Agent ID |
| `disabled` | bool | 否 | 是否禁用（默认 false） |

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/shared-agents/disabled -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"agent_id":"agent-1","disabled":true}'
```
