# API 参考：FAQ 与 Wiki

路由注册：`internal/router/router.go` 的 `RegisterFAQRoutes` 与 `RegisterWikiPageRoutes`。Handler：`internal/handler/faq.go`、`internal/handler/wiki_page.go`。

两组均为 KB 内容子资源：读为 Viewer+ 且 KB read（API key `retrieve`/full）；写为“KB 创建者 OR Admin+”且 KB write（API key `ingest`/full），并受 KB 白名单约束。

## FAQ（/api/v1/knowledge-bases/:id/faq）

### GET /api/v1/knowledge-bases/:id/faq/entries

用途：FAQ 条目列表。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `page` / `page_size` | int | 否 | 分页 |
| `tag_id` | int | 否 | 旧版单标签 seq_id |
| `tag_ids` | string | 否 | 逗号分隔标签 UUID |
| `keyword` | string | 否 | 关键字 |
| `search_field` | string | 否 | `standard_question`/`similar_questions`/`answers`（默认全字段） |
| `sort_order` | string | 否 | `asc`（默认按更新时间倒序） |

响应：200 `{"success":true,"data":{分页 FAQEntry 列表}}`

```bash
curl "$BASE/api/v1/knowledge-bases/kb-1/faq/entries?page=1" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge-bases/:id/faq/entries/export

用途：导出 FAQ。查询参数：`format`（`csv` 默认 / `json`）。

响应：200 文件下载（`text/csv` 或 `application/json`）。

```bash
curl -OJ "$BASE/api/v1/knowledge-bases/kb-1/faq/entries/export?format=csv" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge-bases/:id/faq/entries/:entry_id

用途：FAQ 条目详情（`entry_id` 为整数 seq_id）。

响应：200 `{"success":true,"data":{FAQEntry}}`

```bash
curl $BASE/api/v1/knowledge-bases/kb-1/faq/entries/12 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledge-bases/:id/faq/entries

用途：批量 upsert / 导入（异步任务）。Handler 方法 `UpsertEntries`。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `entries` | []FAQEntryPayload | 是（`binding:"required"`） | 批量条目 |
| `mode` | string | 是（`binding:"oneof=append replace"`） | 追加或替换 |
| `knowledge_id` | string | 否 | FAQ 知识实体 ID |
| `task_id` | string | 否 | 自定义任务 ID |
| `dry_run` | bool | 否 | 仅校验不落库 |

响应：200 `{"success":true,"data":{"task_id"}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/faq/entries -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"mode":"append","entries":[{"standard_question":"如何退款?","answers":["联系客服"]}]}'
```

### POST /api/v1/knowledge-bases/:id/faq/entry

用途：创建单条 FAQ。请求体（`types.FAQEntryPayload`）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `standard_question` | string | 是（`binding:"required"`） | 标准问 |
| `similar_questions` | []string | 否 | 相似问 |
| `negative_questions` | []string | 否 | 负样例问 |
| `answers` | []string | 否 | 答案列表 |
| `answer_strategy` | string | 否 | `all` / `random` |
| `tag_id` | int64 | 否 | 标签 seq_id |
| `tag_name` | string | 否 | 标签名 |
| `is_enabled` / `is_recommended` | *bool | 否 | 启用/推荐 |

响应：200 `{"success":true,"data":{FAQEntry}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/faq/entry -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"standard_question":"如何退款?","answers":["7 天内可退"]}'
```

### PUT /api/v1/knowledge-bases/:id/faq/entries/:entry_id

用途：更新单条 FAQ（请求体同创建）。

响应：200 `{"success":true,"data":{FAQEntry}}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/faq/entries/12 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"standard_question":"如何退款?","answers":["30 天内可退"]}'
```

### POST /api/v1/knowledge-bases/:id/faq/entries/:entry_id/similar-questions

用途：追加相似问。请求体：`{"similar_questions":["..."]}`（`binding:"required,min=1"`）。

响应：200 `{"success":true,"data":{FAQEntry}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/faq/entries/12/similar-questions \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"similar_questions":["退款怎么操作"]}'
```

### PUT /api/v1/knowledge-bases/:id/faq/entries/fields

用途：批量更新条目字段（`is_enabled`/`is_recommended`/`tag_id`）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `by_id` | map[int64]object | 否 | 按条目 seq_id 更新 |
| `by_tag` | map[int64]object | 否 | 按标签批量更新 |
| `exclude_ids` | []int64 | 否 | `by_tag` 时排除的条目 |

响应：200 `{"success":true}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/faq/entries/fields -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"by_id":{"12":{"is_enabled":false}}}'
```

### PUT /api/v1/knowledge-bases/:id/faq/entries/tags

用途：批量改条目标签。请求体：`{"updates":{"<entry_id>":<tag_id|null>}}`（`binding:"required,min=1"`；null 移除标签）。

响应：200 `{"success":true}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/faq/entries/tags -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"updates":{"12":3}}'
```

### DELETE /api/v1/knowledge-bases/:id/faq/entries

用途：批量删除条目。请求体：`{"ids":[int64]}`（`binding:"required,min=1"`）。

响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/knowledge-bases/kb-1/faq/entries -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"ids":[12,13]}'
```

