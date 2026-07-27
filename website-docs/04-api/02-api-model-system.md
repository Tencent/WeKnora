# API 参考：模型、初始化与系统

路由注册：`internal/router/router.go` 的 `RegisterModelRoutes`、`RegisterInitializationRoutes`、`RegisterSystemRoutes`、`RegisterSystemAdminRoutes`、`RegisterEvaluationRoutes`、`RegisterWeKnoraCloudRoutes`。Handler：`internal/handler/model.go`、`internal/handler/model_credentials.go`、`internal/handler/initialization.go`、`internal/handler/system.go`、`internal/handler/evaluation.go`、`internal/handler/weknoracloud.go`。

## 模型（/api/v1/models）

API key：`manage_models` 或 full-access。

### GET /api/v1/models/providers

用途：模型厂商列表。权限：Viewer+。查询参数：`model_type`（可选：`chat/embedding/rerank/vllm/asr`）。Handler: `internal/handler/model.go`

响应：200 `{"success":true,"data":[{value,label,description,defaultUrls,modelTypes}]}`

```bash
curl "$BASE/api/v1/models/providers?model_type=chat" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/models

用途：创建模型。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是（`binding:"required"`） | 模型名 |
| `display_name` | string | 否 | 显示名 |
| `type` | string | 是（`binding:"required"`） | 模型类型 |
| `source` | string | 是（`binding:"required"`） | 来源（local/remote…） |
| `description` | string | 否 | 描述 |
| `parameters` | object | 是（`binding:"required"`） | 连接参数（base_url 等；密钥经 credentials 子资源管理） |

响应：201 `{"success":true,"data":{ModelResponse}}`（`id,name,type,source,parameters,is_default,is_builtin,status,credentials,...`）

```bash
curl -X POST $BASE/api/v1/models -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"gpt-4o-mini","type":"chat","source":"remote","parameters":{"base_url":"https://api.openai.com/v1"}}'
```

### GET /api/v1/models

用途：模型列表。权限：Viewer+。

响应：200 `{"success":true,"data":[ModelResponse]}`

```bash
curl $BASE/api/v1/models -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/models/:id

用途：模型详情。权限：Viewer+。

响应：200 `{"success":true,"data":{ModelResponse}}`

```bash
curl $BASE/api/v1/models/m-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/models/:id/debug

用途：调试已保存模型（发起真实上游调用，产生费用）。权限：Admin+。form-data 字段：`input`（≤64KB）、`options`（JSON 编码调试选项）、`documents`（JSON 数组，≤100 条）、`file`（可选）。

响应：200 `{"success":true,"data":{"ok",elapsed_ms,request,raw_response,observations,error}}`

```bash
curl -X POST $BASE/api/v1/models/m-1/debug -H "Authorization: Bearer $TOKEN" -F 'input=你好'
```

### PUT /api/v1/models/:id

用途：更新模型（内置模型由服务层限定 SystemAdmin）。权限：Admin+ 或 SystemAdmin（`AdminOrSystemAdmin`）。请求体：`name`、`display_name`（指针）、`description`、`parameters`（保留已存密钥）、`source`、`type`（均可选）。

响应：200 `{"success":true,"data":{ModelResponse}}`

```bash
curl -X PUT $BASE/api/v1/models/m-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"display_name":"GPT-4o mini"}'
```

### DELETE /api/v1/models/:id

用途：删除模型。权限：Admin+。

响应：200 `{"success":true,"message":"Model deleted"}`

```bash
curl -X DELETE $BASE/api/v1/models/m-1 -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/models/:id/credentials

