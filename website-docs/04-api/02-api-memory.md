# API 参考：长期记忆

管理当前调用者的长期记忆、主题和文档偏好，以及空间级记忆配置。路径使用 `/api/v1` 前缀。

个人接口均要求 Viewer+，API Key 必须 full-access。作用域从凭证中确定，不接受任意 `subject_id`。示例中的 `$BASE` 为服务地址，`$TOKEN` 为当前用户的 Bearer token。

## 空间配置与请求开关

空间配置使用 `GET/PUT /tenants/kv/memory-config`，不使用租户名称/描述的更新接口。读取需 Viewer+，写入需 Admin+，API Key 需 manage_tenant_settings 或 full-access。响应为 `{success,data:MemoryConfig}`，PUT 直接传配置对象；个人 `PUT /memory/settings` 不能替代空间开关。

```bash
curl -X PUT "$BASE/api/v1/tenants/kv/memory-config" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"write_mode":"explicit_only","max_items":200}'
```

`memory_config` 字段：`enabled`、`write_mode`（explicit_only/auto）、`extract_model_id`、`max_items`、`extract_delay_seconds`、`extract_min_interval_seconds`、`extract_instructions`、`interest_threshold`、`embedding_model_id`、`vector_recall`、`retrieval_conditioning`。语义见[长期记忆](../03-features/23-memory.md)。更新时提交需要保留的完整配置对象。

`CustomAgentConfig.memory_enabled` 省略继承空间，false 禁止本智能体使用记忆；IM/Embed 使用绑定智能体的配置，当前渠道结构没有独立的 memory_enabled 字段。

## 个人设置

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/memory/settings` | `{success,data:{workspace_enabled,user_enabled,effective,write_mode,item_count,max_items}}` |
| PUT | `/memory/settings` | `{"enabled":true}`，enabled 必填；返回更新后的 settings |

`effective` 表示空间与个人开关的合并结果；一次聊天还受智能体开关约束。

```bash
curl -X PUT "$BASE/api/v1/memory/settings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true}'
```

## 条目

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/memory/items` | 可选 status、limit、offset；`{success,data:[MemoryItem],total}` |
| POST | `/memory/items` | `{kind,content,importance}`；200 `{success,data:MemoryItem}` |
| PUT | `/memory/items/:id` | `{content,importance}`；200 `{success,data:MemoryItem}` |
| DELETE | `/memory/items/:id` | 200 `{"success":true}` |
| POST | `/memory/items/:id/confirm` | 200 `{success,data:MemoryItem}`；推断已失效或其依据已被修改时返回 409 |
| POST | `/memory/items/:id/reject` | 200 `{"success":true}` |
| DELETE | `/memory/items` | 清空当前身份；200 `{success,removed}` |

`status` 可为 active、pending、superseded、archived，省略不过滤。`limit` 默认 50，合法范围 1–200，越界回落 50；`offset` 默认 0，负值归零。kind 为 profile/preference/fact/task/interest；内容为简短记忆，最长 300 个字符，importance 用于重要度排序。

MemoryItem 包括 `id`、`kind`、`content`、`topic`、`importance`、`origin`、`status`、`source_session_id`、`source_message_id`、`expires_at`、`superseded_by` 和创建/修改时间。pending 不参与提示词；编辑后按手工维护处理。

用于修改已有记忆的 pending 推断，确认前旧条目继续生效；确认会在同一事务中激活推断并替换旧条目。已失效、已到期或所依据内容已被修改/删除的推断不能确认，返回 409，客户端应刷新列表。内容几乎全是凭据等敏感信息时，新增或编辑返回 400。

```bash
curl -X POST "$BASE/api/v1/memory/items" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"kind":"preference","content":"回答先给结论，再解释依据","importance":3}'

curl "$BASE/api/v1/memory/items?status=pending&limit=50&offset=0" \
  -H "Authorization: Bearer $TOKEN"

curl -X POST "$BASE/api/v1/memory/items/item-1/confirm" \
  -H "Authorization: Bearer $TOKEN"
```

## 主题和文档偏好

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/memory/topics` | 正在跟踪、尚未提升的主题；limit/offset 同条目，返回 data 与 total |
| POST | `/memory/topics/:id/promote` | 手动提升为长期关注；返回 `{success,data:MemoryItem}` |
| DELETE | `/memory/topics/:id` | 停止跟踪该主题；返回 success |
| GET | `/memory/documents` | 文档偏好；limit/offset 同条目，返回 data 与 total |
| DELETE | `/memory/documents/:id` | 删除该偏好记录；返回 success，不删除知识库文档 |

Topic 字段包括 id/topic/aliases/hits/threshold/last_seen_at；Document 字段包括 id/knowledge_id/knowledge_base_id/title/hits/last_used_at。删除使用的是偏好记录 id。

```bash
curl "$BASE/api/v1/memory/topics" -H "Authorization: Bearer $TOKEN"
curl -X POST "$BASE/api/v1/memory/topics/topic-1/promote" \
  -H "Authorization: Bearer $TOKEN"