### POST /api/v1/knowledge-bases/:id/faq/search

用途：FAQ 检索（只读语义，scoped key 用 `retrieve` 亦可调用）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query_text` | string | 是（`binding:"required"`） | 查询 |
| `vector_threshold` | float64 | 否 | 向量阈值 |
| `match_count` | int | 否 | 默认 10，上限 200 |
| `first_priority_tag_ids` / `second_priority_tag_ids` | []int64 | 否 | 标签优先级过滤 |
| `only_recommended` | bool | 否 | 仅推荐条目 |

响应：200 `{"success":true,"data":[FAQEntry(含 match_type/score)]}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/faq/search -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' -d '{"query_text":"退款"}'
```

### PUT /api/v1/knowledge-bases/:id/faq/import/last-result/display

用途：设置最近一次导入结果面板的显示状态。请求体：`{"display_status":"open|close"}`（`binding:"required,oneof=open close"`）。

响应：200 `{"success":true}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/faq/import/last-result/display \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"display_status":"close"}'
```

### GET /api/v1/faq/import/progress/:task_id

用途：查询 FAQ 导入/dry-run 进度（任务按空间隔离）。权限：Viewer+；API key `retrieve`/`ingest`/full。

响应：200 `{"success":true,"data":{status,progress,failed_entries,...}}`

```bash
curl $BASE/api/v1/faq/import/progress/task-1 -H "X-API-Key: $API_KEY"
```

## Wiki（/api/v1/knowledgebase/:kb_id/wiki）

注意此组前缀为 `/knowledgebase/:kb_id/wiki`（单数，无连字符）。Handler: `internal/handler/wiki_page.go`。本组响应多为**原始对象**（不带 `success` 包装）。

### GET /api/v1/knowledgebase/:kb_id/wiki/pages

用途：Wiki 页面列表。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `page_type` | string | 否 | 逗号分隔类型 |
| `status` | string | 否 | 页面状态 |
| `query` | string | 否 | 全文搜索 |
| `category_path` | string | 否 | `/` 分隔路径过滤 |
| `folder_id` | string | 否 | 精确目录过滤（空串=根） |
| `category_depth` | int | 否 | 目录深度 |
| `page` / `page_size` | int | 否 | 分页（默认 1/20） |
| `sort_by` / `sort_order` | string | 否 | 排序（默认 `updated_at` desc） |

响应：200 `WikiPageListResponse`

```bash
curl "$BASE/api/v1/knowledgebase/kb-1/wiki/pages?page=1" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledgebase/:kb_id/wiki/pages

用途：创建页面。请求体（`types.WikiPage`）：`slug`、`title`、`content`、`folder_id`、`page_type` 等（均可选，slug 缺省自动生成）。

响应：201 `WikiPage`

```bash
curl -X POST $BASE/api/v1/knowledgebase/kb-1/wiki/pages -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"title":"架构概览","content":"# 概览"}'
```

### PUT /api/v1/knowledgebase/:kb_id/wiki/move-page

用途：移动页面到目录。请求体：`{"slug":"<页面slug>","folder_id":"<目录ID|空=根>"}`（slug 必填）。

响应：200 `WikiPage`

