# API 参考：长期记忆

管理当前调用者的常驻画像、会话记忆和原话笔记，以及空间级记忆配置。路径使用 `/api/v1` 前缀。

个人接口均要求 Viewer+，API Key 必须 full-access。作用域从凭证中确定，不接受任意 `subject_id`。示例中的 `$BASE` 为服务地址，`$TOKEN` 为当前用户的 Bearer token。

记忆分三层存放，三组接口各管一层：**画像**是每轮对话都会注入的一份文档；**会话记忆**是每段对话结束后写下的一份完整叙述，画像由它们改写而来；**原话笔记**是用户明确要求记住的句子，按原文保存，模型不会改写。语义见[长期记忆](../03-features/23-memory.md)。

## 空间配置与请求开关

空间配置使用 `GET/PUT /tenants/kv/memory-config`，不使用租户名称/描述的更新接口。读取需 Viewer+，写入需 Admin+，API Key 需 manage_tenant_settings 或 full-access。响应为 `{success,data:MemoryConfig}`，PUT 直接传配置对象；个人 `PUT /memory/settings` 不能替代空间开关。

```bash
curl -X PUT "$BASE/api/v1/tenants/kv/memory-config" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"write_mode":"explicit_only","max_episodes":200}'
```

`memory_config` 字段：`enabled`、`write_mode`（explicit_only/auto）、`extract_model_id`、`consolidate_model_id`、`max_episodes`、`extract_delay_seconds`、`extract_min_interval_seconds`、`extract_instructions`、`interest_threshold`、`embedding_model_id`、`vector_recall`、`retrieval_conditioning`。更新时提交需要保留的完整配置对象。

`max_episodes` 是每人保留多少段会话记忆，0 取默认值 200，上限 500。超出后按「最少被读、最久未读、最早写入」裁剪，但画像还没读过的会话记忆不会被裁掉——那是这段对话的唯一副本。`interest_threshold` 是同一关键词要出现在多少段独立会话里，才算这个人反复关心的方向。

`CustomAgentConfig.memory_enabled` 省略继承空间，false 禁止本智能体使用记忆；IM/Embed 使用绑定智能体的配置，当前渠道结构没有独立的 memory_enabled 字段。

## 个人设置

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/memory/settings` | `{success,data:{workspace_enabled,user_enabled,effective,write_mode,episode_count,max_episodes}}` |
| PUT | `/memory/settings` | `{"enabled":true}`，enabled 必填；返回更新后的 settings |

`effective` 表示空间与个人开关的合并结果；一次聊天还受智能体开关约束。

```bash
curl -X PUT "$BASE/api/v1/memory/settings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":true}'
```

## 画像

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/memory/profile` | `{success,data:MemoryDigest}`，尚未生成时 `data` 为 `null` |
| PUT | `/memory/profile` | `{"body":"..."}`；200 `{success,data:{revision}}` |
| DELETE | `/memory/profile` | 200 `{"success":true}` |

MemoryDigest 包括 `body`、`revision`、`episode_count`（由多少段会话记忆改写而来）、`user_edited_at`、`generated_at` 和创建/修改时间。

`body` 是带 `## ` 小标题的文档，最长 2400 个字符，超出或为空返回 400。PUT 走用户编辑路径并记录 `user_edited_at`：这表示文档里有人工措辞，之后的自动改写会整体替换它。有一段会话记忆就会生成第一份画像，在那之前 `data` 为 `null`。

```bash
curl "$BASE/api/v1/memory/profile" -H "Authorization: Bearer $TOKEN"

curl -X PUT "$BASE/api/v1/memory/profile" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"body":"## 用户画像\n- 在做医学影像后端\n\n## 用户偏好\n- 回答先给结论\n"}'
```

## 会话记忆

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/memory/episodes` | 可选 limit、offset；`{success,data:[MemoryEpisode],total}` |
| GET | `/memory/episodes/:id` | 200 `{success,data:MemoryEpisode}` |
| DELETE | `/memory/episodes/:id` | 200 `{"success":true}` |

`limit` 默认 20，合法范围 1–100，越界回落 20；`offset` 默认 0，负值归零。

MemoryEpisode 包括 `id`、`session_id`、`slug`、`title`、`outcome`（success/partial/fail/uncertain）、`summary`、`keywords`、`from_at`、`to_at`、`use_count`、`last_used_at` 和创建/修改时间。一段对话只有一份会话记忆：对话继续后会就地改写，`slug` 保持不变，而不是追加一份新的。

`use_count` 是全系统唯一的保留信号——被读过的会话记忆才会留下，所以它也决定裁剪时谁先被丢。删除一段会话记忆不会立即重写已经包含它的画像，那需要一次模型调用；下次整理会自然修正。

```bash
curl "$BASE/api/v1/memory/episodes?limit=20&offset=0" -H "Authorization: Bearer $TOKEN"