用途：设置模型密钥（密钥不经主 PUT 传输）。权限：Admin+ 或 SystemAdmin。Handler: `internal/handler/model_credentials.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `api_key` | *string | 否 | 新 API Key |
| `app_secret` | *string | 否 | 新 App Secret（两者均省略时仅返回状态） |

响应：200 `{"success":true,"data":{"fields":{"api_key":{"configured":bool},"app_secret":{"configured":bool}}}}`

```bash
curl -X PUT $BASE/api/v1/models/m-1/credentials -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"api_key":"sk-..."}'
```

### DELETE /api/v1/models/:id/credentials/:field

用途：删除某个密钥字段（`api_key` 或 `app_secret`）。权限：Admin+ 或 SystemAdmin。

响应：204 No Content

```bash
curl -X DELETE $BASE/api/v1/models/m-1/credentials/api_key -H "Authorization: Bearer $TOKEN"
```

## WeKnoraCloud

Handler: `internal/handler/weknoracloud.go`。API key：`manage_models`/full。

### POST /api/v1/weknoracloud/credentials

用途：保存 WeKnoraCloud SaaS 凭证。权限：Admin+。请求体：`{"app_id":"...","app_secret":"..."}`（均 `binding:"required"`）。

响应：200 `{"success":true,"message":"凭证保存成功"}`

```bash
curl -X POST $BASE/api/v1/weknoracloud/credentials -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"app_id":"app","app_secret":"secret"}'
```

### GET /api/v1/models/weknoracloud/status

用途：WeKnoraCloud 就绪状态探测。权限：Viewer+。

响应：200 服务状态对象。

```bash
curl $BASE/api/v1/models/weknoracloud/status -H "Authorization: Bearer $TOKEN"
```

## 初始化（/api/v1/initialization）

Handler: `internal/handler/initialization.go`。KB 配置类：API key `manage_kbs`（写）/`retrieve`（读）；模型检测类：`manage_models`（均可 full-access）。

### GET /api/v1/initialization/config/:kbId

用途：读取 KB 当前模型/解析配置。权限：Viewer+，KB read。

响应：200 `{"success":true,"data":{"hasFiles",llm,embedding,rerank,multimodal,documentSplitting,nodeExtract,questionGeneration}}`

```bash
curl $BASE/api/v1/initialization/config/kb-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/initialization/initialize/:kbId

用途：初始化 KB 的模型与解析配置（首次配置向导）。权限：KB 创建者 OR Admin+，KB write。

主要字段（`InitializationRequest`）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `llm.source` / `llm.modelName` | string | 是 | LLM 来源与模型名 |
| `llm.baseUrl` / `llm.apiKey` | string | 否 | 连接参数 |
| `embedding.source` / `embedding.modelName` | string | 是 | Embedding 模型 |
| `embedding.baseUrl` / `embedding.apiKey` / `embedding.dimension` | — | 否 | 连接与维度 |
| `rerank.enabled` + `rerank.modelName/baseUrl/apiKey` | — | 否 | Rerank 配置 |
| `multimodal.enabled` + `multimodal.vlm.*` + `multimodal.storageType` + `multimodal.cos.*|minio.*` | — | 否 | 多模态与图床 |
| `documentSplitting.chunkSize` / `separators` | int / []string | 是 | 分块配置 |
| `documentSplitting.chunkOverlap` | int | 否 | 重叠 |
| `nodeExtract.*` | — | 否 | 图谱抽取（enabled/text/tags/nodes/relations） |
| `questionGeneration.*` | — | 否 | 问题生成（enabled/questionCount） |

响应：200 `{"success":true,"message":"知识库配置更新成功","data":{"models":[Model],"knowledge_base":{KnowledgeBase}}}`

```bash
curl -X POST $BASE/api/v1/initialization/initialize/kb-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"llm":{"source":"remote","modelName":"gpt-4o-mini"},"embedding":{"source":"remote","modelName":"text-embedding-3-small"},"documentSplitting":{"chunkSize":512,"separators":["\n\n"]}}'
```

### PUT /api/v1/initialization/config/:kbId

用途：更新 KB 模型/分块配置（`KBModelConfigRequest`：`llmModelId` 必填，`embeddingModelId`、`vlm_config`、`asr_config`、`documentSplitting.*`、`multimodal.enabled`、`storageProvider`、`storageBackendId`、`nodeExtract.*`、`questionGeneration.*` 可选）。权限：KB 创建者 OR Admin+，KB write。

响应：200 `{"success":true,"message":"配置更新成功"}`

```bash
curl -X PUT $BASE/api/v1/initialization/config/kb-1 -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"llmModelId":"m-1","embeddingModelId":"m-2"}'
```

### GET /api/v1/initialization/ollama/status

用途：Ollama 可用性探测。权限：Viewer+。

响应：200 `{"success":true,"data":{"available","version","baseUrl","error"}}`

```bash
curl $BASE/api/v1/initialization/ollama/status -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/initialization/ollama/models

用途：列出本地 Ollama 模型。权限：Viewer+。

响应：200 `{"success":true,"data":{"models":[...]}}`

```bash
curl $BASE/api/v1/initialization/ollama/models -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/initialization/ollama/models/check

用途：批量检查模型是否已存在。权限：Admin+。请求体：`{"models":["llama3"]}`（`binding:"required"`）。

响应：200 `{"success":true,"data":{"models":{"llama3":true}}}`

