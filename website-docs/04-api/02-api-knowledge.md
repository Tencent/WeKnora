# API 参考：知识库与知识

路由注册：`internal/router/routes_knowledge.go` 的 `RegisterKnowledgeBaseRoutes`、`RegisterKnowledgeRoutes`。Handler：`internal/handler/knowledgebase.go`、`internal/handler/knowledge.go`。

权限速记：读路由为 Viewer+ 且需对 KB 有 read 权限（自有/组织共享/共享 Agent 可见）；写路由为“KB 创建者 OR Admin+”且需 write 权限。API key：读需 `retrieve`，内容写需 `ingest`，KB 生命周期需 `manage_kbs`（均可被 full-access 覆盖），并受 KB 白名单约束。

分块、标签与分块预览接口（`/chunks`、`/knowledge-bases/:id/tags`、`/chunker/preview`）在[分块与标签](./02-api-chunks.md)。

## 知识库（/api/v1/knowledge-bases）

### POST /api/v1/knowledge-bases

用途：创建知识库。权限：Contributor+；API key `manage_kbs`/full。Handler: `internal/handler/knowledgebase.go`

请求体（`types.KnowledgeBase`）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 名称 |
| `description` | string | 否 | 描述 |
| `type` | string | 否 | `document`（默认）/`faq`/`wiki` |
| `embedding_model_id` | string | 否 | Embedding 模型 ID |
| `chunking_config` | object | 否 | 分块配置（chunk_size/overlap/separators/strategy…） |
| `image_processing_config` | object | 否 | 图像处理（多模态）配置 |
| `storage_provider_config` | object | 否 | 存储配置 |
| `vector_store_id` | string | 否 | 向量库绑定（非法返回 code 2200/2201） |
| `faq_config` / `wiki_config` / `extract_config` / `indexing_strategy` | object | 否 | 类型相关配置 |

响应：201 `{"success":true,"data":{KnowledgeBase}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"产品文档","type":"document"}'
```

### GET /api/v1/knowledge-bases

用途：知识库列表。权限：Viewer+；API key `retrieve`/full。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `agent_id` | string | 否 | 过滤某共享 Agent 可见的 KB |
| `agent_source_tenant_id` | uint64 | 否 | 同名 Agent 被多个空间共享时，指定来源空间；取值会与共享关系校验，非法值直接 400 |
| `creator` | string | 否 | `mine` / `others` |

响应：200 `{"success":true,"data":[KnowledgeBase],"total","page","page_size"}`

```bash
curl $BASE/api/v1/knowledge-bases -H "X-API-Key: $API_KEY"
```

### GET /api/v1/knowledge-bases/:id

用途：知识库详情（共享 KB 携带 `my_permission`）。权限：Viewer+，KB read。查询参数：`agent_id`（可选）。

响应：200 `{"success":true,"data":{KnowledgeBase}}`

```bash
curl $BASE/api/v1/knowledge-bases/kb-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledge-bases/:id

用途：更新知识库。权限：创建者 OR Admin+，KB write；API key `manage_kbs`/full。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是（`binding:"required"`） | 名称 |
| `description` | string | 否 | 描述 |
| `config` | object | 否 | 局部配置更新（分块/图像/wiki/索引策略） |

响应：200 `{"success":true,"data":{KnowledgeBase}}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"产品文档 v2"}'
```

### DELETE /api/v1/knowledge-bases/:id

用途：删除知识库（锁定为属主空间 + Admin；共享 editor 不可删）。权限：创建者 OR Admin+，KB write；API key `manage_kbs`/full。

响应：200 `{"success":true,"message":"Knowledge base deleted successfully"}`

```bash
curl -X DELETE $BASE/api/v1/knowledge-bases/kb-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledge-bases/:id/pin

用途：置顶/取消置顶（按用户维度存储）。权限：Viewer+，KB read。无请求体。

响应：200 `{"success":true,"data":{KnowledgeBase(is_pinned 已切换)}}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/pin -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/knowledge-bases/:id/hybrid-search（兼容 GET）

用途：KB 内混合检索（向量+关键词）。权限：Viewer+，KB read；API key `retrieve`/full。GET 携带 JSON body 仅为向后兼容（#1727），推荐 POST。

查询参数：`resource_urls=handle|public`（`public` 把结果 `content` / `image_info` 里的 `resource://` 换成可加载直链，详见 [API 总览](./01-api-overview.md)）。

