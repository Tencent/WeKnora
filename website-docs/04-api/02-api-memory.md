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
| POST | `/memory/items/:id/confirm` | 200 `{success,data:MemoryItem}` |
| POST | `/memory/items/:id/reject` | 200 `{"success":true}` |
| DELETE | `/memory/items` | 清空当前身份；200 `{success,removed}` |

`status` 可为 active、pending、superseded、archived，省略不过滤。`limit` 默认 50，合法范围 1–200，越界回落 50；`offset` 默认 0，负值归零。kind 为 profile/preference/fact/task/interest；内容为简短记忆，最长 300 个字符，importance 用于重要度排序。

MemoryItem 包括 `id`、`kind`、`content`、`topic`、`importance`、`origin`、`status`、`source_session_id`、`source_message_id`、`expires_at`、`superseded_by` 和创建/修改时间。pending 不参与提示词；编辑后按手工维护处理。

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

参数无效返回 400；找不到当前身份的条目返回 404；认证/权限不满足返回 401/403。接口没有管理员读取他人记忆的 subject 参数。实现：`internal/handler/memory.go`、`internal/router/routes_memory.go`。
