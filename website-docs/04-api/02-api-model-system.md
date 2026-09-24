# API 参考：模型与初始化

管理模型、测试连接、初始化知识库，并发起评估任务。WeKnoraCloud 接口用于相关云服务接入。

系统信息与系统管理（`/system`、`/system/admin`）接口见[系统与平台管理](./02-api-system.md)。

## 模型（/api/v1/models）

API key：`manage_models` 或 full-access。

### GET /api/v1/models/providers

用途：厂商目录（前端据此动态渲染厂商下拉、图标、额外字段与模型选择）。权限：Viewer+。查询参数：`model_type`（可选：`chat/embedding/rerank/vllm/asr`，也接受 `KnowledgeQA` 等后端取值；未知取值返回 400），指定后只返回支持该类型的厂商及该类型的模型。Handler: `internal/handler/model_catalog.go`

响应：200 `{"success":true,"data":[ModelProviderDTO]}`，每项：

| 字段 | 说明 |
| --- | --- |
| `value` / `label` / `labels` / `description` / `descriptions` / `website` | 厂商 id、品牌名、按语言的名称与描述 |
| `icon` | `data:image/svg+xml;base64,...`，可直接用于 `<img src>` |
| `api` / `auth` / `requiresAuth` | 默认协议（`openai-completions` 等）、鉴权方式、是否需要密钥 |
| `defaultUrls` / `modelTypes` | 按模型类型的默认地址与支持的类型。`defaultUrls` 仅对 Admin+（或 full-access / `manage_tenant_settings` API key）返回，其他调用方为空 |
| `extraFields` | 厂商额外配置字段定义（`key,label,labels,type,required,default,placeholder,options,model_types,secret`），值存入 `parameters.extra_config` |
| `credentialLabels` | 部分模型类型下凭证输入框的名称与提示（如火山引擎、LKEAP 重排的 API Key 一栏实为 Access Key ID / SecretId） |
| `models` | 内置模型目录（`id,name,type,api,reasoning,input,context_window,max_output_tokens,dimension,thinking_levels,cost,source`），`source` 为该模型参数所依据的厂商文档链接 |
| `thinking` | 厂商级思考编码摘要（`format`、`levels`） |
| `order` | 列表排序值 |

```bash
curl "$BASE/api/v1/models/providers?model_type=chat" -H "Authorization: Bearer $TOKEN"
```

### GET|POST /api/v1/models/catalog/resolve

用途：按厂商、模型名、`base_url` 与 `extra_config` 解析有效接入配置（协议、思考等级、上下文），供模型编辑器实时展示。权限：Viewer+。

参数（GET 用查询参数，POST 用 JSON 请求体，字段相同）：`provider`（厂商 ID）、`model`、`base_url`、`model_type`（默认 `chat`）、`api`、`thinking_control`、`remote_model_name`，以及该厂商声明的非密钥额外字段（如 Azure 的 `api_version`）。POST 请求体还可带 `spec` 对象（与模型 `parameters.spec` 相同），用于预览单行目录覆盖。密钥类字段一律不接受。

响应：200 `{"success":true,"data":{provider,api,remote_model,cataloged,model,capabilities,base_url,url}}`，其中 `capabilities` 为 `{provider,api,cataloged,reasoning,thinking_levels,thinking_format,input,context_window,max_output_tokens,max_tokens_field}`。`base_url` 与 `url`（实际请求地址，仅自行计算地址的厂商返回，如 Azure）只对 Admin+（或 full-access / `manage_tenant_settings` API key）返回。无法解析时返回 400。同一 `capabilities` 结构也随远程对话/视觉模型的 `ModelResponse.capabilities` 返回。

```bash
curl "$BASE/api/v1/models/catalog/resolve?provider=deepseek&model=deepseek-v4-pro" -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/models

用途：创建模型。权限：Admin+。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是（`binding:"required"`） | 模型名 |
| `display_name` | string | 否 | 显示名 |
| `type` | string | 是（`binding:"required"`） | 模型类型：`KnowledgeQA` / `Embedding` / `Rerank` / `VLLM` / `ASR`（按原样保存，不接受 `chat` 等前端写法） |
| `source` | string | 是（`binding:"required"`） | 来源（`local` / `remote`） |
| `description` | string | 否 | 描述 |
| `parameters` | object | 是（`binding:"required"`） | 连接参数（`base_url`、`provider`、`extra_config`、`spec`、`context_window` 等，字段见[模型管理](../03-features/06-models.md#模型配置字段)）。创建时可直接带 `api_key` / `app_secret`，之后经 credentials 子资源修改 |

`parameters` 会按模型目录校验（未知协议、错误的 compat 键、非法思考等级返回 400），`base_url` 经过 SSRF 校验。

响应：201 `{"success":true,"data":{ModelResponse}}`（`id,name,type,source,parameters,is_default,is_builtin,status,credentials,capabilities,...`；响应不含密钥）

```bash
curl -X POST $BASE/api/v1/models -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"gpt-5.5","type":"KnowledgeQA","source":"remote","parameters":{"provider":"openai","base_url":"https://api.openai.com/v1","api_key":"sk-..."}}'
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