请求体（`types.SearchParams`）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query_text` | string | 条件必填 | 查询文本（除非提供 `query_embedding`） |
| `query_embedding` | []float32 | 否 | 预计算向量 |
| `vector_threshold` / `keyword_threshold` | float64 | 否 | 匹配阈值 |
| `match_count` | int | 否 | 返回条数上限 |
| `disable_keywords_match` / `disable_vector_match` | bool | 否 | 关闭某一路召回 |
| `knowledge_ids` | []string | 否 | 限定知识条目 |
| `tag_ids` | []string | 否 | 标签过滤（OR） |
| `only_recommended` | bool | 否 | FAQ 仅推荐条目 |
| `skip_context_enrichment` | bool | 否 | 跳过父块/上下文补齐 |

响应：200 `{"success":true,"data":[SearchResult]}`

```bash
curl -X POST "$BASE/api/v1/knowledge-bases/kb-1/hybrid-search?resource_urls=public" -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' -d '{"query_text":"退款流程","match_count":5}'
```

### POST /api/v1/knowledge-bases/copy

用途：跨 KB 拷贝内容（异步任务）。权限：Contributor+；API key `manage_kbs`/full（源/目标 KB 白名单在 handler 校验）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `source_id` | string | 是（`binding:"required"`） | 源 KB |
| `target_id` | string | 否 | 目标 KB（为空则自动创建） |
| `task_id` | string | 否 | 自定义任务 ID |

响应：200 `{"success":true,"data":{"task_id","source_id","target_id","message"}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/copy -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"source_id":"kb-1"}'
```

### POST /api/v1/knowledge-bases/:id/duplicate

用途：创建 KB 副本（仅复制设置，不复制内容/索引/分享）。权限：Contributor+，源 KB read；API key `manage_kbs`/full。无请求体。

响应：201 `{"success":true,"data":{"source_id","target_id","message","knowledge_base":{...}}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/duplicate -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge-bases/copy/progress/:task_id

用途：查询拷贝进度（任务按空间隔离）。权限：Viewer+；API key `retrieve`/`manage_kbs`/full。

响应：200 `{"success":true,"data":{status,progress,message,...}}`

```bash
curl $BASE/api/v1/knowledge-bases/copy/progress/task-1 -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge-bases/:id/move-targets

用途：列出可作为移动目标的 KB（同类型/同 embedding）。权限：Viewer+，KB read。

响应：200 `{"success":true,"data":[KnowledgeBase]}`

```bash
curl $BASE/api/v1/knowledge-bases/kb-1/move-targets -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge-bases/:id/files

用途：KB 范围文件代理（渲染共享 KB 内容中的图片；上下文 tenant 已被重写为 KB 属主）。权限：Viewer+，KB read；KB 受限 key 拒绝，全空间 `retrieve`/full key 放行。注册于 `serveKBScopedFiles`（`internal/router/router.go`）。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file_path` | string | 是 | `provider://...` 存储路径（禁止 `..`） |

响应：200 文件流（`Content-Type` 按扩展名推断；`Cache-Control: private`）。

```bash
curl "$BASE/api/v1/knowledge-bases/kb-1/files?file_path=local://1/exports/chart.png" \
  -H "Authorization: Bearer $TOKEN" -o chart.png
```

## 知识（KB 内容，/api/v1/knowledge-bases/:id/knowledge 与 /api/v1/knowledge）

### POST /api/v1/knowledge-bases/:id/knowledge/file

用途：上传文件创建知识。权限：KB 创建者 OR Admin+，KB write；API key `ingest`/full。Handler: `internal/handler/knowledge.go`

multipart/form-data 字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `file` | file | 是 | 上传文件 |
| `fileName` | string | 否 | 覆盖显示名 |
| `metadata` | JSON 字符串 | 否 | 自定义元数据 |
| `enable_multimodel` | bool | 否 | 多模态处理开关 |
| `tag_ids` | string | 否 | 逗号分隔标签 ID |
| `channel` | string | 否 | 摄取渠道 |
| `process_config` | JSON 字符串 | 否 | 解析配置覆盖（KnowledgeProcessOverrides） |

响应：200 `{"success":true,"data":{Knowledge}}`；重复文件返回 409 且 `data` 为已存在的 Knowledge。

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/knowledge/file \
  -H "X-API-Key: $API_KEY" -F 'file=@./manual.pdf' -F 'enable_multimodel=true'
```

### POST /api/v1/knowledge-bases/:id/knowledge/url

用途：从 URL 抓取创建知识。权限/API key 同上。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `url` | string | 是（`binding:"required"`） | 抓取地址 |
| `file_name` / `file_type` / `title` | string | 否 | 覆盖信息 |
| `enable_multimodel` | *bool | 否 | 多模态开关 |
| `tag_ids` | []string | 否 | 标签 |
| `channel` | string | 否 | 渠道 |
| `process_config` | object | 否 | 解析覆盖 |

响应：201 `{"success":true,"data":{Knowledge}}`；重复 URL 返回 409。

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/knowledge/url -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' -d '{"url":"https://example.com/doc"}'
```

### POST /api/v1/knowledge-bases/:id/knowledge/manual

用途：创建手工（Markdown）知识。权限/API key 同上。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `title` | string | 否 | 标题 |
| `content` | string | 否 | Markdown 内容 |
| `status` | string | 否 | `draft` / `publish` |
| `tag_ids` | []string | 否 | 标签 |
| `channel` | string | 否 | 渠道 |
| `process_config` | object | 否 | 解析覆盖 |

响应：200 `{"success":true,"data":{Knowledge}}`

```bash
curl -X POST $BASE/api/v1/knowledge-bases/kb-1/knowledge/manual -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"title":"FAQ 汇总","content":"# 内容","status":"publish"}'
```

### GET /api/v1/knowledge-bases/:id/knowledge

用途：KB 下知识列表。权限：Viewer+，KB read；API key `retrieve`/full。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `page` / `page_size` | int | 否 | 分页（默认 1/20） |
| `tag_ids` | string | 否 | 逗号分隔标签（OR） |
| `keyword` | string | 否 | 关键字 |
| `file_type` | string | 否 | 文件类型过滤 |
| `parse_status` | string | 否 | `pending/processing/completed/failed` |
| `source` | string | 否 | 渠道或 `manual`/`url` |
| `start_time` / `end_time` | string | 否 | RFC3339，按 `updated_at` 过滤 |

响应：200 `{"success":true,"data":[Knowledge],"total","page","page_size"}`

```bash
curl "$BASE/api/v1/knowledge-bases/kb-1/knowledge?page=1&parse_status=completed" -H "X-API-Key: $API_KEY"
```

### GET /api/v1/knowledge-bases/:id/knowledge/folders

用途：获取知识库的文件夹目录树。整目录上传时目录结构会被保留（migration `000079` 起存在 `knowledges.folder_path` 列，早期把路径塞在 `file_name` 里的数据已回填）。权限：Viewer+ + KBAccessRead。

响应：200 `{"success":true,"data":[{FolderNode}]}`

```bash
curl $BASE/api/v1/knowledge-bases/kb-1/knowledge/folders -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledge-bases/:id/knowledge/folders

用途：重命名或移动文件夹，连同其所有子目录一起改路径。目标路径已存在时两个文件夹合并；不允许移动到自己的子目录下。权限：KB owner 或 Admin+ + KBAccessWrite。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `from` | string | 是 | 原路径 |
| `to` | string | 是 | 新路径 |

响应：200 `{"success":true}`

```bash
curl -X PUT $BASE/api/v1/knowledge-bases/kb-1/knowledge/folders -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"from":"设计文档/旧版","to":"归档/设计文档"}'
```

### DELETE /api/v1/knowledge-bases/:id/knowledge

用途：清空 KB 全部内容（破坏性）。权限：Admin+，KB write；API key 仅 full-access。

响应：200 `{"success":true,"message":"Knowledge base contents clear task submitted","data":{"deleted_count":N}}`

```bash
curl -X DELETE $BASE/api/v1/knowledge-bases/kb-1/knowledge -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge/batch

