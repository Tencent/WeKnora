# API 参考：基础设施与数据源

路由注册：`internal/router/router.go` 的 `RegisterVectorStoreRoutes`、`RegisterStorageBackendRoutes`、`RegisterWebSearchRoutes`、`RegisterWebSearchProviderRoutes`、`RegisterDataSourceRoutes`。Handler：`internal/handler/vectorstore.go`、`internal/handler/storagebackend.go`、`internal/handler/web_search.go`、`internal/handler/web_search_provider.go`、`internal/handler/web_search_provider_credentials.go`、`internal/handler/datasource.go`、`internal/handler/datasource_credentials.go`。

统一约定：读 Viewer+，写/连接测试 Admin+（凭证探测外部系统）。API key capability：向量库 `manage_vector_stores`、存储后端 `manage_storage_backends`、Web 搜索 `manage_web_search`、数据源 `manage_datasources`（均可 full-access）。

## 向量存储（/api/v1/vector-stores）

### GET /api/v1/vector-stores/types

用途：可用引擎类型与配置 schema。权限：Viewer+。

响应：200 `{"success":true,"data":[类型定义]}`

```bash
curl $BASE/api/v1/vector-stores/types -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/vector-stores/test

用途：用原始配置测试连接（不落库）。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `engine_type` | string | 是（`binding:"required"`） | 引擎类型 |
| `connection_config` | object | 是（`binding:"required"`） | 连接配置 |

响应：200 `{"success":true|false,"version":"...","error":"..."}`

```bash
curl -X POST $BASE/api/v1/vector-stores/test -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"engine_type":"qdrant","connection_config":{"addr":"qdrant:6334"}}'
```

### POST /api/v1/vector-stores

用途：创建向量库配置。权限：Admin+。字段：`name`（必填）、`engine_type`（必填）、`connection_config`（必填）、`index_config`（可选）。

响应：201 `{"success":true,"data":{VectorStoreResponse}}`（`id,tenant_id,name,engine_type,connection_config,index_config,...`）

```bash
curl -X POST $BASE/api/v1/vector-stores -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"qdrant-main","engine_type":"qdrant","connection_config":{"addr":"qdrant:6334"}}'
```

### GET /api/v1/vector-stores

用途：向量库列表（环境变量注入的 `__env_*` store 在前）。权限：Viewer+。

响应：200 `{"success":true,"data":[VectorStoreResponse]}`

```bash
curl $BASE/api/v1/vector-stores -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/vector-stores/:id

用途：向量库详情（支持 `__env_*` ID）。权限：Viewer+。

响应：200 `{"success":true,"data":{VectorStoreResponse}}`

```bash
curl $BASE/api/v1/vector-stores/vs-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/vector-stores/:id

用途：更新（仅重命名；env store 不可改）。权限：Admin+。请求体：`{"name":"..."}`（`binding:"required"`）。

响应：200 `{"success":true,"data":{VectorStoreResponse}}`

```bash
curl -X PUT $BASE/api/v1/vector-stores/vs-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"qdrant-prod"}'
```

### DELETE /api/v1/vector-stores/:id

用途：删除（env store 不可删）。权限：Admin+。

响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/vector-stores/vs-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/vector-stores/:id/test

用途：测试已保存/env 向量库。权限：Admin+。

响应：200 `{"success":true|false,"version","error"}`

```bash
curl -X POST $BASE/api/v1/vector-stores/vs-1/test -H "Authorization: Bearer $TOKEN"
```

## 存储后端（/api/v1/storage-backends）

请求体（Create/Update/TestRaw 共用 `storageBackendRequest`）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是（`binding:"required"`） | 名称 |
| `provider` | string | 是（`binding:"required"`） | 提供方（minio/cos/tos/s3/oss/ks3/obs…） |
| `config` | object | 否 | 提供方配置（响应中凭证掩码） |
| `status` | string | 否 | 状态 |

### GET /api/v1/storage-backends/types

用途：允许的存储类型。权限：Viewer+。响应：200 `{"success":true,"data":[...]}`

```bash
curl $BASE/api/v1/storage-backends/types -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/storage-backends/test

用途：原始配置连接测试。权限：Admin+。响应：200 `{"success":bool,"error"}`