curl -X DELETE "$BASE/api/v1/memory/episodes/ep-1" -H "Authorization: Bearer $TOKEN"
```

## 原话笔记

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| GET | `/memory/notes` | 可选 limit；`{success,data:[MemoryNote]}` |
| POST | `/memory/notes` | `{"content":"..."}`；200 `{success,data:MemoryNote}` |
| DELETE | `/memory/notes/:id` | 200 `{"success":true}` |

MemoryNote 包括 `id`、`content`、`source_session_id`、`source_message_id` 和创建/修改时间。

`content` 最长 300 个字符，为空或超长返回 400。每人最多 20 条，写满后继续新增会报错而不是静默丢弃最旧的一条。重复提交同一句话返回已存在的那条笔记，不新增。`limit` 默认且上限均为 20。

笔记按原文注入，模型不会改写，所以它是「记住：我只用中文」这类明确要求的落点。聊天里说出这类指令也会直接写成笔记，不必调这个接口。

```bash
curl -X POST "$BASE/api/v1/memory/notes" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"content":"回答先给结论，再解释依据"}'
```

## 清空、导出与立即整理

| 方法 | 路径 | 请求 / 响应 |
| --- | --- | --- |
| DELETE | `/memory/all` | 清空当前身份的三层记忆；200 `{success,removed}` |
| GET | `/memory/export` | `{success,total,truncated,data:{profile,episodes,notes}}` |
| POST | `/memory/consolidate` | 200 `{success,data:MemoryConsolidationResult}` |

`GET /memory/export` 带下载文件名 `weknora-memories.json`，响应体仍是 JSON。最多导出 20,000 段会话记忆，触及上限时检查 truncated。

`POST /memory/consolidate` 立即用最近的会话记忆重写一次画像，不等待后台整理。返回 `{merged,demoted,expired,reviewed,candidates,skipped?}`，其中只有 `reviewed`（这次改写读了多少段会话记忆）和 `skipped` 会被填充；`merged`、`demoted`、`expired` 恒为 0，是旧条目合并留下的字段。`skipped` 取值：`too_soon`（刚刚整理过，或另一次改写正在进行）、`too_few_items`（会话记忆还不够写出画像）、`model_unavailable`（模型暂时不可达，原画像保持不变）。

```bash
curl "$BASE/api/v1/memory/export" -H "Authorization: Bearer $TOKEN" \
  -o weknora-memories.json
curl -X POST "$BASE/api/v1/memory/consolidate" -H "Authorization: Bearer $TOKEN"
```

## 已移除的接口

条目接口（`/memory/items` 及其 confirm/reject 子路径）已移除。它存放的是一句话事实，保留了结论却丢掉了产生结论的对话——「用户用 PostgreSQL」能活过那次对话，但当时的理由、被否掉的方案和那个前提都没留下，读到它的回答只能复述偏好而无法照着做事。会话记忆、画像和原话笔记三层接口替代了它，数据不做迁移：叙述要从对话原文写出来，而条目背后没有原文。

主题跟踪（`/memory/topics`）和文档偏好（`/memory/documents`）两组接口也已移除。

「反复问到的方向」不再由单独的主题计数表维护，而是统计会话记忆里同一关键词出现在多少段会话中得出，因此没有可供逐条提升或停止跟踪的行。

文档偏好统计的是「回答引用过哪些文档」，然后在重排里给这些文档一点加成。它度量的其实是检索器自己反复挑中了什么，而不是用户觉得什么有用——等于把检索器过去的选择再喂回给它自己，从没被召回过的文档也就永远攒不出计数。这条信号连同它驱动的 Wiki 图谱「常用资料」高亮一起下线，个性化改由常驻画像（影响查询改写）和命中的会话记忆（注入当轮）承担。

参数无效返回 400；找不到当前身份的记录返回 404；认证/权限不满足返回 401/403。接口没有管理员读取他人记忆的 subject 参数。实现：`internal/handler/memory.go`、`internal/router/routes_memory.go`。