用途：按 ID 批量获取知识（跨 KB，handler 自行校验访问）。权限：Viewer+；API key `retrieve`/full。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `ids` | []string | 是 | 知识 ID（可重复传参或逗号分隔） |
| `kb_id` | string | 否 | 限定 KB |
| `agent_id` | string | 否 | 共享 Agent 范围 |
| `agent_source_tenant_id` | uint64 | 否 | 共享 Agent 的来源空间选择器，与共享关系校验 |

响应：200 `{"success":true,"data":[Knowledge]}`

```bash
curl "$BASE/api/v1/knowledge/batch?ids=k-1&ids=k-2" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge/:id

用途：知识详情。权限：Viewer+，父 KB read。

响应：200 `{"success":true,"data":{Knowledge}}`

```bash
curl $BASE/api/v1/knowledge/k-1 -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge/:id/stages 与 GET /api/v1/knowledge/:id/spans

用途：解析阶段/trace（两条路径同一 handler `GetKnowledgeSpans`）。权限：Viewer+，父 KB read。查询参数：`attempt`（int，0=最新一次）。

响应：200 `{"success":true,"data":{"knowledge_id","attempt","latest_attempt","parse_status","current_stage","trace":{...},"last_error":{...}}}`

```bash
curl $BASE/api/v1/knowledge/k-1/spans -H "Authorization: Bearer $TOKEN"
```