```bash
curl -X POST $BASE/api/v1/initialization/ollama/models/check -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"models":["llama3"]}'
```

### POST /api/v1/initialization/ollama/models/download

用途：拉取 Ollama 模型（异步任务）。权限：Admin+。请求体：`{"modelName":"llama3"}`（`binding:"required"`）。

响应：200 `{"success":true,"data":{"taskId","modelName","status","progress"}}`

```bash
curl -X POST $BASE/api/v1/initialization/ollama/models/download -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"modelName":"llama3"}'
```

### GET /api/v1/initialization/ollama/download/progress/:taskId

用途：下载任务进度。权限：Viewer+。

响应：200 `{"success":true,"data":{id,modelName,status,progress,message,startTime,endTime}}`

```bash
curl $BASE/api/v1/initialization/ollama/download/progress/task-1 -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/initialization/ollama/download/tasks

用途：全部下载任务列表。权限：Viewer+。

响应：200 `{"success":true,"data":[DownloadTask]}`

```bash
curl $BASE/api/v1/initialization/ollama/download/tasks -H "Authorization: Bearer $TOKEN"
```

### 模型连通性检测（均 POST，权限 Admin+）

请求体统一为 `ModelTestRequest`：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `source` | string | 否 | 默认 `remote` |
| `modelName` | string | 是 | 模型名 |
| `baseUrl` / `apiKey` / `appSecret` | string | 否 | 连接参数 |
| `provider` / `interfaceType` | string | 否 | 厂商/接口类型 |
| `dimension` | int | 否 | embedding 维度 |
| `customHeaders` / `extraConfig` | map | 否 | 扩展 |
| `modelId` | string | 否 | 从已存模型取密钥 |

| 端点 | 用途 | 响应 data |
| --- | --- | --- |
| `POST /api/v1/initialization/remote/check` | LLM 远程连通性 | `{available,message}` |
| `POST /api/v1/initialization/embedding/test` | Embedding 测试 | `{available,message,dimension}` |
| `POST /api/v1/initialization/rerank/check` | Rerank 测试 | `{available,message}` |
| `POST /api/v1/initialization/asr/check` | ASR 测试 | `{available,message}` |

```bash
curl -X POST $BASE/api/v1/initialization/remote/check -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"modelName":"gpt-4o-mini","baseUrl":"https://api.openai.com/v1","apiKey":"sk-..."}'
```

### POST /api/v1/initialization/multimodal/test

用途：多模态（VLM+图床）端到端测试。权限：Admin+。multipart 字段：`image`（必填）、`vlm_model`、`vlm_base_url`（必填）、`vlm_api_key`、`vlm_interface_type`、`storage_type`（`cos|minio`，必填）及对应 `cos_*`/`minio_*` 字段、`chunk_size`、`chunk_overlap`、`separators`。

响应：200 `{"success":true,"data":{"success","caption","ocr","processing_time"}}`

```bash
curl -X POST $BASE/api/v1/initialization/multimodal/test -H "Authorization: Bearer $TOKEN" \
  -F 'image=@demo.png' -F 'vlm_model=qwen-vl' -F 'vlm_base_url=http://x' -F 'storage_type=minio'
```

### POST /api/v1/initialization/extract/text-relation

用途：文本图谱抽取测试。权限：Admin+。请求体：`text`（必填，≤5000 字符）、`tags`（必填，至少一个）、`model_id`（必填）。

响应：200 `{"success":true,"data":{"nodes":[GraphNode],"relations":[GraphRelation]}}`

```bash
curl -X POST $BASE/api/v1/initialization/extract/text-relation -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"text":"小明在腾讯工作","tags":["人物","公司"],"model_id":"m-1"}'
```

### POST /api/v1/initialization/extract/fabri-tag

用途：生成示例标签。权限：Admin+。无请求体。

响应：200 `{"success":true,"data":{"tags":[...]}}`

```bash
curl -X POST $BASE/api/v1/initialization/extract/fabri-tag -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/initialization/extract/fabri-text

用途：按标签生成示例文本。权限：Admin+。请求体：`{"tags":[...],"model_id":"m-1"}`（model_id 必填）。

响应：200 `{"success":true,"data":{"text":"..."}}`

```bash
curl -X POST $BASE/api/v1/initialization/extract/fabri-text -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"model_id":"m-1","tags":["人物"]}'
```

## 系统信息（/api/v1/system）