```bash
curl -X POST $BASE/api/v1/storage-backends/test -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"t","provider":"minio","config":{"endpoint":"minio:9000"}}'
```

### POST /api/v1/storage-backends

用途：创建存储后端。权限：Admin+。响应：201 `{"success":true,"data":{StorageBackend}}`

```bash
curl -X POST $BASE/api/v1/storage-backends -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"minio-main","provider":"minio","config":{"endpoint":"minio:9000"}}'
```

### GET /api/v1/storage-backends

用途：列表（含 `default_storage_backend_id`）。权限：Viewer+。响应：200 `{"success":true,"data":[...],"default_storage_backend_id":"..."}`

```bash
curl $BASE/api/v1/storage-backends -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/storage-backends/:id

用途：详情（凭证掩码）。权限：Viewer+。响应：200 `{"success":true,"data":{StorageBackend}}`

```bash
curl $BASE/api/v1/storage-backends/sb-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/storage-backends/:id

用途：更新。权限：Admin+。响应：200 `{"success":true,"data":{StorageBackend}}`

```bash
curl -X PUT $BASE/api/v1/storage-backends/sb-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"minio-prod","provider":"minio"}'
```

### DELETE /api/v1/storage-backends/:id

用途：删除。权限：Admin+。响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/storage-backends/sb-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/storage-backends/:id/test

用途：测试已保存后端。权限：Admin+。响应：200 `{"success":bool,"error"}`

```bash
curl -X POST $BASE/api/v1/storage-backends/sb-1/test -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/storage-backends/:id/default

用途：设为默认后端。权限：Admin+。响应：200 `{"success":true}`

```bash
curl -X PUT $BASE/api/v1/storage-backends/sb-1/default -H "Authorization: Bearer $TOKEN"
```

## Web 搜索（/api/v1/web-search 与 /api/v1/web-search-providers）

### GET /api/v1/web-search/providers

用途：内置搜索提供方目录（只读）。权限：Viewer+，仅 JWT（未声明 API key 策略）。Handler: `internal/handler/web_search.go`

响应：200 `{"success":true,"data":[...]}`

```bash
curl $BASE/api/v1/web-search/providers -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/web-search-providers/types

用途：提供方类型与参数 schema。权限：Viewer+。Handler: `internal/handler/web_search_provider.go`

响应：200 `{"success":true,"data":[...]}`

```bash
curl $BASE/api/v1/web-search-providers/types -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/web-search-providers/test

用途：原始凭证测试（不落库）。权限：Admin+。请求体：`provider`（`binding:"required"`）、`parameters`（可选）。

响应：200 `{"success":bool,"error"}`

```bash
curl -X POST $BASE/api/v1/web-search-providers/test -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"provider":"tavily","parameters":{"api_key":"tvly-..."}}'
```

### POST /api/v1/web-search-providers

用途：创建提供方配置。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是（`binding:"required"`） | 名称 |
| `provider` | string | 是（`binding:"required"`） | 类型（bing/tavily/google…） |
| `description` | string | 否 | 描述 |
| `parameters` | object | 否 | 参数（api_key 建议走 credentials 子资源） |
| `is_default` | bool | 否 | 默认提供方 |

响应：201 `{"success":true,"data":{WebSearchProviderResponse}}`

```bash
curl -X POST $BASE/api/v1/web-search-providers -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"tavily-main","provider":"tavily"}'
```

### GET /api/v1/web-search-providers

用途：提供方列表。权限：Viewer+。响应：200 `{"success":true,"data":[...]}`

```bash
curl $BASE/api/v1/web-search-providers -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/web-search-providers/:id

用途：详情。权限：Viewer+。响应：200 `{"success":true,"data":{...}}`