### DELETE /api/v1/knowledge/:id

用途：删除知识（异步）。权限：KB 创建者 OR Admin+，KB write；API key `ingest`/full。

响应：200 `{"success":true,"message":"Delete task submitted","data":{"task_id"}}`

```bash
curl -X DELETE $BASE/api/v1/knowledge/k-1 -H "X-API-Key: $API_KEY"
```

### PUT /api/v1/knowledge/:id

用途：更新知识元信息。权限同上。请求体（`types.Knowledge` 子集）：`title`、`description`、`tags`、`custom_metadata`（均可选）。

`custom_metadata` 是用户自填的描述性元数据（与系统内部使用的 `metadata` 分开存放，migration `000078`），校验规则见 `internal/application/service/knowledge.go`：

| 约束 | 值 |
| --- | --- |
| 字段数 | ≤ 20 |
| 键长度 | 1-64 字符，不能为空白 |
| 值类型 | string / number / boolean / null |
| 值长度 | ≤ 1000 字符 |

整体覆盖式更新（传入的对象替换原有对象）。元数据发生变化且该文档已有摘要时，会自动入队一次摘要刷新（`summary_status` 转为 `pending`）。元数据文本会参与摘要生成与文档级模型上下文（`Knowledge.CustomMetadataText()`）。

响应：200 `{"success":true,"message":"Knowledge updated successfully","data":{Knowledge}}`

```bash
curl -X PUT $BASE/api/v1/knowledge/k-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"新标题","custom_metadata":{"部门":"研发中心","密级":"内部","版本":3}}'
```

### POST /api/v1/knowledge/:id/regenerate-summary

用途：在分块内容或自定义元数据被编辑后，重新生成该文档的摘要。权限：KB owner 或 Admin+，且对父 KB 有 write 权限。

行为分两种：文档此前没有摘要（`summary_status` 为空或 `none`）时同步触发一次生成；已有摘要时改为入队刷新任务，`summary_status` 转为 `pending`，由 `knowledge_summary_refresh.go` 异步执行。

响应：200 `{"success":true,"data":{Knowledge}}`

```bash
curl -X POST $BASE/api/v1/knowledge/k-1/regenerate-summary -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledge/manual/:id

用途：更新手工知识内容（`ManualKnowledgePayload` 子集：`title/content/status/...`）。权限同上。

响应：200 `{"success":true,"data":{Knowledge}}`

```bash
curl -X PUT $BASE/api/v1/knowledge/manual/k-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"content":"# 更新内容","status":"publish"}'
```

### POST /api/v1/knowledge/:id/reparse

用途：重新解析知识。权限同上。请求体（可选）：`{"process_config":{...}}`。

响应：200 `{"success":true,"message":"Reparse task submitted","data":{Knowledge}}`

```bash
curl -X POST $BASE/api/v1/knowledge/k-1/reparse -H "X-API-Key: $API_KEY"
```

### POST /api/v1/knowledge/:id/cancel-parse

用途：取消解析。权限同上。无请求体。

响应：200 `{"success":true,"message":"Knowledge parse cancelled","data":{Knowledge}}`

```bash
curl -X POST $BASE/api/v1/knowledge/k-1/cancel-parse -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge/:id/download

用途：下载原始源文件（比预览更严格：Contributor+ 且 KB write；组织共享 Viewer 不可下载源文件）。API key `retrieve`/full。

响应：200 二进制流（`application/octet-stream`）。

```bash
curl -OJ $BASE/api/v1/knowledge/k-1/download -H "Authorization: Bearer $TOKEN"
```

工作空间事件回调不要走这条登录态下载。接收方用回调里的 5 分钟票请求 `GET /api/v1/files/knowledge-download/:id`，Header `X-WeKnora-Download-Ticket`。过期后 1 小时内可 `POST .../:id/renew`。详见[工作空间知识事件回调](../03-features/22-workspace-webhooks.md)。

### GET /api/v1/knowledge/:id/preview

用途：预览解析后的文件内容。权限：Viewer+，KB read。

响应：200 预览流（文本/HTML）。