Handler: `internal/handler/system.go`。API key：`manage_vector_stores`/full。本组响应使用 `{"code":0,"msg":"success","data":...}` 包装。

### GET /api/v1/system/info

用途：系统版本与引擎信息。权限：Viewer+。

响应：200 `{"code":0,"msg":"success","data":{version,edition,commit_id,build_time,go_version,keyword_index_engine,vector_store_engine,graph_database_engine,minio_enabled,db_version,started_at,uptime_seconds}}`

```bash
curl $BASE/api/v1/system/info -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/system/parser-engines

用途：解析引擎列表与 DocReader 连接状态。权限：Viewer+。

响应：200 `{"code":0,"msg":"success","data":[...],"docreader_addr","docreader_transport","connected"}`

```bash
curl $BASE/api/v1/system/parser-engines -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/system/parser-engines/check

用途：用给定配置探测解析引擎（`types.ParserEngineConfig` 请求体）。权限：Admin+。

响应：200，同上。

```bash
curl -X POST $BASE/api/v1/system/parser-engines/check -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{}'
```

### POST /api/v1/system/docreader/reconnect

用途：重连 DocReader。权限：Admin+。请求体：`{"addr":"host:port"}`（`binding:"required"`）。

响应：200 `{"code":0,"msg":"连接成功",...,"connected":true}`

```bash
curl -X POST $BASE/api/v1/system/docreader/reconnect -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"addr":"docreader:50051"}'
```

### GET /api/v1/system/storage-engine-status

用途：对象存储引擎可用性。权限：Viewer+。

响应：200 `{"code":0,"msg":"success","data":{"engines":[{name,allowed,available,description}],"allowed_providers":[...],"minio_env_available":bool}}`

```bash
curl $BASE/api/v1/system/storage-engine-status -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/system/storage-engine-check

用途：校验存储配置（SSRF 防护后探测）。权限：Admin+。请求体：`provider`（必填，`minio/cos/tos/s3/oss/ks3/obs`）+ 对应 `minio|cos|tos|s3|oss|ks3|obs` 配置对象。

响应：200 `{"code":0,"data":{"ok","message","bucket_created"}}`

```bash
curl -X POST $BASE/api/v1/system/storage-engine-check -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"provider":"minio","minio":{"endpoint":"minio:9000"}}'
```

## 系统管理（/api/v1/system/admin，SystemAdmin 专属）

组级挂载 `SystemAdmin()` 守卫（始终强制，不受 EnableRBAC 影响）；平台 API key 需对应 `system_*` capability。本组读取接口多返回原始行/数组（无包装）。Handler: `internal/handler/system.go`、`internal/handler/audit_log.go`。

### POST /api/v1/system/admin/promote

用途：授予 SystemAdmin。请求体：`user_id`（UUID，优先）或 `email`（二选一）。

响应：200 `UserInfo`（原始对象）。

```bash
curl -X POST $BASE/api/v1/system/admin/promote -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"email":"admin@ex.com"}'
```

### POST /api/v1/system/admin/revoke

用途：撤销 SystemAdmin。请求体：`{"user_id":"..."}`（`binding:"required"`）。

响应：200 `UserInfo`

```bash
curl -X POST $BASE/api/v1/system/admin/revoke -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"user_id":"u-1"}'
```

### GET /api/v1/system/admin/list

用途：SystemAdmin 列表。查询参数：`offset`（默认 0）、`limit`（默认 50，上限 200）。

响应：200 `{"total":N,"admins":[UserInfo]}`

```bash
curl $BASE/api/v1/system/admin/list -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/system/admin/users/reset-password

用途：重置用户密码。请求体：`email`（`binding:"required,email"`）、`new_password`（`binding:"required"`）。

响应：200 `{"message":"Password reset successfully"}`

```bash
curl -X POST $BASE/api/v1/system/admin/users/reset-password -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"email":"a@ex.com","new_password":"newpass1"}'
```

### GET /api/v1/system/admin/api-keys

用途：平台 API key 列表（掩码）。

响应：200 `{"success":true,"data":[{id,name,api_key,capabilities,expires_at_unix,...}]}`

