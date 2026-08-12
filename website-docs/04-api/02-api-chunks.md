# API 参考：分块与标签

分块（chunk）是检索的最小单元，标签用于给文档分类。两组接口都挂在知识库之下，与[知识库与知识](./02-api-knowledge.md)共用同一套权限规则：读为 Viewer+ 且对父 KB 有 read 权限（API key `retrieve`），写为「KB 创建者 OR Admin+」且有 write 权限（API key `ingest`），均受 API key 的 KB 白名单约束。

路由注册：`internal/router/routes_knowledge.go` 的 `RegisterChunkRoutes`、`RegisterKnowledgeTagRoutes`、`RegisterChunkerDebugRoutes`。

通用约定（Base URL、认证、错误码、分页）见 [API 总览](./01-api-overview.md)。

## 分块（/api/v1/chunks）

Handler: `internal/handler/chunk.go`。读：Viewer+ 且父 KB read（API key `retrieve`/full）；写：KB 创建者 OR Admin+ 且父 KB write（API key `ingest`/full）。

### GET /api/v1/chunks/:knowledge_id

用途：知识的分块列表。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `page` | int | 否 | 默认 1 |
| `page_size` | int | 否 | 默认 10，上限 100 |
| `chunk_type` | string | 否 | 可重复，按分块类型过滤 |

响应：200 `{"success":true,"data":[Chunk],"total","page","page_size"}`

```bash
curl "$BASE/api/v1/chunks/k-1?page=1" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/chunks/by-id/:id

用途：按 chunk ID 获取单个分块（无需 knowledge_id）。

响应：200 `{"success":true,"data":{Chunk}}`

```bash
curl $BASE/api/v1/chunks/by-id/c-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/chunks/:knowledge_id/:id

用途：编辑分块内容或启停状态（migration `000078` 起为带版本的乐观更新）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `content` | string | 否 | 新内容，去除首尾空白后不得为空，长度上限 200000 字节 |
| `is_enabled` | bool | 否 | 启用/停用该分块 |
| `expected_revision` | int | 否 | 期望的 `content_revision`，用于乐观并发控制 |

约束与副作用：

- 仅 `text` 类型分块可编辑，其它类型返回 400；
- `expected_revision` 与当前 `content_revision` 不一致时返回 **409**（`Chunk was modified by another user; refresh and retry`）；
- 不允许在编辑中引入源内容里没有的图片 URL；删除 Markdown 图片会同步停用对应的 OCR/caption 子分块；
- 编辑成功后 `content_revision` +1，旧版本写入 `chunk_revisions` 表，`index_status` 依次经历 `processing` → `ready`；重建检索索引失败时行仍保存但 `index_status = failed`，可再次提交同样内容触发重试；
- 子分块编辑会按偏移量回写父分块内容（父分块的 `source_content` 保持不可变）；
- 内容或启停状态变化会入队一次文档摘要刷新。

响应：200 `{"success":true,"data":{Chunk},"summary_status":"pending","description":"..."}`

```bash
curl -X PUT $BASE/api/v1/chunks/k-1/c-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"content":"修正后的内容","expected_revision":2}'
```

### GET /api/v1/chunks/:knowledge_id/:id/revisions

用途：分块的历史版本列表（`chunk_revisions` 表，按 revision 倒序）。

响应：200 `{"success":true,"data":[{ChunkRevision}]}`，单条包含 `revision`、`content`、`is_enabled`、`editor_id`、`edit_source`、`edited_at`。