用途：调试已保存模型（发起真实上游调用，产生费用）。权限：Admin+。form-data 字段：`input`（≤64KB）、`options`（JSON 编码调试选项：`system_prompt`、`temperature`（0~2）、`top_p`、`max_tokens`（1~8192）、`thinking`、`reasoning_effort`（`off/auto/minimal/low/medium/high/xhigh/max`，设置后覆盖 `thinking`））、`documents`（JSON 数组，≤100 条）、`file`（可选）。

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

仍被当前空间的知识库、智能体或长期记忆引用时，响应为 HTTP 400；兼容 message 保留，同时 `error.code=2300`，`error.details` 给出具体对象和引用位置：

```json
{
  "success": false,
  "error": {
    "code": 2300,
    "message": "model is used by 2 knowledge base(s); reconfigure or remove those references before deleting",
    "details": {
      "knowledge_bases": [
        {"id": "kb-1", "name": "Product docs", "bindings": ["vlm_model"]},
        {"id": "kb-2", "name": "Engineering", "bindings": ["vlm_model"]}
      ],
      "agents": [],
      "long_term_memory": {"bindings": []},
      "knowledge_base_total": 2,
      "agent_total": 0
    }
  }
}
```

知识库绑定值：`embedding_model`、`summary_model`、`image_processing_model`、`vlm_model`、`asr_model`、`wiki_synthesis_model`、`auto_tag_model`；智能体绑定值：`chat_model`、`rerank_model`、`vlm_model`、`asr_model`、`query_understand_model`、`follow_up_model`；长期记忆绑定值：`embedding_model`、`extract_model`。详情包含对象 `id`、`name`、合并后的 `bindings`，以及 `knowledge_base_total` / `agent_total`。列表最多各 50 条，删除守卫以总数为准。

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

模型 `baseUrl` 仅对 KB 所属空间的 Admin+（或 full-access / `manage_tenant_settings` API key）返回。通过组织分享访问的空间只能看到凭证是否已配置（`credentials.*`），看不到来源空间的模型地址和存储桶信息。

响应：200 `{"success":true,"data":{"hasFiles",llm,embedding,rerank,multimodal,documentSplitting,nodeExtract,questionGeneration}}`

```bash
curl $BASE/api/v1/initialization/config/kb-1 -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/initialization/initialize/:kbId

用途：初始化 KB 的模型与解析配置（首次配置向导）。权限：KB 创建者 OR Admin+，KB write。

只有 KB 所属空间可以调用；通过组织分享获得编辑权限的空间会被拒绝（403）。KB 已绑定模型时，该接口会原地更新这些模型的配置，这一步需要与 `PUT /models/:id` 相同的权限（Admin+，或拥有 `manage_models` 能力的 API key），否则 403。

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

通过组织分享访问时，需要有效分享权限为 admin；editor 只能编辑内容，不能改设置（403）。存储绑定（`storageBackendId` / `storageProvider`）只有 KB 所属空间可以修改，其他空间提交与当前不同的值会返回 403。

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
| `dimension` / `supportsDimensionOverride` | int / bool | 否 | embedding 维度；是否在请求中指定维度 |
| `customHeaders` / `extraConfig` | map | 否 | 扩展 |
| `spec` | object | 否 | 单行目录覆盖，与模型 `parameters.spec` 相同 |
| `modelId` | string | 否 | 已存模型 ID：请求中缺失的密钥、`extraConfig` 与 `spec` 从该模型补齐 |

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

## 实现参考

路由注册：`internal/router/router.go` 调用 `RegisterModelRoutes`、`RegisterInitializationRoutes`、`RegisterEvaluationRoutes`、`RegisterWeKnoraCloudRoutes`（定义在 `internal/router/routes_infra.go`）。Handler：`internal/handler/model.go`、`internal/handler/model_catalog.go`、`internal/handler/model_credentials.go`、`internal/handler/initialization.go`、`internal/handler/evaluation.go`、`internal/handler/weknoracloud.go`。