```bash
curl $BASE/api/v1/system/admin/api-keys -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/system/admin/api-keys

用途：创建平台 API key（明文仅返回一次）。请求体：`name`（非空）、`capabilities`（`system_*` 列表，必填）、`expires_at_unix`（可选，须为未来时间）。

响应：201 `{"success":true,"data":{...,"api_key":"<明文>","token":"<明文>"}}`

```bash
curl -X POST $BASE/api/v1/system/admin/api-keys -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"ops","capabilities":["system_tenants_read"]}'
```

### DELETE /api/v1/system/admin/api-keys/:key_id

用途：删除平台 API key。

响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/system/admin/api-keys/3 -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/system/admin/settings 与 GET /api/v1/system/admin/settings/:key

用途：平台运行时设置列表 / 单项（平台 key 需 `system_settings_read|manage`）。

响应：200 `[SystemSetting]` / `SystemSetting`（原始，无包装；字段：`key,value,value_type,description,last_modified_by,last_modified_at`）。

```bash
curl $BASE/api/v1/system/admin/settings -H "Authorization: Bearer $TOKEN"
```

### PUT /api/v1/system/admin/settings/:key

用途：更新设置（平台 key 需 `system_settings_manage`）。请求体：`{"value":<任意 JSON，按注册表类型校验>}`（必填）。

响应：200 `SystemSetting`

```bash
curl -X PUT $BASE/api/v1/system/admin/settings/default_storage_quota -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"value":10737418240}'
```

### DELETE /api/v1/system/admin/settings/:key

用途：恢复设置默认值。

响应：200 `{"success":true}`

```bash
curl -X DELETE $BASE/api/v1/system/admin/settings/default_storage_quota -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/system/admin/runtime/queues

用途：asynq 队列深度与并发状态（Lite 模式返回 `available:false`；平台 key 需 `system_runtime_read|manage`）。

响应：200 `{"available",upstream_concurrency,parse_concurrency,wiki_concurrency,pools,queues,model_limiter_available,models,timestamp}`

```bash
curl $BASE/api/v1/system/admin/runtime/queues -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/system/admin/runtime/queues/:queue/tasks

用途：队列任务列表。查询参数：`state`（`pending/active/scheduled/retry/archived/completed`）、`cursor`、`page_size`（默认 20，上限 100）。

响应：200 `{"available","tasks":[RuntimeTaskInfo],"page_size","has_more","next_cursor"}`

```bash
curl "$BASE/api/v1/system/admin/runtime/queues/default/tasks?state=pending" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/system/admin/runtime/queues/:queue/tasks/:task_id/actions/:action

用途：任务操作（`action` ∈ `cancel/run_now/delete`；平台 key 需 `system_runtime_manage`）。

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/system/admin/runtime/queues/default/tasks/t-1/actions/cancel \
  -H "Authorization: Bearer $TOKEN"
```

### DELETE /api/v1/system/admin/runtime/queues/:queue/archived

用途：清空归档任务。

响应：200 `{"success":true,"deleted":N}`

```bash
curl -X DELETE $BASE/api/v1/system/admin/runtime/queues/default/archived -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/system/admin/tenants/apply-default-storage-quota

用途：把当前默认存储配额批量写到全部空间（平台 key 需 `system_tenants_manage`）。无请求体。

响应：200 `{"affected":N,"quota_bytes":N,"quota_gb":N}`

```bash
curl -X POST $BASE/api/v1/system/admin/tenants/apply-default-storage-quota -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/system/admin/audit-log

用途：平台级审计日志（tenant_id=0 行；平台 key 需 `system_audit_read`）。查询参数同空间审计（`after_id/limit/action/outcome/actor`）。Handler: `internal/handler/audit_log.go`

响应：200 `{"success":true,"data":[AuditLog],"next_cursor":N}`

```bash
curl $BASE/api/v1/system/admin/audit-log -H "Authorization: Bearer $TOKEN"
```

## 评估（/api/v1/evaluation）

Handler: `internal/handler/evaluation.go`。API key：`run_evaluations`/full。

### POST /api/v1/evaluation

用途：发起评估任务（驱动 LLM 调用，产生费用）。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `dataset_id` | string | 否 | 数据集 ID |
| `knowledge_base_id` | string | 否 | 目标 KB |
| `chat_id` | string | 否 | 对话模型 ID |
| `rerank_id` | string | 否 | Rerank 模型 ID |

响应：200 `{"success":true,"data":{评估任务}}`

```bash
curl -X POST $BASE/api/v1/evaluation -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"knowledge_base_id":"kb-1","chat_id":"m-1"}'
```

### GET /api/v1/evaluation

用途：查询评估结果。权限：Viewer+。查询参数：`task_id`（必填）。

响应：200 `{"success":true,"data":{评估结果}}`

```bash
curl "$BASE/api/v1/evaluation?task_id=task-1" -H "Authorization: Bearer $TOKEN"
```