```bash
curl $BASE/api/v1/web-search-providers/wsp-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/web-search-providers/:id

用途：更新（空字段保留原值；APIKey 保留）。权限：Admin+。请求体：`name/description/parameters/is_default`（均可选）。

响应：200 `{"success":true,"data":{...}}`

```bash
curl -X PUT $BASE/api/v1/web-search-providers/wsp-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"is_default":true}'
```

### DELETE /api/v1/web-search-providers/:id

用途：删除。权限：Admin+。响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/web-search-providers/wsp-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/web-search-providers/:id/credentials

用途：设置 API key（`{"api_key":"..."}`，省略时返回状态）。权限：Admin+。Handler: `internal/handler/web_search_provider_credentials.go`

响应：200 `{"success":true,"data":{"fields":{"api_key":{"configured":bool}}}}`

```bash
curl -X PUT $BASE/api/v1/web-search-providers/wsp-1/credentials -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"api_key":"tvly-..."}'
```

### DELETE /api/v1/web-search-providers/:id/credentials/:field

用途：删除凭证字段（`field` 仅 `api_key`）。权限：Admin+。响应：204。

```bash
curl -X DELETE $BASE/api/v1/web-search-providers/wsp-1/credentials/api_key -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/web-search-providers/:id/test

用途：测试已保存提供方。权限：Admin+。响应：200 `{"success":bool,"error"}`

```bash
curl -X POST $BASE/api/v1/web-search-providers/wsp-1/test -H "Authorization: Bearer $TOKEN"
```

## 数据源（/api/v1/datasource）

外部内容连接器（Feishu/Notion/语雀等），同步任务会写入 KB。Handler: `internal/handler/datasource.go`。本组多数响应为原始对象/数组（无 `success` 包装）。

### GET /api/v1/datasource/types

用途：可用连接器目录。权限：Viewer+。

响应：200 `[{type,name,description,icon,priority,auth_type,capabilities}]`

```bash
curl $BASE/api/v1/datasource/types -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/datasource/validate-credentials

用途：校验原始凭证（“测试连接”按钮，不落库）。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `type` | string | 是（`binding:"required"`） | 连接器类型 |
| `credentials` | map | 是（`binding:"required"`） | 凭证 |

响应：200 `{"status":"connected"}`；失败 400 `{"error":"..."}`

```bash
curl -X POST $BASE/api/v1/datasource/validate-credentials -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"type":"notion","credentials":{"token":"secret"}}'
```

### POST /api/v1/datasource

用途：创建数据源。权限：Admin+。请求体（`types.DataSource`）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `knowledge_base_id` | string | 是 | 目标 KB（须归属本空间） |
| `name` | string | 是 | 名称 |
| `type` | string | 是 | 连接器类型 |
| `config` | object | 是 | 凭证（加密存储）+资源选择+设置 |
| `sync_schedule` | string | 否 | cron 表达式 |
| `sync_mode` | string | 否 | `incremental`（默认）/`full` |
| `conflict_strategy` | string | 否 | `overwrite`（默认）/`skip` |
| `sync_deletions` | bool | 否 | 默认 true |
| `sync_log_retention_days` | int | 否 | 默认 30 |

响应：201 `DataSourceResponse`（凭证剥离，见 `internal/handler/dto/datasource.go`）。

```bash
curl -X POST $BASE/api/v1/datasource -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"knowledge_base_id":"kb-1","name":"notion 同步","type":"notion","config":{}}'
```

### GET /api/v1/datasource

用途：数据源列表。权限：Viewer+。查询参数：`kb_id`（必填）。

响应：200 `[DataSourceResponse]`

```bash
curl "$BASE/api/v1/datasource?kb_id=kb-1" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/datasource/:id

用途：详情。权限：Viewer+。响应：200 `DataSourceResponse`；404 `{"error":"data source not found"}`

```bash
curl $BASE/api/v1/datasource/ds-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/datasource/:id

用途：更新（`id/tenant_id/knowledge_base_id` 锁定为原值）。权限：Admin+。请求体同创建。

响应：200 `DataSourceResponse`

```bash
curl -X PUT $BASE/api/v1/datasource/ds-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"notion 同步 v2","type":"notion","knowledge_base_id":"kb-1","config":{}}'
```

### DELETE /api/v1/datasource/:id

用途：删除。权限：Admin+。响应：204。

```bash
curl -X DELETE $BASE/api/v1/datasource/ds-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/datasource/:id/credentials

用途：整体替换凭证（数据源凭证为“单一逻辑字段 `credentials`”的原子 map）。权限：Admin+。请求体：`{"credentials":{...}}`（非空 map 必填）。Handler: `internal/handler/datasource_credentials.go`

响应：200 `{"success":true,"data":{"fields":{"credentials":{"configured":bool}}}}`