```bash
curl -X PUT $BASE/api/v1/knowledgebase/kb-1/wiki/move-page -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"slug":"overview","folder_id":"f-1"}'
```

### GET /api/v1/knowledgebase/:kb_id/wiki/pages/*slug

用途：获取页面（`*slug` 为通配路径）。

响应：200 `WikiPage`

```bash
curl $BASE/api/v1/knowledgebase/kb-1/wiki/pages/overview -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledgebase/:kb_id/wiki/pages/*slug

用途：更新页面（请求体同创建）。旧版本会先整份快照进 `wiki_page_revisions`，`version` 递增，`last_edit_source` 记为 `user`（Agent 工具写入时为 `agent`）。

响应：200 `WikiPage`

```bash
curl -X PUT $BASE/api/v1/knowledgebase/kb-1/wiki/pages/overview -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"content":"# 更新后的概览"}'
```

### GET /api/v1/knowledgebase/:kb_id/wiki/revisions/*slug

用途：页面版本历史（migration `000075`）。权限：Viewer+ + KBAccessRead。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `version` | int | 否 | 传入时返回**该版本全文**（用于 diff），无效或 < 1 返回 400，找不到返回 404 |
| `limit` | int | 否 | 默认 50，上限 200；仅列表模式生效 |
| `offset` | int | 否 | 分页偏移 |

不带 `version` 时返回历史列表（版本号倒序、**不含正文**）加上页面当前版本号；每条含 `edit_source`（`pipeline` / `agent` / `user` / `revert`）、`editor_id`、`edited_at`。

历史保留是两级上限：软上限 50 版只裁剪 `pipeline` 与空来源的快照，硬上限 200 版对所有来源生效，因此人工编辑不会被管道刷掉。

```bash
# 历史列表
curl $BASE/api/v1/knowledgebase/kb-1/wiki/revisions/entity/acme-corp -H "Authorization: Bearer $TOKEN"
# 取第 3 版全文
curl "$BASE/api/v1/knowledgebase/kb-1/wiki/revisions/entity/acme-corp?version=3" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledgebase/:kb_id/wiki/revert

用途：把页面回滚到某个历史版本。权限：KB owner 或 Admin+ + KBAccessWrite。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `slug` | string | 是 | 目标页面 |
| `version` | int | 是 | 目标版本号（≥ 1） |

回滚**不会把版本号退回去**：目标版本的内容会作为一个新版本写入，`last_edit_source` 记为 `revert`，所以回滚也能被回滚。回滚到当前版本返回 400（一般是前端历史列表过期）。

响应：200 `WikiPage`

```bash
curl -X POST $BASE/api/v1/knowledgebase/kb-1/wiki/revert -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"slug":"entity/acme-corp","version":3}'
```

### DELETE /api/v1/knowledgebase/:kb_id/wiki/pages/*slug

用途：删除页面。

响应：204 No Content

```bash
curl -X DELETE $BASE/api/v1/knowledgebase/kb-1/wiki/pages/overview -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledgebase/:kb_id/wiki/folders

用途：目录列表。查询参数：`parent_id`（空=根）、`page_types`（逗号分隔）。

响应：200 `WikiFolderListResponse`

```bash
curl $BASE/api/v1/knowledgebase/kb-1/wiki/folders -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledgebase/:kb_id/wiki/folders

用途：创建目录。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 目录名 |
| `parent_id` | string | 否 | 父目录 |

响应：201 `WikiFolder`

```bash
curl -X POST $BASE/api/v1/knowledgebase/kb-1/wiki/folders -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"设计文档"}'
```

### PUT /api/v1/knowledgebase/:kb_id/wiki/folders/:folder_id

用途：重命名/移动目录。请求体：`name`、`parent_id`、`move_parent`（bool），均可选。

响应：200 `WikiFolder`

```bash
curl -X PUT $BASE/api/v1/knowledgebase/kb-1/wiki/folders/f-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"架构设计"}'
```

### DELETE /api/v1/knowledgebase/:kb_id/wiki/folders/:folder_id

用途：删除目录。

响应：204 No Content

