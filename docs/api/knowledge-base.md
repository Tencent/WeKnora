# 知识库管理 API

[返回目录](./README.md)

**字段说明（知识库对象）**

- 知识库类型 `type` 为 `document`（文档）或 `faq`（FAQ），默认 `document`。
- JSON 中对象存储相关字段：**`storage_config`** 为序列化字段名（对应数据库列 `cos_config`，兼容旧数据）。旧客户端若仍发送或接收 `cos_config`，服务端会兼容解析；新集成请使用 **`storage_config`**。
- **`storage_provider_config`** 为新版存储提供者选择（如 `{"provider": "local"}`），与租户级存储引擎凭证配合使用；无配置时可为 `null`。
- 嵌套配置对象：`chunking_config`、`image_processing_config`、`vlm_config`、`asr_config`、`extract_config`、`faq_config`、`question_generation_config`。其中 `extract_config`、`faq_config`、`question_generation_config` 允许为 `null`。

| 方法   | 路径                                      | 描述                     |
| ------ | ----------------------------------------- | ------------------------ |
| POST   | `/knowledge-bases`                        | 创建知识库               |
| GET    | `/knowledge-bases`                        | 获取知识库列表           |
| GET    | `/knowledge-bases/:id`                    | 获取知识库详情           |
| PUT    | `/knowledge-bases/:id`                    | 更新知识库               |
| DELETE | `/knowledge-bases/:id`                    | 删除知识库               |
| PUT    | `/knowledge-bases/:id/pin`                | 置顶/取消置顶知识库      |
| GET    | `/knowledge-bases/:id/hybrid-search`      | 混合搜索（向量+关键词）  |
| POST   | `/knowledge-bases/copy`                   | 拷贝知识库（异步任务）   |
| GET    | `/knowledge-bases/copy/progress/:task_id` | 获取拷贝进度             |
| GET    | `/knowledge-bases/:id/move-targets`       | 获取可迁移目标知识库列表 |

## POST `/knowledge-bases` - 创建知识库

**参数说明（请求体）**:

| 字段                          | 类型    | 必填 | 说明                                                            |
| ----------------------------- | ------- | ---- | --------------------------------------------------------------- |
| name                          | string  | 是   | 知识库名称                                                      |
| description                   | string  | 否   | 知识库描述                                                      |
| type                          | string  | 否   | 知识库类型：`document`（默认）或 `faq`                          |
| is_temporary                  | boolean | 否   | 是否为临时知识库（默认 `false`，临时库通常不在 UI 列表中显示）  |
| chunking_config               | object  | 否   | 分块配置（见下方示例）                                          |
| image_processing_config       | object  | 否   | 图片处理配置                                                    |
| embedding_model_id            | string  | 否   | Embedding 模型 ID                                               |
| summary_model_id              | string  | 否   | 摘要模型 ID                                                     |
| vlm_config                    | object  | 否   | VLM（视觉模型）配置                                             |
| asr_config                    | object  | 否   | ASR（语音识别）配置                                             |
| storage_provider_config       | object  | 否   | 存储提供者选择，如 `{"provider": "local"}`                      |
| storage_config                | object  | 否   | 旧版 COS 存储凭证（兼容字段，新集成留空即可）                   |
| extract_config                | object  | 否   | 图谱抽取配置；`enabled=true` 时需提供 `text`/`tags`/`nodes`/`relations` |
| faq_config                    | object  | 否   | FAQ 配置（仅 FAQ 类型知识库需要）                               |
| question_generation_config    | object  | 否   | 问题生成配置                                                    |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "name": "weknora",
    "description": "weknora description",
    "type": "document",
    "is_temporary": false,
    "chunking_config": {
        "chunk_size": 1000,
        "chunk_overlap": 200,
        "separators": [
            "."
        ],
        "enable_multimodal": true,
        "parser_engine_rules": [
            {
                "file_types": [".pdf", ".docx"],
                "engine": "builtin"
            }
        ],
        "enable_parent_child": false,
        "parent_chunk_size": 4096,
        "child_chunk_size": 384
    },
    "image_processing_config": {
        "model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e"
    },
    "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
    "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
    "vlm_config": {
        "enabled": true,
        "model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e"
    },
    "asr_config": {
        "enabled": false,
        "model_id": "",
        "language": ""
    },
    "storage_provider_config": {
        "provider": "local"
    },
    "storage_config": {
        "secret_id": "",
        "secret_key": "",
        "region": "",
        "bucket_name": "",
        "app_id": "",
        "path_prefix": ""
    },
    "extract_config": null,
    "faq_config": null,
    "question_generation_config": {
        "enabled": false,
        "question_count": 3
    }
}'
```

**响应**:

```json
{
    "data": {
        "id": "b5829e4a-3845-4624-a7fb-ea3b35e843b0",
        "name": "weknora",
        "description": "weknora description",
        "type": "document",
        "is_temporary": false,
        "tenant_id": 1,
        "chunking_config": {
            "chunk_size": 1000,
            "chunk_overlap": 200,
            "separators": [
                "."
            ],
            "enable_multimodal": true,
            "parser_engine_rules": [
                {
                    "file_types": [".pdf", ".docx"],
                    "engine": "builtin"
                }
            ],
            "enable_parent_child": false,
            "parent_chunk_size": 4096,
            "child_chunk_size": 384
        },
        "image_processing_config": {
            "model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e"
        },
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "vlm_config": {
            "enabled": true,
            "model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e"
        },
        "asr_config": {
            "enabled": false,
            "model_id": "",
            "language": ""
        },
        "storage_provider_config": {
            "provider": "local"
        },
        "storage_config": {
            "secret_id": "",
            "secret_key": "",
            "region": "",
            "bucket_name": "",
            "app_id": "",
            "path_prefix": ""
        },
        "extract_config": null,
        "faq_config": null,
        "question_generation_config": {
            "enabled": false,
            "question_count": 3
        },
        "is_pinned": false,
        "pinned_at": null,
        "knowledge_count": 0,
        "chunk_count": 0,
        "processing_count": 0,
        "created_at": "2025-08-12T11:30:09.206238645+08:00",
        "updated_at": "2025-08-12T11:30:09.206238854+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## GET `/knowledge-bases` - 获取知识库列表

返回当前租户拥有的全部知识库。当传入 `agent_id` 时，校验调用者对该共享智能体的访问权限后，返回该智能体配置可见的知识库范围（用于 `@` 提及）。

**Query 参数**:

| 字段     | 类型   | 必填 | 说明                                                       |
| -------- | ------ | ---- | ---------------------------------------------------------- |
| agent_id | string | 否   | 共享智能体 ID；传入时按智能体配置（`all` / `selected` / `none`）过滤可见知识库 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**: `data` 为数组，每个元素的字段结构同 `POST /knowledge-bases` 响应，并额外携带 `knowledge_count` / `chunk_count` / `processing_count` / `share_count` / `is_pinned` / `pinned_at` 这些聚合与状态字段。

## GET `/knowledge-bases/:id` - 获取知识库详情

根据 ID 获取知识库详情。当通过共享智能体访问时，可传 `agent_id` 进行权限校验；此时返回对象会附加 `my_permission` 字段以指示当前用户对该知识库的角色（如 `viewer`）。

**路径参数**:

| 字段 | 类型   | 说明      |
| ---- | ------ | --------- |
| id   | string | 知识库 ID |

**Query 参数**:

| 字段     | 类型   | 必填 | 说明                                       |
| -------- | ------ | ---- | ------------------------------------------ |
| agent_id | string | 否   | 共享智能体 ID（用于校验该智能体是否有权访问） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**: 字段结构同 `POST /knowledge-bases` 响应，并附 `is_pinned` / `pinned_at` / `knowledge_count` / `chunk_count` / `processing_count` 状态字段。通过共享智能体访问时还会附加 `my_permission`。

## PUT `/knowledge-bases/:id` - 更新知识库

仅知识库 owner（admin）或具备 `editor` 权限的用户可调用。注意：**`vector_store_id` 在创建后不可修改**，更新接口不接收该字段。

**路径参数**:

| 字段 | 类型   | 说明      |
| ---- | ------ | --------- |
| id   | string | 知识库 ID |

**参数说明（请求体）**:

| 字段        | 类型   | 必填 | 说明                                                          |
| ----------- | ------ | ---- | ------------------------------------------------------------- |
| name        | string | 是   | 知识库名称                                                    |
| description | string | 否   | 知识库描述                                                    |
| config      | object | 否   | 更新配置；包含 `chunking_config` / `image_processing_config` / `faq_config` / `wiki_config` / `indexing_strategy` |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/b5829e4a-3845-4624-a7fb-ea3b35e843b0' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "name": "weknora new",
    "description": "weknora description new",
    "config": {
        "chunking_config": {
            "chunk_size": 1000,
            "chunk_overlap": 200,
            "separators": [
                "\n\n",
                "\n",
                "。",
                "！",
                "？",
                ";",
                "；"
            ],
            "enable_multimodal": true,
            "parser_engine_rules": [
                {
                    "file_types": [".md", ".txt"],
                    "engine": "builtin"
                }
            ],
            "enable_parent_child": true,
            "parent_chunk_size": 4096,
            "child_chunk_size": 384
        },
        "image_processing_config": {
            "model_id": ""
        }
    }
}'
```

**响应**: 字段结构同 `POST /knowledge-bases` 响应（返回更新后的完整知识库对象）。

## DELETE `/knowledge-bases/:id` - 删除知识库

仅知识库 owner（与 owning tenant 匹配的 admin）可调用，删除将级联清理知识库下所有知识与切片。

**路径参数**:

| 字段 | 类型   | 说明      |
| ---- | ------ | --------- |
| id   | string | 知识库 ID |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/b5829e4a-3845-4624-a7fb-ea3b35e843b0' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "message": "Knowledge base deleted successfully",
    "success": true
}
```

## PUT `/knowledge-bases/:id/pin` - 置顶/取消置顶知识库

切换知识库的置顶状态。无需请求体，每次调用会自动反转当前 `is_pinned`。置顶时会同步写入 `pinned_at` 时间戳。

**路径参数**:

| 字段 | 类型   | 说明      |
| ---- | ------ | --------- |
| id   | string | 知识库 ID |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/pin' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**: 字段结构同 `POST /knowledge-bases` 响应，本接口操作后 `is_pinned` 翻转、`pinned_at` 同步更新。

## GET `/knowledge-bases/:id/hybrid-search` - 混合搜索

在指定知识库内执行向量召回 + 关键词召回的混合检索。

**注意**：此接口使用 `GET` 方法但 **需要 JSON 请求体**（`SearchParams`），并非通过 query string 传参。

**路径参数**:

| 字段 | 类型   | 说明      |
| ---- | ------ | --------- |
| id   | string | 知识库 ID |

**参数说明（请求体）**:

| 字段                     | 类型     | 必填 | 说明                                                             |
| ------------------------ | -------- | ---- | ---------------------------------------------------------------- |
| query_text               | string   | 是   | 查询文本                                                         |
| vector_threshold         | number   | 否   | 向量相似度阈值（0-1）                                            |
| keyword_threshold        | number   | 否   | 关键词匹配阈值                                                   |
| match_count              | integer  | 否   | 返回结果数量上限                                                 |
| disable_keywords_match   | boolean  | 否   | 关闭关键词召回                                                   |
| disable_vector_match     | boolean  | 否   | 关闭向量召回                                                     |
| knowledge_ids            | string[] | 否   | 仅在指定的知识 ID 范围内召回                                     |
| tag_ids                  | string[] | 否   | 标签过滤（FAQ 类型常用于优先级过滤）                             |
| only_recommended         | boolean  | 否   | 仅返回标记为推荐的内容                                           |
| knowledge_base_ids       | string[] | 否   | 跨知识库召回（需共享相同 embedding 模型），优先级高于路径中的 `:id` |
| skip_context_enrichment  | boolean  | 否   | 跳过父子片段/相邻片段的上下文补全（chat 流程使用）               |

**请求**:

```curl
curl --location --request GET 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/hybrid-search' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query_text": "如何使用知识库",
    "vector_threshold": 0.5,
    "match_count": 10
}'
```

**响应**:

```json
{
    "data": [
        {
            "id": "chunk-00000001",
            "content": "知识库是用于存储和检索知识的系统...",
            "knowledge_id": "knowledge-00000001",
            "chunk_index": 0,
            "knowledge_title": "知识库使用指南",
            "start_at": 0,
            "end_at": 500,
            "seq": 1,
            "score": 0.95,
            "chunk_type": "text",
            "image_info": "",
            "metadata": {},
            "knowledge_filename": "guide.pdf",
            "knowledge_source": "file"
        }
    ],
    "success": true
}
```

## POST `/knowledge-bases/copy` - 拷贝知识库

异步拷贝整个知识库（配置 + 全部知识内容）。请求会被入队到 Asynq 后台任务（队列 `default`，最多重试 3 次），并立即返回 `task_id` 供轮询进度。

**约束**：源知识库 `source_id` 必须属于调用者所在租户；若指定 `target_id`，目标知识库同样必须属于调用者租户，否则返回 `403 Forbidden`。

**参数说明（请求体）**:

| 字段       | 类型   | 必填 | 说明                                                          |
| ---------- | ------ | ---- | ------------------------------------------------------------- |
| source_id  | string | 是   | 源知识库 ID（必须属于当前租户）                               |
| target_id  | string | 否   | 目标知识库 ID（若复用已存在知识库；同样必须属于当前租户）     |
| task_id    | string | 否   | 自定义任务 ID；不传则由服务端生成（基于租户、源 ID、时间戳）  |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/copy' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "source_id": "kb-00000001"
}'
```

**响应**:

```json
{
    "data": {
        "task_id": "kb_clone_1_kb-00000001_1736582400",
        "source_id": "kb-00000001",
        "target_id": "",
        "message": "Knowledge base copy task started"
    },
    "success": true
}
```

## GET `/knowledge-bases/copy/progress/:task_id` - 获取拷贝进度

查询拷贝任务的当前状态与进度（数据由 worker 写入 Redis）。

**路径参数**:

| 字段    | 类型   | 说明                                              |
| ------- | ------ | ------------------------------------------------- |
| task_id | string | 由 `POST /knowledge-bases/copy` 返回的任务 ID     |

**响应字段（`data`）**:

| 字段       | 类型    | 说明                                                         |
| ---------- | ------- | ------------------------------------------------------------ |
| task_id    | string  | 任务 ID                                                      |
| source_id  | string  | 源知识库 ID                                                  |
| target_id  | string  | 目标知识库 ID（任务开始后填入）                              |
| status     | string  | `pending` / `processing` / `completed` / `failed`            |
| progress   | integer | 进度百分比 0–100                                             |
| total      | integer | 计划拷贝的知识总数                                           |
| processed  | integer | 已处理的知识数                                               |
| message    | string  | 当前状态描述                                                 |
| error      | string  | 失败时的错误信息                                             |
| created_at | integer | 任务创建时间（Unix 秒）                                      |
| updated_at | integer | 最后更新时间（Unix 秒）                                      |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/copy/progress/kb_clone_1_kb-00000001_1736582400' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "task_id": "kb_clone_1_kb-00000001_1736582400",
        "source_id": "kb-00000001",
        "target_id": "kb-00000002",
        "status": "completed",
        "progress": 100,
        "total": 10,
        "processed": 10,
        "message": "Task completed successfully",
        "error": "",
        "created_at": 1736582400,
        "updated_at": 1736582460
    },
    "success": true
}
```