```bash
curl $BASE/api/v1/knowledge/k-1/preview -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledge/image/:id/:chunk_id

用途：更新某分块的图片信息（caption/OCR 等）。权限：KB 创建者 OR Admin+，KB write。路径参数：`id` 知识 ID、`chunk_id` 分块 ID。请求体为图片信息 JSON。

响应：200 `{"success":true,...}`

```bash
curl -X PUT $BASE/api/v1/knowledge/image/k-1/c-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"caption":"架构图"}'
```

### GET /api/v1/knowledge/search

用途：跨 KB 文件搜索（会话 @文件 选择器）。权限：Viewer+；API key `retrieve`/full。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `q` | string | 否 | 关键字（为空且 `recent=true` 返回最近文件） |
| `file_type` / `file_types` | string | 否 | 类型过滤（后者逗号分隔） |
| `page` / `page_size` | int | 否 | 分页 |
| `recent` | bool | 否 | 最近文件模式 |
| `agent_id` | string | 否 | 共享 Agent 范围 |
| `agent_source_tenant_id` | uint64 | 否 | 共享 Agent 的来源空间选择器，与共享关系校验 |

响应：200 `{"success":true,"data":[Knowledge]}`

```bash
curl "$BASE/api/v1/knowledge/search?q=报告&recent=false" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/knowledge/move/progress/:task_id

用途：查询移动任务进度。权限：Viewer+；API key `retrieve`/full。

响应：200 `{"success":true,"data":{MoveProgress}}`

```bash
curl $BASE/api/v1/knowledge/move/progress/task-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/knowledge/tags

用途：批量更新知识标签。权限：Contributor+；API key `ingest`/full（KB 白名单在 handler 校验）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `updates` | map[string][]string | 是（`binding:"required,min=1"`） | knowledge_id → tag_ids |
| `kb_id` | string | 否 | 限定 KB |

响应：200 `{"success":true}`

```bash
curl -X PUT $BASE/api/v1/knowledge/tags -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"updates":{"k-1":["t-1"]},"kb_id":"kb-1"}'
```

### POST /api/v1/knowledge/batch-reparse

用途：批量重解析。权限：Contributor+；API key `ingest`/full。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `kb_id` | string | 是（`binding:"required"`） | KB ID |
| `ids` | []string | 是（`binding:"required"`） | 知识 ID 列表 |
| `process_config` | object | 否 | 解析覆盖 |

响应：200 `{"success":true,"message":"Batch reparse task submitted","data":{"task_id"}}`

```bash
curl -X POST $BASE/api/v1/knowledge/batch-reparse -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' -d '{"kb_id":"kb-1","ids":["k-1","k-2"]}'
```

### POST /api/v1/knowledge/batch-delete

用途：批量删除（≤200 条）。权限：Contributor+；API key `ingest`/full。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `kb_id` | string | 是（`binding:"required"`） | KB ID |
| `ids` | []string | 是（`binding:"required"`） | 知识 ID 列表（≤200） |

响应：200 `{"success":true,"message":"Batch delete task submitted","data":{"task_id","deleted_count"}}`

```bash
curl -X POST $BASE/api/v1/knowledge/batch-delete -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"kb_id":"kb-1","ids":["k-1"]}'
```

### POST /api/v1/knowledge/folder

用途：把若干文档归类到指定文件夹（只改归类，不动知识库归属，也不重新解析）。权限：Contributor+ / API key `ingest`。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `kb_id` | string | 是 | 知识库 ID |
| `knowledge_ids` | []string | 是 | 待移动的文档 |
| `folder_path` | string | 否 | 目标文件夹；空字符串表示移回知识库根目录 |

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/knowledge/folder -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"kb_id":"kb-1","knowledge_ids":["k-1","k-2"],"folder_path":"设计文档"}'
```

### POST /api/v1/knowledge/move

用途：跨 KB 移动知识（异步）。权限：Contributor+；API key `ingest`/full（源+目标 KB 均需在白名单）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `knowledge_ids` | []string | 是（`binding:"required,min=1"`） | 待移动知识 |
| `source_kb_id` | string | 是（`binding:"required"`） | 源 KB |
| `target_kb_id` | string | 是（`binding:"required"`） | 目标 KB |
| `mode` | string | 是（`binding:"required,oneof=reuse_vectors reparse"`） | 复用向量或重解析 |

响应：200 `{"success":true,"data":{"task_id","source_kb_id","target_kb_id","knowledge_count","message"}}`

```bash
curl -X POST $BASE/api/v1/knowledge/move -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"knowledge_ids":["k-1"],"source_kb_id":"kb-1","target_kb_id":"kb-2","mode":"reuse_vectors"}'
```