```bash
curl -X PUT $BASE/api/v1/datasource/ds-1/credentials -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"credentials":{"token":"secret"}}'
```

### DELETE /api/v1/datasource/:id/credentials/:field

用途：清空凭证（`field` 必须为 `credentials`）。权限：Admin+。响应：204。

```bash
curl -X DELETE $BASE/api/v1/datasource/ds-1/credentials/credentials -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/datasource/:id/validate

用途：校验已保存数据源连接。权限：Admin+。响应：200 `{"status":"connected"}`

```bash
curl -X POST $BASE/api/v1/datasource/ds-1/validate -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/datasource/:id/resources

用途：浏览外部资源树（懒加载）。权限：Admin+。查询参数：`parent_id`（可选，空=顶层）。

响应：200 `[{external_id,name,type,description,url,modified_at,parent_id,has_children,metadata}]`

```bash
curl "$BASE/api/v1/datasource/ds-1/resources?parent_id=" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/datasource/:id/resource-ancestors

用途：解析资源祖先链（选择器展开）。权限：Admin+。请求体：`{"resource_ids":["..."]}`（必填）。

响应：200 `{"ancestors":[...]}`

```bash
curl -X POST $BASE/api/v1/datasource/ds-1/resource-ancestors -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"resource_ids":["page-1"]}'
```

### POST /api/v1/datasource/:id/sync

用途：手动触发同步。权限：Admin+。响应：200 `SyncLog`（`id,status,started_at,items_total,items_created,items_updated,items_deleted,items_failed,...`）

```bash
curl -X POST $BASE/api/v1/datasource/ds-1/sync -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/datasource/webhooks/git/:id

用途：git_repo 数据源的 push 事件回调（GitLab / GitHub Webhook），鉴权通过后触发一次增量同步。**公开端点**（无 WeKnora JWT/API Key，走平台自有鉴权）：

- GitLab：请求头 `X-Gitlab-Token` 与共享密钥常量时间比对
- GitHub：请求头 `X-Hub-Signature-256` 对请求体 HMAC-SHA256 验签（不再接受 sha1）

共享密钥：数据源凭据 `credentials.webhook_secret` 优先，兼容 `settings.webhook_secret`，再回退环境变量 `GIT_REPO_WEBHOOK_SECRET`；均未配置或鉴权失败时 fail-closed 404（与不存在同一响应，避免枚举）。仅 `git_repo` 类型数据源可触发；载荷仓库 URL 须与数据源配置归一化匹配；非分支 push / 分支删除 / 仓库不匹配返回 200 `{"status":"ignored"}`（避免平台重试）。同步异步入队（`trigger=webhook`）。查库失败返回 500 以便平台重试。

```bash
curl -X POST $BASE/api/v1/datasource/webhooks/git/ds-1 \
  -H "X-Gitlab-Event: Push Hook" -H "X-Gitlab-Token: <secret>" \
  -H "Content-Type: application/json" \
  -d '{"ref":"refs/heads/main","after":"<sha>","repository":{"git_http_url":"http://gitlab.example.com/group/repo.git"}}'
# -> 200 {"status":"triggered"}
```

响应：200 `{"status":"triggered"|"ignored"}`；404（不存在 / 非 git_repo / 鉴权失败 / 未配密钥）；400（载荷异常）；409（数据源非 active）；500（查库或入队失败）。

### POST /api/v1/datasource/:id/pause 与 POST /api/v1/datasource/:id/resume

用途：暂停 / 恢复定时同步。权限：Admin+。

响应：200 `{"status":"paused"}` / `{"status":"active"}`

```bash
curl -X POST $BASE/api/v1/datasource/ds-1/pause -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/datasource/:id/logs

用途：同步日志列表。权限：Viewer+。查询参数：`limit`（默认 10，上限 100）、`offset`（默认 0）。

响应：200 `[SyncLog]`

```bash
curl "$BASE/api/v1/datasource/ds-1/logs?limit=10" -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/datasource/logs/:log_id

用途：单条同步日志。权限：Viewer+。响应：200 `SyncLog`；404 `{"error":"sync log not found"}`

```bash
curl $BASE/api/v1/datasource/logs/log-1 -H "Authorization: Bearer $TOKEN"
```