## GET `/knowledge-bases/:id/move-targets` - 获取可迁移目标知识库列表

返回当前知识库的内容**可以迁移到**的目标知识库列表。筛选规则：

- 与源知识库 `type` 相同
- 与源知识库 `embedding_model_id` 相同
- 非临时知识库（`is_temporary = false`）
- 不包含源知识库自身
- 仅同租户的知识库

**路径参数**:

| 字段 | 类型   | 说明          |
| ---- | ------ | ------------- |
| id   | string | 源知识库 ID   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/move-targets' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "kb-00000002",
            "name": "技术文档知识库",
            "description": "技术文档相关知识",
            "type": "document",
            "is_temporary": false,
            "tenant_id": 1,
            "chunking_config": {
                "chunk_size": 1000,
                "chunk_overlap": 200,
                "separators": ["\n\n", "\n"],
                "enable_multimodal": true,
                "parser_engine_rules": [],
                "enable_parent_child": false,
                "parent_chunk_size": 4096,
                "child_chunk_size": 384
            },
            "image_processing_config": {
                "model_id": ""
            },
            "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "vlm_config": {
                "enabled": false,
                "model_id": ""
            },
            "asr_config": {
                "enabled": false,
                "model_id": "",
                "language": ""
            },
            "storage_provider_config": {
                "provider": "local"
            },
            "storage_config": {
                "secret_id": "",
                "secret_key": "",
                "region": "",
                "bucket_name": "",
                "app_id": "",
                "path_prefix": ""
            },
            "extract_config": null,
            "faq_config": null,
            "question_generation_config": null,
            "is_pinned": false,
            "pinned_at": null,
            "knowledge_count": 8,
            "chunk_count": 210,
            "processing_count": 0,
            "created_at": "2025-08-12T11:30:09.206238+08:00",
            "updated_at": "2025-08-12T11:30:09.206238+08:00",
            "deleted_at": null
        }
    ],
    "success": true
}
```