curl -X DELETE "$BASE/api/v1/memory/documents/affinity-1" \
  -H "Authorization: Bearer $TOKEN"
```

## 导出与立即整理

`GET /memory/export` 返回 `{success,total,truncated,data}`，带下载文件名 `weknora-memories.json`。最多导出 20,000 条，触及上限时检查 truncated。

`POST /memory/consolidate` 返回 `{success,data:{merged,demoted,expired,reviewed,candidates,skipped?}}`，立即合并近义条目、归档到期事项。没有变化时 skipped 说明原因。

```bash
curl "$BASE/api/v1/memory/export" -H "Authorization: Bearer $TOKEN" \
  -o weknora-memories.json
curl -X POST "$BASE/api/v1/memory/consolidate" -H "Authorization: Bearer $TOKEN"
```

参数无效、内容为敏感信息或记忆未开启时返回 400；找不到当前身份的条目返回 404；确认冲突返回 409；认证/权限不满足返回 401/403。个人接口不接受 subject 参数。实现：`internal/handler/memory.go`、`internal/router/routes_memory.go`。

## 管理无账号身份的记忆

以下接口使用当前工作空间 Admin/Owner 的 Bearer 登录凭证，API Key 不可访问；即使租户 RBAC 的灰度开关关闭，管理员角色校验也保持生效。管理员只能管理当前调用者所在工作空间的身份，不能通过请求参数切换租户。空间或身份关闭记忆时，管理员仍可查看、清空和调整开关。

路径中的 `subject_id` 是列表返回的记忆空间 UUID（`id`），不是 `web_user:...`、`im_user:...` 等 `subject_id` 存储标识。条目 ID 也必须属于该 UUID 对应的身份；不存在与其他工作空间的 UUID/条目均返回 404。

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/admin/memory/subjects` | 可选 limit、offset；`{success,data:[MemorySubjectSummary],total}` |
| GET | `/admin/memory/subjects/:subject_id/items` | 可选 status、limit、offset；`{success,data:[MemoryItem],total}`，支持查看 pending |
| POST | `/admin/memory/subjects/:subject_id/items/:id/confirm` | 200 `{success,data:MemoryItem}`；失效或到期的推断返回 409 |
| POST | `/admin/memory/subjects/:subject_id/items/:id/reject` | 200 `{"success":true}`，拒绝 pending 推断 |
| DELETE | `/admin/memory/subjects/:subject_id/items` | 200 `{success,removed}`，清空该身份的记忆及派生资料 |
| PUT | `/admin/memory/subjects/:subject_id/settings` | `{"enabled":false}`，enabled 必填；200 `{"success":true}` |

分页和 status 取值沿用个人条目接口。`MemorySubjectSummary` 包含 `id`、`subject_id`、`principal_type`、`enabled`、`item_count`、`active_count`、`pending_count`、`last_activity_at`。计数从当前条目计算；列表不返回记忆正文、常驻画像块、后台任务队列或提取状态。

```bash
curl "$BASE/api/v1/admin/memory/subjects?limit=50" \
  -H "Authorization: Bearer $TOKEN"

# 将 SUBJECT_UUID 替换为上一步返回的 id。
curl "$BASE/api/v1/admin/memory/subjects/$SUBJECT_UUID/items?status=pending" \
  -H "Authorization: Bearer $TOKEN"

curl -X PUT "$BASE/api/v1/admin/memory/subjects/$SUBJECT_UUID/settings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":false}'
```

管理员查看指定身份的条目以及成功的确认、拒绝、清空、开关变更会写入现有审计日志，事件名以 `memory.` 开头；日志记录操作者、目标 UUID 和操作元数据，不复制记忆内容。审计存储失败会记录服务日志，不使已完成的操作失败。

`max_items` 分别限制每个身份的 active 和 pending 条数（默认各 200，配置最大 2000）。写入新的推断时，在同一身份事务内将超限的最旧 pending 归档，不因 pending 超限淘汰 active 事实。降低配置后，存量 pending 在下一次写入时收敛到上限；没有新增定时清理器。

清空在同一主体事务中删除记忆及派生资料、切换写入代次，并将提取历史水位推进到清空时刻。已经开始的记忆提取、整理和画像重建不能以旧代次写回；同一会话后续的新消息也不会重新提取清空前的历史，或将这些历史作为提取模型的前文。已有较新的游标保持单调前进。

清空保留身份及其 enabled 设置，不删除聊天消息，也不撤回已经发送给模型或客户端的内容。清空后开始的新记忆操作仍可正常写入；需要持续停止自动提取时，先将该身份 enabled 设为 false，再执行清空。渠道或智能体级提取策略继承不在这些接口的范围内。