```bash
curl $BASE/api/v1/chunks/k-1/c-1/revisions -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/chunks/:knowledge_id/:id/revert

用途：回滚到某个历史版本。回滚本身也是一次新编辑：`content_revision` 继续递增，当前内容会被存为新的历史版本。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `revision` | int | 是 | 目标历史版本号（非负） |
| `expected_revision` | int | 否 | 乐观锁，语义同上，冲突返回 409 |

响应：200 `{"success":true,"data":{Chunk},"summary_status":"...","description":"..."}`

```bash
curl -X POST $BASE/api/v1/chunks/k-1/c-1/revert -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"revision":1}'
```

### DELETE /api/v1/chunks/:knowledge_id/:id

用途：删除单个分块。

响应：200 `{"success":true,"message":"Chunk deleted"}`

```bash
curl -X DELETE $BASE/api/v1/chunks/k-1/c-1 -H "Authorization: Bearer $TOKEN"
```

### DELETE /api/v1/chunks/:knowledge_id

用途：删除知识下全部分块。

响应：200 `{"success":true,"message":"All chunks under knowledge deleted"}`

```bash
curl -X DELETE $BASE/api/v1/chunks/k-1 -H "Authorization: Bearer $TOKEN"
```

### DELETE /api/v1/chunks/by-id/:id/questions

用途：删除该分块下某条生成的问题。请求体：`{"question_id":"..."}`（`binding:"required"`）。

响应：200 `{"success":true,"message":"Generated question deleted"}`

```bash
curl -X DELETE $BASE/api/v1/chunks/by-id/c-1/questions -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"question_id":"q-1"}'
```

### PUT /api/v1/chunks/by-id/:id/questions

用途：新增或修改该分块的一条生成问题。请求体：`{"question":"...","question_id":"..."}`，`question` 必填；`question_id` 留空表示新增。

响应：200 `{"success":true,"data":{GeneratedQuestion}}`

```bash
curl -X PUT $BASE/api/v1/chunks/by-id/c-1/questions -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"question_id":"q-1","question":"WeKnora 如何配置向量库？"}'
```

### POST /api/v1/chunks/by-id/:id/questions/regenerate

用途：基于分块当前内容重新生成检索问题。内容编辑后原有问题不会被删除，而是标记为「过期」（revision 与当前正文不匹配），可用本接口刷新。

响应：200 `{"success":true,"data":[{GeneratedQuestion}]}`

```bash
curl -X POST $BASE/api/v1/chunks/by-id/c-1/questions/regenerate -H "Authorization: Bearer $TOKEN"
```

## 标签（/api/v1/knowledge-bases/:id/tags）

Handler: `internal/handler/tag.go`。读：Viewer+ + KB read（API key `retrieve`/full）；写：KB 创建者 OR Admin+ + KB write（API key `ingest`/full）。

### GET /api/v1/knowledge-bases/:id/tags

用途：标签列表。查询参数：`page`、`page_size`、`keyword`（均可选）。

响应：200 `{"success":true,"data":[KnowledgeTag]}`

```bash
curl $BASE/api/v1/knowledge-bases/kb-1/tags -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledge-bases/:id/tags

用途：创建标签。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是（`binding:"required"`） | 标签名 |
| `color` | string | 否 | 颜色 |
| `sort_order` | int | 否 | 排序 |

响应：200 `{"success":true,"data":{KnowledgeTag}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/tags -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"售后"}'
```

### PUT /api/v1/knowledge-bases/:id/tags/:tag_id

用途：更新标签（`tag_id` 支持 UUID 或整数 seq_id）。请求体：`name`/`color`/`sort_order`（指针字段，均可选）。

响应：200 `{"success":true,"data":{KnowledgeTag}}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/tags/t-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"售后支持"}'
```

### DELETE /api/v1/knowledge-bases/:id/tags/:tag_id

用途：删除标签。查询参数：`force`（bool，强制删除）、`content_only`（bool，仅删内容保留标签）。请求体（可选）：`{"exclude_ids":[int64]}`。

响应：200 `{"success":true}`

```bash
curl -X DELETE "$BASE/api/v1/knowledge-bases/kb-1/tags/t-1?force=true" -H "Authorization: Bearer $TOKEN"
```

## 分块调试

### POST /api/v1/chunker/preview

用途：无状态分块预览（KB 编辑器调试面板）。权限：Viewer+；API key `retrieve`/`ingest`/full。Handler: `internal/handler/chunker_debug.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `text` | string | 是（代码校验非空，≤64k 字符） | 样例文本 |
| `chunking_config.chunk_size` | int | 否 | 分块字符数 |
| `chunking_config.chunk_overlap` | int | 否 | 重叠 |
| `chunking_config.separators` | []string | 否 | 分隔符 |
| `chunking_config.strategy` | string | 否 | `auto/heading/heuristic/recursive/legacy` |
| `chunking_config.token_limit` | int | 否 | token 上限 |
| `chunking_config.languages` | []string | 否 | 语言提示 |
| `chunking_config.enable_parent_child` | bool | 否 | 按父子分块试切，返回的是子块（与检索粒度一致） |
| `chunking_config.parent_chunk_size` / `child_chunk_size` | int | 否 | 父/子块大小，缺省 4096 / 384 |

响应：200 `{"success":true,"data":{"selected_tier","tier_chain","rejected","profile","chunks":[...],"stats":{count,avg_chars,min_chars,max_chars,stddev_chars,truncated_to}}}`；文本超长 413；分块超时（5s）504。

```bash
curl -X POST $BASE/api/v1/chunker/preview -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"text":"# 标题\n正文...","chunking_config":{"chunk_size":512}}'
```
