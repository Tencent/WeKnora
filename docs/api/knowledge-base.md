# 知识库管理 API

[返回目录](./README.md)

**字段说明（知识库对象）**

- 知识库类型 `type` 为 `document`（文档）或 `faq`（FAQ），默认 `document`。
- JSON 中对象存储相关字段：**`storage_config`** 为序列化字段名（对应数据库列 `cos_config`，兼容旧数据）。旧客户端若仍发送或接收 `cos_config`，服务端会兼容解析；新集成请使用 **`storage_config`**。
- **`storage_provider_config`** 为新版存储提供者选择（如 `{"provider": "local"}`），与租户级存储引擎凭证配合使用；无配置时可为 `null`。


| 方法   | 路径                                 | 描述                     |
| ------ | ------------------------------------ | ------------------------ |
| POST   | `/knowledge-bases`                   | 创建知识库               |
| GET    | `/knowledge-bases`                   | 获取知识库列表           |
| GET    | `/knowledge-bases/:id`               | 获取知识库详情           |
| PUT    | `/knowledge-bases/:id`               | 更新知识库               |
| DELETE | `/knowledge-bases/:id`               | 删除知识库               |
| POST   | `/knowledge-bases/copy`              | 拷贝知识库               |
| GET    | `/knowledge-bases/copy/progress/:task_id` | 获取拷贝进度      |
| GET    | `/knowledge-bases/:id/hybrid-search` | 混合搜索（向量+关键词）  |
| PUT    | `/knowledge-bases/:id/pin`           | 置顶/取消置顶知识库      |
| GET    | `/knowledge-bases/:id/move-targets`  | 获取可迁移目标知识库列表 |

## POST `/knowledge-bases` - 创建知识库

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
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

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```json
{
    "data": [
        {
            "id": "kb-00000001",
            "name": "Default Knowledge Base",
            "description": "System Default Knowledge Base",
            "type": "document",
            "is_temporary": false,
            "tenant_id": 1,
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
            "question_generation_config": null,
            "is_pinned": false,
            "pinned_at": null,
            "knowledge_count": 12,
            "chunk_count": 340,
            "processing_count": 0,
            "created_at": "2025-08-11T20:10:41.817794+08:00",
            "updated_at": "2025-08-12T11:23:00.593097+08:00",
            "deleted_at": null
        }
    ],
    "success": true
}
```

## GET `/knowledge-bases/:id` - 获取知识库详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```json
{
    "data": {
        "id": "kb-00000001",
        "name": "Default Knowledge Base",
        "description": "System Default Knowledge Base",
        "type": "document",
        "is_temporary": false,
        "tenant_id": 1,
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
        "extract_config": {
            "enabled": false,
            "text": "",
            "tags": [],
            "nodes": [],
            "relations": []
        },
        "faq_config": null,
        "question_generation_config": null,
        "is_pinned": false,
        "pinned_at": null,
        "knowledge_count": 12,
        "chunk_count": 340,
        "processing_count": 0,
        "created_at": "2025-08-11T20:10:41.817794+08:00",
        "updated_at": "2025-08-12T11:23:00.593097+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## PUT `/knowledge-bases/:id` - 更新知识库

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/b5829e4a-3845-4624-a7fb-ea3b35e843b0' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
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

**响应**:

```json
{
    "data": {
        "id": "b5829e4a-3845-4624-a7fb-ea3b35e843b0",
        "name": "weknora new",
        "description": "weknora description new",
        "type": "document",
        "is_temporary": false,
        "tenant_id": 1,
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
        "question_generation_config": null,
        "is_pinned": false,
        "pinned_at": null,
        "knowledge_count": 3,
        "chunk_count": 48,
        "processing_count": 1,
        "created_at": "2025-08-12T11:30:09.206238+08:00",
        "updated_at": "2025-08-12T11:36:09.083577609+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## DELETE `/knowledge-bases/:id` - 删除知识库

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/b5829e4a-3845-4624-a7fb-ea3b35e843b0' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```json
{
    "message": "Knowledge base deleted successfully",
    "success": true
}
```

## POST `/knowledge-bases/copy` - 拷贝知识库

异步拷贝一个知识库，包括知识库配置和所有知识内容。返回任务ID用于查询拷贝进度。

**请求参数**:
- `source_id`: 源知识库ID（必填）
- `name`: 新知识库名称（可选，默认使用原名称加"(副本)"后缀）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/copy' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "source_id": "kb-00000001",
    "name": "知识库副本"
}'
```

**响应**:

```json
{
    "data": {
        "task_id": "task-copy-00000001",
        "target_id": "kb-00000002"
    },
    "success": true
}
```

## GET `/knowledge-bases/copy/progress/:task_id` - 获取拷贝进度

查询知识库拷贝任务的执行进度。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/copy/progress/task-copy-00000001' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "task_id": "task-copy-00000001",
        "status": "completed",
        "total": 10,
        "finished": 10,
        "source_id": "kb-00000001",
        "target_id": "kb-00000002"
    },
    "success": true
}
```

注：`status` 可能的值为 `pending`、`processing`、`completed`、`failed`。

## GET `/knowledge-bases/:id/hybrid-search` - 混合搜索

执行向量搜索和关键词搜索的混合检索。

**注意**：此接口使用 GET 方法但需要 JSON 请求体。

**请求参数**：
- `query_text`: 搜索查询文本（必填）
- `vector_threshold`: 向量相似度阈值（0-1，可选）
- `keyword_threshold`: 关键词匹配阈值（可选）
- `match_count`: 返回结果数量（可选）
- `disable_keywords_match`: 是否禁用关键词匹配（可选）
- `disable_vector_match`: 是否禁用向量匹配（可选）

**请求**:

```curl
curl --location --request GET 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/hybrid-search' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
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

## PUT `/knowledge-bases/:id/pin` - 置顶/取消置顶知识库

切换知识库的置顶状态。无需请求体，每次调用会自动切换当前置顶状态。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/pin' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "kb-00000001",
        "name": "Default Knowledge Base",
        "description": "System Default Knowledge Base",
        "type": "document",
        "is_temporary": false,
        "tenant_id": 1,
        "chunking_config": {
            "chunk_size": 1000,
            "chunk_overlap": 200,
            "separators": ["\n\n", "\n", "。", "！", "？", ";", "；"],
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
        "question_generation_config": null,
        "is_pinned": true,
        "pinned_at": "2025-08-12T15:00:00.000000+08:00",
        "knowledge_count": 12,
        "chunk_count": 340,
        "processing_count": 0,
        "created_at": "2025-08-11T20:10:41.817794+08:00",
        "updated_at": "2025-08-12T15:00:00.000000+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## GET `/knowledge-bases/:id/move-targets` - 获取可迁移目标知识库列表

获取当前知识库可以迁移知识到的目标知识库列表。返回结果会排除当前知识库本身。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/move-targets' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
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