```bash
curl -X DELETE $BASE/api/v1/knowledgebase/kb-1/wiki/folders/f-1 -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledgebase/:kb_id/wiki/index

用途：Wiki 索引页（按类型分组窗口）。查询参数：`types`（逗号分隔）、`limit`（1-200，默认 50）、`cursor`（游标）。

响应：200 `WikiIndexResponse`

```bash
curl $BASE/api/v1/knowledgebase/kb-1/wiki/index -H "Authorization: Bearer $TOKEN"
```

::: warning 已移除
`GET /api/v1/knowledgebase/:kb_id/wiki/log`（Wiki 变更日志）已随 migration `000077_remove_wiki_log` 一并下线，`wiki_log_entries` 表被删除。Wiki 变更现在统一投影到知识库活动流，改用 `GET /api/v1/knowledge-bases/:id/activity`。
:::

### GET /api/v1/knowledgebase/:kb_id/wiki/graph

用途：页面关系图。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `mode` | string | 否 | `overview`（默认）/ `ego` |
| `center` | string | 否 | ego 模式中心 slug（ego 时必填） |
| `depth` | int | 否 | 1-3，默认 1 |
| `types` | string | 否 | page_type 过滤 |
| `limit` | int | 否 | 默认 500，上限 2000 |

响应：200 `WikiGraphData`

```bash
curl "$BASE/api/v1/knowledgebase/kb-1/wiki/graph?mode=overview" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledgebase/:kb_id/wiki/stats

用途：Wiki 统计。

响应：200 `WikiStats`

```bash
curl $BASE/api/v1/knowledgebase/kb-1/wiki/stats -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledgebase/:kb_id/wiki/search

用途：页面搜索。查询参数：`q`（必填）、`limit`（默认 10）。

响应：200 `{"pages":[WikiPage]}`

```bash
curl "$BASE/api/v1/knowledgebase/kb-1/wiki/search?q=部署" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledgebase/:kb_id/wiki/source-chunks/*slug

用途：按 Wiki 页面展开其 `chunk_refs` 对应的**全部原文分块**（存储顺序、全文，无 8 块上限）。摘要页或引文缺失时 `chunks` 为空且 `reason=no_chunk_refs`。某条 ref 已删除时该项 `missing=true`。

响应：200 `WikiPageSourceChunksResult`

```json
{
  "knowledge_base_id": "kb-1",
  "slug": "concept/rag",
  "title": "RAG",
  "page_type": "concept",
  "sources": [{"knowledge_id": "doc-a", "title": "手册A"}],
  "chunks": [
    {
      "id": "chunk-1",
      "knowledge_id": "doc-a",
      "knowledge_title": "手册A",
      "chunk_index": 3,
      "content": "原文……"
    }
  ],
  "chunk_ref_count": 1
}
```

```bash
curl $BASE/api/v1/knowledgebase/kb-1/wiki/source-chunks/concept/rag -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledgebase/:kb_id/wiki/rebuild-links

用途：重建页面互链。写权限。无请求体。

响应：200 `{"message":"Links rebuilt successfully"}`

```bash
curl -X POST $BASE/api/v1/knowledgebase/kb-1/wiki/rebuild-links -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledgebase/:kb_id/wiki/lint

用途：Wiki 一致性检查报告。

响应：200 `WikiLintReport`

```bash
curl $BASE/api/v1/knowledgebase/kb-1/wiki/lint -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledgebase/:kb_id/wiki/auto-fix

用途：自动修复 lint 问题。写权限。无请求体。

响应：200 `{"fixed":N,"message":"Auto-fixed N issues"}`

```bash
curl -X POST $BASE/api/v1/knowledgebase/kb-1/wiki/auto-fix -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledgebase/:kb_id/wiki/issues

用途：问题列表。查询参数：`slug`（按页面过滤）、`status`（`pending/ignored/resolved`）。

响应：200 `[WikiPageIssue]`

```bash
curl $BASE/api/v1/knowledgebase/kb-1/wiki/issues -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledgebase/:kb_id/wiki/issues/:issue_id/status

用途：更新问题状态。写权限。请求体：`{"status":"pending|ignored|resolved"}`（`binding:"required"`）。

响应：200 `{"message":"Issue status updated successfully"}`

```bash
curl -X PUT $BASE/api/v1/knowledgebase/kb-1/wiki/issues/i-1/status -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"status":"resolved"}'
```
