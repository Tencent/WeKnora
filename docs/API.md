# WeKnora API 文档

## 目录

- [概述](#概述)
- [基础信息](#基础信息)
- [认证机制](#认证机制)
- [错误处理](#错误处理)
- [API 概览](#api-概览)
- [API 详细说明](#api-详细说明)
  - [租户管理 API](#租户管理api)
  - [知识库管理 API](#知识库管理api)
  - [知识管理 API](#知识管理api)
  - [模型管理 API](#模型管理api)
  - [分块管理 API](#分块管理api)
  - [会话管理 API](#会话管理api)
  - [聊天功能 API](#聊天功能api)
  - [消息管理 API](#消息管理api)
  - [评估功能 API](#评估功能api)

## 概述

WeKnora 提供了一系列 RESTful API，用于创建和管理知识库、检索知识，以及进行基于知识的问答。本文档详细描述了这些 API 的使用方式。

## 基础信息

- **基础 URL**: `/api/v1`
- **响应格式**: JSON
- **认证方式**: API Key

## 认证机制

所有 API 请求需要在 HTTP 请求头中包含 `X-API-Key` 进行身份认证：

```
X-API-Key: your_api_key
```

为便于问题追踪和调试，建议每个请求的 HTTP 请求头中添加 `X-Request-ID`：

```
X-Request-ID: unique_request_id
```

### 获取 API Key

获取 API Key 有以下方式：

**创建租户时获取**：通过 `POST /api/v1/tenants` 接口创建新租户时，响应中会自动返回生成的 API Key。

请妥善保管您的 API Key，避免泄露。API Key 代表您的账户身份，拥有完整的 API 访问权限。

## 错误处理

所有 API 使用标准的 HTTP 状态码表示请求状态，并返回统一的错误响应格式：

```json
{
  "success": false,
  "error": {
    "code": "错误代码",
    "message": "错误信息",
    "details": "错误详情"
  }
}
```

## API 概览

WeKnora API 按功能分为以下几类：

1. **租户管理**：创建和管理租户账户
2. **知识库管理**：创建、查询和管理知识库
3. **知识管理**：上传、检索和管理知识内容
4. **模型管理**：配置和管理各种AI模型
5. **分块管理**：管理知识的分块内容
6. **会话管理**：创建和管理对话会话
7. **聊天功能**：基于知识库进行问答
8. **消息管理**：获取和管理对话消息
9. **评估功能**：评估模型性能

## API 详细说明

以下是每个API的详细说明和示例。

### 租户管理API

| 方法   | 路径           | 描述                  |
| ------ | -------------- | --------------------- |
| POST   | `/tenants`     | 创建新租户            |
| GET    | `/tenants/:id` | 获取指定租户信息      |
| PUT    | `/tenants/:id` | 更新租户信息          |
| DELETE | `/tenants/:id` | 删除租户              |
| GET    | `/tenants`     | 获取租户列表          |

#### POST `/tenants` - 创建新租户

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants' \
--header 'Content-Type: application/json' \
--data '{
    "name": "weknora",
    "description": "weknora tenants",
    "business": "wechat",
    "retriever_engines": {
        "engines": [
            {
                "retriever_type": "keywords",
                "retriever_engine_type": "postgres"
            },
            {
                "retriever_type": "vector",
                "retriever_engine_type": "postgres"
            }
        ]
    }
}'
```

**响应**:

```json
{
    "data": {
        "id": 10000,
        "name": "weknora",
        "description": "weknora tenants",
        "api_key": "sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG",
        "status": "active",
        "retriever_engines": {
            "engines": [
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "keywords"
                },
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "vector"
                }
            ]
        },
        "business": "wechat",
        "storage_quota": 10737418240,
        "storage_used": 0,
        "created_at": "2025-08-11T20:37:28.396980093+08:00",
        "updated_at": "2025-08-11T20:37:28.396980301+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### GET `/tenants/:id` - 获取指定租户信息

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants/10000' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG'
```

**响应**:

```json
{
    "data": {
        "id": 10000,
        "name": "weknora",
        "description": "weknora tenants",
        "api_key": "sk-aaLRAgvCRJcmtiL2vLMeB1FB5UV0Q-qB7DlTE1pJ9KA93XZG",
        "status": "active",
        "retriever_engines": {
            "engines": [
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "keywords"
                },
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "vector"
                }
            ]
        },
        "business": "wechat",
        "storage_quota": 10737418240,
        "storage_used": 0,
        "created_at": "2025-08-11T20:37:28.39698+08:00",
        "updated_at": "2025-08-11T20:37:28.405693+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### PUT `/tenants/:id` - 更新租户信息

注意 API Key 会变更

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/tenants/10000' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-KREi84yPtahKxMtIMOW-Cxx2dxb9xROpUuDSpi3vbiC1QVDe' \
--data '{
    "name": "weknora new",
    "description": "weknora tenants new",
    "status": "active",
    "retriever_engines": {
        "engines": [
            {
                "retriever_engine_type": "postgres",
                "retriever_type": "keywords"
            },
            {
                "retriever_engine_type": "postgres",
                "retriever_type": "vector"
            }
        ]
    },
    "business": "wechat",
    "storage_quota": 10737418240
}'
```

**响应**:

```json
{
    "data": {
        "id": 10000,
        "name": "weknora new",
        "description": "weknora tenants new",
        "api_key": "sk-IKtd9JGV4-aPGQ6RiL8YJu9Vzb3-ae4lgFkjFJZmhvUn2mLu",
        "status": "active",
        "retriever_engines": {
            "engines": [
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "keywords"
                },
                {
                    "retriever_engine_type": "postgres",
                    "retriever_type": "vector"
                }
            ]
        },
        "business": "wechat",
        "storage_quota": 10737418240,
        "storage_used": 0,
        "created_at": "0001-01-01T00:00:00Z",
        "updated_at": "2025-08-11T20:49:02.13421034+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### DELETE `/tenants/:id` - 删除租户

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/tenants/10000' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-IKtd9JGV4-aPGQ6RiL8YJu9Vzb3-ae4lgFkjFJZmhvUn2mLu'
```

**响应**:

```json
{
    "message": "Tenant deleted successfully",
    "success": true
}
```

#### GET `/tenants` - 获取租户列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/tenants' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-An7_t_izCKFIJ4iht9Xjcjnj_MC48ILvwezEDki9ScfIa7KA'
```

**响应**:

```json
{
    "data": {
        "items": [
            {
                "id": 10002,
                "name": "weknora",
                "description": "weknora tenants",
                "api_key": "sk-An7_t_izCKFIJ4iht9Xjcjnj_MC48ILvwezEDki9ScfIa7KA",
                "status": "active",
                "retriever_engines": {
                    "engines": [
                        {
                            "retriever_engine_type": "postgres",
                            "retriever_type": "keywords"
                        },
                        {
                            "retriever_engine_type": "postgres",
                            "retriever_type": "vector"
                        }
                    ]
                },
                "business": "wechat",
                "storage_quota": 10737418240,
                "storage_used": 0,
                "created_at": "2025-08-11T20:52:58.05679+08:00",
                "updated_at": "2025-08-11T20:52:58.060495+08:00",
                "deleted_at": null
            }
        ]
    },
    "success": true
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 知识库管理API

| 方法   | 路径                                 | 描述                     |
| ------ | ------------------------------------ | ------------------------ |
| POST   | `/knowledge-bases`                   | 创建知识库               |
| GET    | `/knowledge-bases`                   | 获取知识库列表           |
| GET    | `/knowledge-bases/:id`               | 获取知识库详情           |
| PUT    | `/knowledge-bases/:id`               | 更新知识库               |
| DELETE | `/knowledge-bases/:id`               | 删除知识库               |
| GET    | `/knowledge-bases/:id/hybrid-search` | 混合搜索知识库内容       |
| POST   | `/knowledge-bases/copy`              | 拷贝知识库               |

#### POST `/knowledge-bases` - 创建知识库

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--data '{
    "name": "weknora",
    "description": "weknora description",
    "chunking_config": {
        "chunk_size": 1000,
        "chunk_overlap": 200,
        "separators": [
            "."
        ],
        "enable_multimodal": true
    },
    "image_processing_config": {
        "model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e"
    },
    "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
    "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
    "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
    "vlm_model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e",
    "vlm_config": {
        "model_name": "qwen2.5vl:3b",
        "interface_type": "ollama",
        "base_url": "",
        "api_key": ""
    },
    "cos_config": {
        "secret_id": "",
        "secret_key": "",
        "region": "",
        "bucket_name": "",
        "app_id": "",
        "path_prefix": ""
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
        "tenant_id": 1,
        "chunking_config": {
            "chunk_size": 1000,
            "chunk_overlap": 200,
            "separators": [
                "."
            ],
            "enable_multimodal": true
        },
        "image_processing_config": {
            "model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e"
        },
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
        "vlm_model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e",
        "vlm_config": {
            "model_name": "qwen2.5vl:3b",
            "base_url": "",
            "api_key": "",
            "interface_type": "ollama"
        },
        "cos_config": {
            "secret_id": "",
            "secret_key": "",
            "region": "",
            "bucket_name": "",
            "app_id": "",
            "path_prefix": ""
        },
        "created_at": "2025-08-12T11:30:09.206238645+08:00",
        "updated_at": "2025-08-12T11:30:09.206238854+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### GET `/knowledge-bases` - 获取知识库列表

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
                "enable_multimodal": true
            },
            "image_processing_config": {
                "model_id": ""
            },
            "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
            "vlm_model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e",
            "vlm_config": {
                "model_name": "qwen2.5vl:3b",
                "base_url": "http://host.docker.internal:11435/v1",
                "api_key": "",
                "interface_type": "ollama"
            },
            "cos_config": {
                "secret_id": "",
                "secret_key": "",
                "region": "",
                "bucket_name": "",
                "app_id": "",
                "path_prefix": ""
            },
            "created_at": "2025-08-11T20:10:41.817794+08:00",
            "updated_at": "2025-08-12T11:23:00.593097+08:00",
            "deleted_at": null
        }
    ],
    "success": true
}
```

#### GET `/knowledge-bases/:id` - 获取知识库详情

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
            "enable_multimodal": true
        },
        "image_processing_config": {
            "model_id": ""
        },
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
        "vlm_model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e",
        "vlm_config": {
            "model_name": "qwen2.5vl:3b",
            "base_url": "http://host.docker.internal:11435/v1",
            "api_key": "",
            "interface_type": "ollama"
        },
        "cos_config": {
            "secret_id": "",
            "secret_key": "",
            "region": "",
            "bucket_name": "",
            "app_id": "",
            "path_prefix": ""
        },
        "created_at": "2025-08-11T20:10:41.817794+08:00",
        "updated_at": "2025-08-12T11:23:00.593097+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### PUT `/knowledge-bases/:id` - 更新知识库

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
            "enable_multimodal": true
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
            "enable_multimodal": true
        },
        "image_processing_config": {
            "model_id": ""
        },
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
        "vlm_model_id": "f2083ad7-63e3-486d-a610-e6c56e58d72e",
        "vlm_config": {
            "model_name": "qwen2.5vl:3b",
            "base_url": "",
            "api_key": "",
            "interface_type": "ollama"
        },
        "cos_config": {
            "secret_id": "",
            "secret_key": "",
            "region": "",
            "bucket_name": "",
            "app_id": "",
            "path_prefix": ""
        },
        "created_at": "2025-08-12T11:30:09.206238+08:00",
        "updated_at": "2025-08-12T11:36:09.083577609+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### DELETE `/knowledge-bases/:id` - 删除知识库

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

#### GET `/knowledge-bases/:id/hybrid-search` - 混合搜索知识库内容

**请求**:

```curl
curl --location --request GET 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/hybrid-search' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--data '{
    "query_text": "彗星",
    "vector_threshold": 0.1,
    "keyword_threshold": 0.1,
    "match_count": 1
}'
```

**响应**:

```json
{
    "data": [
        {
            "id": "7d955251-3f79-4fd5-a6aa-02f81e044091",
            "content": "有几位后来xxxxx",
            "knowledge_id": "a6790b93-4700-4676-bd48-0d4804e1456b",
            "chunk_index": 3,
            "knowledge_title": "彗星.txt",
            "start_at": 2287,
            "end_at": 2760,
            "seq": 3,
            "score": 0.7402352891601821,
            "match_type": 2,
            "sub_chunk_id": null,
            "metadata": {},
            "chunk_type": "text",
            "parent_chunk_id": "",
            "image_info": "",
            "knowledge_filename": "彗星.txt",
            "knowledge_source": ""
        }
    ],
    "success": true
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 知识管理API

| 方法   | 路径                                  | 描述                     |
| ------ | ------------------------------------- | ------------------------ |
| POST   | `/knowledge-bases/:id/knowledge/file` | 从文件创建知识           |
| POST   | `/knowledge-bases/:id/knowledge/url`  | 从 URL 创建知识          |
| GET    | `/knowledge-bases/:id/knowledge`      | 获取知识库下的知识列表   |
| GET    | `/knowledge/:id`                      | 获取知识详情             |
| DELETE | `/knowledge/:id`                      | 删除知识                 |
| GET    | `/knowledge/:id/download`             | 下载知识文件             |
| PUT    | `/knowledge/:id`                      | 更新知识                 |
| PUT    | `/knowledge/image/:id/:chunk_id`      | 更新图像分块信息         |
| GET    | `/knowledge/batch`                    | 批量获取知识             |

#### POST `/knowledge-bases/:id/knowledge/file` - 从文件创建知识

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge/file' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--form 'file=@"/Users/xxxx/tests/彗星.txt"' \
--form 'enable_multimodel="true"'
```

**响应**:

```json
{
    "data": {
        "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "file",
        "title": "彗星.txt",
        "description": "",
        "source": "",
        "parse_status": "processing",
        "enable_status": "disabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "彗星.txt",
        "file_type": "txt",
        "file_size": 7710,
        "file_hash": "d69476ddbba45223a5e97e786539952c",
        "file_path": "data/files/1/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/1754970756171067621.txt",
        "storage_size": 0,
        "metadata": null,
        "created_at": "2025-08-12T11:52:36.168632288+08:00",
        "updated_at": "2025-08-12T11:52:36.173612121+08:00",
        "processed_at": null,
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

#### POST `/knowledge-bases/:id/knowledge/url` - 从 URL 创建知识

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge/url' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "url":"https://github.com/Tencent/WeKnora",
    "enable_multimodel":true
}'
```

**响应**:

```json
{
    "data": {
        "id": "9c8af585-ae15-44ce-8f73-45ad18394651",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "url",
        "title": "",
        "description": "",
        "source": "https://github.com/Tencent/WeKnora",
        "parse_status": "processing",
        "enable_status": "disabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "",
        "file_type": "",
        "file_size": 0,
        "file_hash": "",
        "file_path": "",
        "storage_size": 0,
        "metadata": null,
        "created_at": "2025-08-12T11:55:05.709266776+08:00",
        "updated_at": "2025-08-12T11:55:05.712918234+08:00",
        "processed_at": null,
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

#### GET `/knowledge-bases/:id/knowledge?page=&page_size` - 获取知识库下的知识列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge?page_size=1&page=1' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "9c8af585-ae15-44ce-8f73-45ad18394651",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "type": "url",
            "title": "",
            "description": "",
            "source": "https://github.com/Tencent/WeKnora",
            "parse_status": "pending",
            "enable_status": "disabled",
            "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "file_name": "",
            "file_type": "",
            "file_size": 0,
            "file_hash": "",
            "file_path": "",
            "storage_size": 0,
            "metadata": null,
            "created_at": "2025-08-12T11:55:05.709266+08:00",
            "updated_at": "2025-08-12T11:55:05.709266+08:00",
            "processed_at": null,
            "error_message": "",
            "deleted_at": null
        }
    ],
    "page": 1,
    "page_size": 1,
    "success": true,
    "total": 2
}
```

注：parse_status 包含 `pending/processing/failed/completed` 四种状态

#### GET `/knowledge/:id` - 获取知识详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "file",
        "title": "彗星.txt",
        "description": "彗星是由冰和尘埃构成的太阳系小天体，接近太阳时会形成彗发和彗尾。其轨道周期差异大，来源包括柯伊伯带和奥尔特云。彗星与小行星的区别逐渐模糊，部分彗星已失去挥发物质，类似小行星。截至2019年，已知彗星超6600颗，数量庞大。彗星在古代被视为凶兆，现代研究揭示其复杂结构与起源。",
        "source": "",
        "parse_status": "completed",
        "enable_status": "enabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "彗星.txt",
        "file_type": "txt",
        "file_size": 7710,
        "file_hash": "d69476ddbba45223a5e97e786539952c",
        "file_path": "data/files/1/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/1754970756171067621.txt",
        "storage_size": 33689,
        "metadata": null,
        "created_at": "2025-08-12T11:52:36.168632+08:00",
        "updated_at": "2025-08-12T11:52:53.376871+08:00",
        "processed_at": "2025-08-12T11:52:53.376573+08:00",
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

#### GET `/knowledge/batch` - 批量获取知识

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/batch?ids=9c8af585-ae15-44ce-8f73-45ad18394651&ids=4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "9c8af585-ae15-44ce-8f73-45ad18394651",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "type": "url",
            "title": "",
            "description": "",
            "source": "https://github.com/Tencent/WeKnora",
            "parse_status": "pending",
            "enable_status": "disabled",
            "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "file_name": "",
            "file_type": "",
            "file_size": 0,
            "file_hash": "",
            "file_path": "",
            "storage_size": 0,
            "metadata": null,
            "created_at": "2025-08-12T11:55:05.709266+08:00",
            "updated_at": "2025-08-12T11:55:05.709266+08:00",
            "processed_at": null,
            "error_message": "",
            "deleted_at": null
        },
        {
            "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "type": "file",
            "title": "彗星.txt",
            "description": "彗星是由冰和尘埃构成的太阳系小天体，接近太阳时会形成彗发和彗尾。其轨道周期差异大，来源包括柯伊伯带和奥尔特云。彗星与小行星的区别逐渐模糊，部分彗星已失去挥发物质，类似小行星。截至2019年，已知彗星超6600颗，数量庞大。彗星在古代被视为凶兆，现代研究揭示其复杂结构与起源。",
            "source": "",
            "parse_status": "completed",
            "enable_status": "enabled",
            "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "file_name": "彗星.txt",
            "file_type": "txt",
            "file_size": 7710,
            "file_hash": "d69476ddbba45223a5e97e786539952c",
            "file_path": "data/files/1/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/1754970756171067621.txt",
            "storage_size": 33689,
            "metadata": null,
            "created_at": "2025-08-12T11:52:36.168632+08:00",
            "updated_at": "2025-08-12T11:52:53.376871+08:00",
            "processed_at": "2025-08-12T11:52:53.376573+08:00",
            "error_message": "",
            "deleted_at": null
        }
    ],
    "success": true
}
```

#### DELETE `/knowledge/:id` - 删除知识

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge/9c8af585-ae15-44ce-8f73-45ad18394651' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "Deleted successfully",
    "success": true
}
```

#### GET `/knowledge/:id/download` - 下载知识文件

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/download' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```
attachment
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 模型管理API

| 方法   | 路径                  | 描述                  |
| ------ | --------------------- | --------------------- |
| POST   | `/models`             | 创建模型              |
| GET    | `/models`             | 获取模型列表          |
| GET    | `/models/:id`         | 获取模型详情          |
| PUT    | `/models/:id`         | 更新模型              |
| DELETE | `/models/:id`         | 删除模型              |

#### POST `/models` - 创建模型

创建对话模型（KnowledgeQA）请求体:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--data '{
    "name": "qwen3:8b",
    "type": "KnowledgeQA",
    "source": "local",
    "description": "LLM Model for Knowledge QA",
    "parameters": {
        "base_url": "",
        "api_key": ""
    },
    "is_default": false
}'
```

创建嵌入模型（Embedding）请求体:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--data '{
    "name": "nomic-embed-text:latest",
    "type": "Embedding",
    "source": "local",
    "description": "Embedding Model",
    "parameters": {
        "base_url": "",
        "api_key": "",
        "embedding_parameters": {
            "dimension": 768,
            "truncate_prompt_tokens": 0
        }
    },
    "is_default": false
}'
```

创建排序模型（Rerank）请求体:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--data '{
    "name": "linux6200/bge-reranker-v2-m3:latest",
    "type": "Rerank",
    "source": "local",
    "description": "Rerank Model for Knowledge QA",
    "parameters": {
        "base_url": "",
        "api_key": ""
    },
    "is_default": false
}'
```

**响应**:

```json
{
    "data": {
        "id": "09c5a1d6-ee8b-4657-9a17-d3dcbd5c70cb",
        "tenant_id": 1,
        "name": "nomic-embed-text:latest3",
        "type": "Embedding",
        "source": "local",
        "description": "Embedding Model",
        "parameters": {
            "base_url": "",
            "api_key": "",
            "embedding_parameters": {
                "dimension": 768,
                "truncate_prompt_tokens": 0
            }
        },
        "is_default": false,
        "status": "downloading",
        "created_at": "2025-08-12T10:39:01.454591766+08:00",
        "updated_at": "2025-08-12T10:39:01.454591766+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### GET `/models` - 获取模型列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```json
{
    "data": [
        {
            "id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "tenant_id": 1,
            "name": "nomic-embed-text:latest",
            "type": "Embedding",
            "source": "local",
            "description": "Embedding Model",
            "parameters": {
                "base_url": "",
                "api_key": "",
                "embedding_parameters": {
                    "dimension": 768,
                    "truncate_prompt_tokens": 0
                }
            },
            "is_default": true,
            "status": "active",
            "created_at": "2025-08-11T20:10:41.813832+08:00",
            "updated_at": "2025-08-11T20:10:41.822354+08:00",
            "deleted_at": null
        },
        {
            "id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "tenant_id": 1,
            "name": "qwen3:8b",
            "type": "KnowledgeQA",
            "source": "local",
            "description": "LLM Model for Knowledge QA",
            "parameters": {
                "base_url": "",
                "api_key": "",
                "embedding_parameters": {
                    "dimension": 0,
                    "truncate_prompt_tokens": 0
                }
            },
            "is_default": true,
            "status": "active",
            "created_at": "2025-08-11T20:10:41.811761+08:00",
            "updated_at": "2025-08-11T20:10:41.825381+08:00",
            "deleted_at": null
        }
    ],
    "success": true
}
```

#### GET `/models/:id` - 获取模型详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/models/dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```json
{
    "data": {
        "id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "tenant_id": 1,
        "name": "nomic-embed-text:latest",
        "type": "Embedding",
        "source": "local",
        "description": "Embedding Model",
        "parameters": {
            "base_url": "",
            "api_key": "",
            "embedding_parameters": {
                "dimension": 768,
                "truncate_prompt_tokens": 0
            }
        },
        "is_default": true,
        "status": "active",
        "created_at": "2025-08-11T20:10:41.813832+08:00",
        "updated_at": "2025-08-11T20:10:41.822354+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### PUT `/models/:id` - 更新模型

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/models/8fdc464d-8eaa-44d4-a85b-094b28af5330' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--data '{
    "name": "linux6200/bge-reranker-v2-m3:latest",
    "description": "Rerank Model for Knowledge QA new",
    "parameters": {
        "base_url": "",
        "api_key": ""
    },
    "is_default": false
}'
```

**响应**:

```json
{
    "data": {
        "id": "8fdc464d-8eaa-44d4-a85b-094b28af5330",
        "tenant_id": 1,
        "name": "linux6200/bge-reranker-v2-m3:latest",
        "type": "Rerank",
        "source": "local",
        "description": "Rerank Model for Knowledge QA new",
        "parameters": {
            "base_url": "",
            "api_key": "",
            "embedding_parameters": {
                "dimension": 0,
                "truncate_prompt_tokens": 0
            }
        },
        "is_default": false,
        "status": "active",
        "created_at": "2025-08-12T10:57:39.512681+08:00",
        "updated_at": "2025-08-12T11:00:27.271678+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### DELETE `/models/:id` - 删除模型

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/models/8fdc464d-8eaa-44d4-a85b-094b28af5330' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```json
{
    "message": "Model deleted",
    "success": true
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 分块管理API

| 方法   | 路径                        | 描述                     |
| ------ | --------------------------- | ------------------------ |
| GET    | `/chunks/:knowledge_id`     | 获取知识的分块列表       |
| DELETE | `/chunks/:knowledge_id/:id` | 删除分块                 |
| DELETE | `/chunks/:knowledge_id`     | 删除知识下的所有分块     |

#### GET `/chunks/:knowledge_id?page=&page_size=` - 获取知识的分块列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5?page=1&page_size=1' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7",
            "tenant_id": 0,
            "knowledge_id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
            "knowledge_base_id": "kb-00000001",
            "content": "彗星xxxx",
            "chunk_index": 0,
            "is_enabled": true,
            "start_at": 0,
            "end_at": 964,
            "pre_chunk_id": "",
            "next_chunk_id": "",
            "chunk_type": "text",
            "parent_chunk_id": "",
            "relation_chunks": null,
            "indirect_relation_chunks": null,
            "image_info": "",
            "created_at": "0001-01-01T00:00:00Z",
            "updated_at": "0001-01-01T00:00:00Z",
            "deleted_at": null
        }
    ],
    "page": 1,
    "page_size": 1,
    "success": true,
    "total": 5
}
```

#### DELETE `/chunks/:knowledge_id/:id` - 删除分块

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "Chunk deleted",
    "success": true
}
```

#### DELETE `/chunks/:knowledge_id` - 删除知识下的所有分块

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "All chunks under knowledge deleted",
    "success": true
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 会话管理API

| 方法   | 路径                                    | 描述                  |
| ------ | --------------------------------------- | --------------------- |
| POST   | `/sessions`                             | 创建会话              |
| GET    | `/sessions/:id`                         | 获取会话详情          |
| GET    | `/sessions`                             | 获取租户的会话列表    |
| PUT    | `/sessions/:id`                         | 更新会话              |
| DELETE | `/sessions/:id`                         | 删除会话              |
| POST   | `/sessions/:session_id/generate_title`  | 生成会话标题          |
| GET    | `/sessions/continue-stream/:session_id` | 继续未完成的会话      |

#### POST `/sessions` - 创建会话

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "knowledge_base_id": "kb-00000001",
    "session_strategy": {
        "max_rounds": 5,
        "enable_rewrite": true,
        "fallback_strategy": "FIXED_RESPONSE",
        "fallback_response": "对不起，我无法回答这个问题",
        "embedding_top_k": 10,
        "keyword_threshold": 0.5,
        "vector_threshold": 0.7,
        "rerank_model_id": "排序模型ID",
        "rerank_top_k": 3,
        "rerank_threshold": 0.7,
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "summary_parameters": {
            "max_tokens": 0,
            "repeat_penalty": 1,
            "top_k": 0,
            "top_p": 0,
            "frequency_penalty": 0,
            "presence_penalty": 0,
            "prompt": "这是用户和助手之间的对话。xxx",
            "context_template": "你是一个专业的智能信息检索助手xxx",
            "no_match_prefix": "<think>\n</think>\nNO_MATCH",
            "temperature": 0.3,
            "seed": 0,
            "max_completion_tokens": 2048
        },
        "no_match_prefix": "<think>\n</think>\nNO_MATCH"
    }
}'
```

**响应**:

```json
{
    "data": {
        "id": "411d6b70-9a85-4d03-bb74-aab0fd8bd12f",
        "title": "",
        "description": "",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "max_rounds": 5,
        "enable_rewrite": true,
        "fallback_strategy": "FIXED_RESPONSE",
        "fallback_response": "对不起，我无法回答这个问题",
        "embedding_top_k": 10,
        "keyword_threshold": 0.5,
        "vector_threshold": 0.7,
        "rerank_model_id": "排序模型ID",
        "rerank_top_k": 3,
        "rerank_threshold": 0.7,
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "summary_parameters": {
            "max_tokens": 0,
            "repeat_penalty": 1,
            "top_k": 0,
            "top_p": 0,
            "frequency_penalty": 0,
            "presence_penalty": 0,
            "prompt": "这是用户和助手之间的对话。xxx",
            "context_template": "你是一个专业的智能信息检索助手xxx",
            "no_match_prefix": "<think>\n</think>\nNO_MATCH",
            "temperature": 0.3,
            "seed": 0,
            "max_completion_tokens": 2048
        },
        "created_at": "2025-08-12T12:26:19.611616669+08:00",
        "updated_at": "2025-08-12T12:26:19.611616919+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### GET `/sessions/:id` - 获取会话详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/ceb9babb-1e30-41d7-817d-fd584954304b' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "ceb9babb-1e30-41d7-817d-fd584954304b",
        "title": "模型优化策略",
        "description": "",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "max_rounds": 5,
        "enable_rewrite": true,
        "fallback_strategy": "fixed",
        "fallback_response": "抱歉，我无法回答这个问题。",
        "embedding_top_k": 10,
        "keyword_threshold": 0.3,
        "vector_threshold": 0.5,
        "rerank_model_id": "",
        "rerank_top_k": 5,
        "rerank_threshold": 0.7,
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "summary_parameters": {
            "max_tokens": 0,
            "repeat_penalty": 1,
            "top_k": 0,
            "top_p": 0,
            "frequency_penalty": 0,
            "presence_penalty": 0,
            "prompt": "这是用户和助手之间的对话",
            "context_template": "你是一个专业的智能信息检索助手",
            "no_match_prefix": "<think>\n</think>\nNO_MATCH",
            "temperature": 0.3,
            "seed": 0,
            "max_completion_tokens": 2048
        },
        "created_at": "2025-08-12T10:24:38.308596+08:00",
        "updated_at": "2025-08-12T10:25:41.317761+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### GET `/sessions?page=&page_size=` - 获取租户的会话列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions?page=1&page_size=1' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "411d6b70-9a85-4d03-bb74-aab0fd8bd12f",
            "title": "",
            "description": "",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "max_rounds": 5,
            "enable_rewrite": true,
            "fallback_strategy": "FIXED_RESPONSE",
            "fallback_response": "对不起，我无法回答这个问题",
            "embedding_top_k": 10,
            "keyword_threshold": 0.5,
            "vector_threshold": 0.7,
            "rerank_model_id": "排序模型ID",
            "rerank_top_k": 3,
            "rerank_threshold": 0.7,
            "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "summary_parameters": {
                "max_tokens": 0,
                "repeat_penalty": 1,
                "top_k": 0,
                "top_p": 0,
                "frequency_penalty": 0,
                "presence_penalty": 0,
                "prompt": "这是用户和助手之间的对话。xxx",
                "context_template": "你是一个专业的智能信息检索助手xxx",
                "no_match_prefix": "<think>\n</think>\nNO_MATCH",
                "temperature": 0.3,
                "seed": 0,
                "max_completion_tokens": 2048
            },
            "created_at": "2025-08-12T12:26:19.611616+08:00",
            "updated_at": "2025-08-12T12:26:19.611616+08:00",
            "deleted_at": null
        }
    ],
    "page": 1,
    "page_size": 1,
    "success": true,
    "total": 2
}
```

#### PUT `/sessions/:id` - 更新会话

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/sessions/411d6b70-9a85-4d03-bb74-aab0fd8bd12f' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "title": "weknora",
    "description": "weknora description",
    "knowledge_base_id": "kb-00000001",
    "max_rounds": 5,
    "enable_rewrite": true,
    "fallback_strategy": "FIXED_RESPONSE",
    "fallback_response": "对不起，我无法回答这个问题",
    "embedding_top_k": 10,
    "keyword_threshold": 0.5,
    "vector_threshold": 0.7,
    "rerank_model_id": "排序模型ID",
    "rerank_top_k": 3,
    "rerank_threshold": 0.7,
    "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
    "summary_parameters": {
        "max_tokens": 0,
        "repeat_penalty": 1,
        "top_k": 0,
        "top_p": 0,
        "frequency_penalty": 0,
        "presence_penalty": 0,
        "prompt": "这是用户和助手之间的对话。xxx",
        "context_template": "你是一个专业的智能信息检索助手xxx",
        "no_match_prefix": "<think>\n</think>\nNO_MATCH",
        "temperature": 0.3,
        "seed": 0,
        "max_completion_tokens": 2048
    }
}'
```

**响应**:

```json
{
    "data": {
        "id": "411d6b70-9a85-4d03-bb74-aab0fd8bd12f",
        "title": "weknora",
        "description": "weknora description",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "max_rounds": 5,
        "enable_rewrite": true,
        "fallback_strategy": "FIXED_RESPONSE",
        "fallback_response": "对不起，我无法回答这个问题",
        "embedding_top_k": 10,
        "keyword_threshold": 0.5,
        "vector_threshold": 0.7,
        "rerank_model_id": "排序模型ID",
        "rerank_top_k": 3,
        "rerank_threshold": 0.7,
        "summary_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
        "summary_parameters": {
            "max_tokens": 0,
            "repeat_penalty": 1,
            "top_k": 0,
            "top_p": 0,
            "frequency_penalty": 0,
            "presence_penalty": 0,
            "prompt": "这是用户和助手之间的对话。xxx",
            "context_template": "你是一个专业的智能信息检索助手xxx",
            "no_match_prefix": "<think>\n</think>\nNO_MATCH",
            "temperature": 0.3,
            "seed": 0,
            "max_completion_tokens": 2048
        },
        "created_at": "0001-01-01T00:00:00Z",
        "updated_at": "2025-08-12T14:20:56.738424351+08:00",
        "deleted_at": null
    },
    "success": true
}
```

#### DELETE `/sessions/:id` - 删除会话

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/sessions/411d6b70-9a85-4d03-bb74-aab0fd8bd12f' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "Session deleted successfully",
    "success": true
}
```

#### POST `/sessions/:session_id/generate_title` - 生成会话标题

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/ceb9babb-1e30-41d7-817d-fd584954304b/generate_title' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
  "messages": [
    {
      "role": "user",
      "content": "你好，我想了解关于人工智能的知识"
    },
    {
      "role": "assistant",
      "content": "人工智能是计算机科学的一个分支..."
    }
  ]
}'
```

**响应**:

```json
{
    "data": "模型优化策略",
    "success": true
}
```

#### GET `/sessions/continue-stream/:session_id` - 继续未完成的会话

**查询参数**:
- `message_id`: 从 `/messages/:session_id/load` 接口中获取的 `is_completed` 为 `false` 的消息 ID

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/continue-stream/ceb9babb-1e30-41d7-817d-fd584954304b?message_id=b8b90eeb-7dd5-4cf9-81c6-5ebcbd759451' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应格式**:
服务器端事件流（Server-Sent Events），与 `/knowledge-chat/:session_id` 返回结果一致

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 聊天功能API

| 方法 | 路径                          | 描述                     |
| ---- | ----------------------------- | ------------------------ |
| POST | `/knowledge-chat/:session_id` | 基于知识库的问答         |
| POST | `/knowledge-search`           | 基于知识库的搜索知识     |

#### POST `/knowledge-chat/:session_id` - 基于知识库的问答

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-chat/ceb9babb-1e30-41d7-817d-fd584954304b' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "query": "彗尾的形状"
}'
```

**响应格式**:
服务器端事件流（Server-Sent Events，Content-Type: text/event-stream）

**响应**:

```
event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"references","content":"","done":false,"knowledge_references":[{"id":"c8347bef-127f-4a22-b962-edf5a75386ec","content":"彗星xxx。","knowledge_id":"a6790b93-4700-4676-bd48-0d4804e1456b","chunk_index":0,"knowledge_title":"彗星.txt","start_at":0,"end_at":2760,"seq":0,"score":4.038836479187012,"match_type":3,"sub_chunk_id":["688821f0-40bf-428e-8cb6-541531ebeb76","c1e9903e-2b4d-4281-be15-0149288d45c2","7d955251-3f79-4fd5-a6aa-02f81e044091"],"metadata":{},"chunk_type":"text","parent_chunk_id":"","image_info":"","knowledge_filename":"彗星.txt","knowledge_source":""},{"id":"fa3aadee-cadb-4a84-9941-c839edc3e626","content":"# 文档名称\n彗星.txt\n\n# 摘要\n彗星是由冰和尘埃构成的太阳系小天体，接近太阳时会释放气体形成彗发和彗尾。其轨道周期差异大，来源包括柯伊伯带和奥尔特云。彗星与小行星的区别逐渐模糊，部分彗星已失去挥发物质，类似小行星。目前已知彗星数量众多，且存在系外彗星。彗星在古代被视为凶兆，现代研究揭示其复杂结构与起源。","knowledge_id":"a6790b93-4700-4676-bd48-0d4804e1456b","chunk_index":6,"knowledge_title":"彗星.txt","start_at":0,"end_at":0,"seq":6,"score":0.6131043121858466,"match_type":3,"sub_chunk_id":null,"metadata":{},"chunk_type":"summary","parent_chunk_id":"c8347bef-127f-4a22-b962-edf5a75386ec","image_info":"","knowledge_filename":"彗星.txt","knowledge_source":""}]}

event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"answer","content":"表现为","done":false,"knowledge_references":null}

event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"answer","content":"结构","done":false,"knowledge_references":null}

event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"answer","content":"。","done":false,"knowledge_references":null}

event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"answer","content":"","done":true,"knowledge_references":null}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 消息管理API

| 方法   | 路径                         | 描述                     |
| ------ | ---------------------------- | ------------------------ |
| GET    | `/messages/:session_id/load` | 获取最近的会话消息列表   |
| DELETE | `/messages/:session_id/:id`  | 删除消息                 |

#### GET `/messages/:session_id/load?before_time=2025-04-18T11:57:31.310671+08:00&limit=20` - 获取最近的会话消息列表

**查询参数**:

- `before_time`: 上一次拉取的最早一条消息的 created_at 字段，为空拉取最近的消息
- `limit`: 每页条数(默认 20)

**请求**:

```curl
curl --location --request GET 'http://localhost:8080/api/v1/messages/ceb9babb-1e30-41d7-817d-fd584954304b/load?limit=3&before_time=2030-08-12T14%3A35%3A42.123456789Z' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "query": "彗尾的形状"
}'
```

**响应**:

```json
{
    "data": [
        {
            "id": "b8b90eeb-7dd5-4cf9-81c6-5ebcbd759451",
            "session_id": "ceb9babb-1e30-41d7-817d-fd584954304b",
            "request_id": "hCA8SDjxcAvv",
            "content": "<think>\n好的",
            "role": "assistant",
            "knowledge_references": [
                {
                    "id": "c8347bef-127f-4a22-b962-edf5a75386ec",
                    "content": "彗星xxx",
                    "knowledge_id": "a6790b93-4700-4676-bd48-0d4804e1456b",
                    "chunk_index": 0,
                    "knowledge_title": "彗星.txt",
                    "start_at": 0,
                    "end_at": 2760,
                    "seq": 0,
                    "score": 4.038836479187012,
                    "match_type": 4,
                    "sub_chunk_id": [
                        "688821f0-40bf-428e-8cb6-541531ebeb76",
                        "c1e9903e-2b4d-4281-be15-0149288d45c2",
                        "7d955251-3f79-4fd5-a6aa-02f81e044091"
                    ],
                    "metadata": {},
                    "chunk_type": "text",
                    "parent_chunk_id": "",
                    "image_info": "",
                    "knowledge_filename": "彗星.txt",
                    "knowledge_source": ""
                },
                {
                    "id": "fa3aadee-cadb-4a84-9941-c839edc3e626",
                    "content": "# 文档名称\n彗星.txt\n\n# 摘要\n彗星是由冰和尘埃构成的太阳系小天体，接近太阳时会释放气体形成彗发和彗尾。其轨道周期差异大，来源包括柯伊伯带和奥尔特云。彗星与小行星的区别逐渐模糊，部分彗星已失去挥发物质，类似小行星。目前已知彗星数量众多，且存在系外彗星。彗星在古代被视为凶兆，现代研究揭示其复杂结构与起源。",
                    "knowledge_id": "a6790b93-4700-4676-bd48-0d4804e1456b",
                    "chunk_index": 6,
                    "knowledge_title": "彗星.txt",
                    "start_at": 0,
                    "end_at": 0,
                    "seq": 6,
                    "score": 0.6131043121858466,
                    "match_type": 0,
                    "sub_chunk_id": null,
                    "metadata": {},
                    "chunk_type": "summary",
                    "parent_chunk_id": "c8347bef-127f-4a22-b962-edf5a75386ec",
                    "image_info": "",
                    "knowledge_filename": "彗星.txt",
                    "knowledge_source": ""
                }
            ],
            "is_completed": true,
            "created_at": "2025-08-12T10:24:38.370548+08:00",
            "updated_at": "2025-08-12T10:25:40.416382+08:00",
            "deleted_at": null
        },
        {
            "id": "7fa136ae-a045-424e-baac-52113d92ae94",
            "session_id": "ceb9babb-1e30-41d7-817d-fd584954304b",
            "request_id": "3475c004-0ada-4306-9d30-d7f5efce50d2",
            "content": "彗尾的形状",
            "role": "user",
            "knowledge_references": [],
            "is_completed": true,
            "created_at": "2025-08-12T14:30:39.732246+08:00",
            "updated_at": "2025-08-12T14:30:39.733277+08:00",
            "deleted_at": null
        },
        {
            "id": "9bcafbcf-a758-40af-a9a3-c4d8e0f49439",
            "session_id": "ceb9babb-1e30-41d7-817d-fd584954304b",
            "request_id": "3475c004-0ada-4306-9d30-d7f5efce50d2",
            "content": "<think>\n好的",
            "role": "assistant",
            "knowledge_references": [
                {
                    "id": "c8347bef-127f-4a22-b962-edf5a75386ec",
                    "content": "彗星xxx",
                    "knowledge_id": "a6790b93-4700-4676-bd48-0d4804e1456b",
                    "chunk_index": 0,
                    "knowledge_title": "彗星.txt",
                    "start_at": 0,
                    "end_at": 2760,
                    "seq": 0,
                    "score": 4.038836479187012,
                    "match_type": 3,
                    "sub_chunk_id": [
                        "688821f0-40bf-428e-8cb6-541531ebeb76",
                        "c1e9903e-2b4d-4281-be15-0149288d45c2",
                        "7d955251-3f79-4fd5-a6aa-02f81e044091"
                    ],
                    "metadata": {},
                    "chunk_type": "text",
                    "parent_chunk_id": "",
                    "image_info": "",
                    "knowledge_filename": "彗星.txt",
                    "knowledge_source": ""
                },
                {
                    "id": "fa3aadee-cadb-4a84-9941-c839edc3e626",
                    "content": "# 文档名称\n彗星.txt\n\n# 摘要\n彗星是由冰和尘埃构成的太阳系小天体，接近太阳时会释放气体形成彗发和彗尾。其轨道周期差异大，来源包括柯伊伯带和奥尔特云。彗星与小行星的区别逐渐模糊，部分彗星已失去挥发物质，类似小行星。目前已知彗星数量众多，且存在系外彗星。彗星在古代被视为凶兆，现代研究揭示其复杂结构与起源。",
                    "knowledge_id": "a6790b93-4700-4676-bd48-0d4804e1456b",
                    "chunk_index": 6,
                    "knowledge_title": "彗星.txt",
                    "start_at": 0,
                    "end_at": 0,
                    "seq": 6,
                    "score": 0.6131043121858466,
                    "match_type": 3,
                    "sub_chunk_id": null,
                    "metadata": {},
                    "chunk_type": "summary",
                    "parent_chunk_id": "c8347bef-127f-4a22-b962-edf5a75386ec",
                    "image_info": "",
                    "knowledge_filename": "彗星.txt",
                    "knowledge_source": ""
                }
            ],
            "is_completed": true,
            "created_at": "2025-08-12T14:30:39.735108+08:00",
            "updated_at": "2025-08-12T14:31:17.829926+08:00",
            "deleted_at": null
        }
    ],
    "success": true
}
```

#### DELETE `/messages/:session_id/:id` - 删除消息

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/messages/ceb9babb-1e30-41d7-817d-fd584954304b/9bcafbcf-a758-40af-a9a3-c4d8e0f49439' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "Message deleted successfully",
    "success": true
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 评估功能API

| 方法 | 路径          | 描述                  |
| ---- | ------------- | --------------------- |
| GET  | `/evaluation` | 获取评估任务          |
| POST | `/evaluation` | 创建评估任务          |

#### GET `/evaluation` - 获取评估任务

**请求参数**:
- `task_id`: 从 `POST /evaluation` 接口中获取到的任务 ID
- `X-API-Key`: 用户 API Key

**请求**:

```bash
curl --location 'http://localhost:8080/api/v1/evaluation?task_id=c34563ad-b09f-4858-b72e-e92beb80becb' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "task": {
            "id": "c34563ad-b09f-4858-b72e-e92beb80becb",
            "tenant_id": 1,
            "dataset_id": "default",
            "start_time": "2025-08-12T14:54:26.221804768+08:00",
            "status": 2,
            "total": 1,
            "finished": 1
        },
        "params": {
            "session_id": "",
            "knowledge_base_id": "2ef57434-8c8d-4442-b967-2f7fc578a2fc",
            "vector_threshold": 0.5,
            "keyword_threshold": 0.3,
            "embedding_top_k": 10,
            "vector_database": "",
            "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
            "rerank_top_k": 5,
            "rerank_threshold": 0.7,
            "chat_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "summary_config": {
                "max_tokens": 0,
                "repeat_penalty": 1,
                "top_k": 0,
                "top_p": 0,
                "frequency_penalty": 0,
                "presence_penalty": 0,
                "prompt": "这是用户和助手之间的对话。",
                "context_template": "你是一个专业的智能信息检索助手",
                "no_match_prefix": "<think>\n</think>\nNO_MATCH",
                "temperature": 0.3,
                "seed": 0,
                "max_completion_tokens": 2048
            },
            "fallback_strategy": "",
            "fallback_response": "抱歉，我无法回答这个问题。"
        },
        "metric": {
            "retrieval_metrics": {
                "precision": 0,
                "recall": 0,
                "ndcg3": 0,
                "ndcg10": 0,
                "mrr": 0,
                "map": 0
            },
            "generation_metrics": {
                "bleu1": 0.037656734016532384,
                "bleu2": 0.04067392145167686,
                "bleu4": 0.048963321289052536,
                "rouge1": 0,
                "rouge2": 0,
                "rougel": 0
            }
        }
    },
    "success": true
}
```

#### POST `/evaluation` - 创建评估任务

**请求参数**:
- `dataset_id`: 评估使用的数据集，暂时只支持官方测试数据集 `default`
- `knowledge_base_id`: 评估使用的知识库
- `chat_id`: 评估使用的对话模型
- `rerank_id`: 评估使用的重排序模型

**请求**:

```bash
curl --location 'http://localhost:8080/api/v1/evaluation' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "dataset_id": "default",
    "knowledge_base_id": "kb-00000001",
    "chat_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
    "rerank_id": "b30171a1-787b-426e-a293-735cd5ac16c0"
}'
```

**响应**:

```json
{
    "data": {
        "task": {
            "id": "c34563ad-b09f-4858-b72e-e92beb80becb",
            "tenant_id": 1,
            "dataset_id": "default",
            "start_time": "2025-08-12T14:54:26.221804768+08:00",
            "status": 1
        },
        "params": {
            "session_id": "",
            "knowledge_base_id": "2ef57434-8c8d-4442-b967-2f7fc578a2fc",
            "vector_threshold": 0.5,
            "keyword_threshold": 0.3,
            "embedding_top_k": 10,
            "vector_database": "",
            "rerank_model_id": "b30171a1-787b-426e-a293-735cd5ac16c0",
            "rerank_top_k": 5,
            "rerank_threshold": 0.7,
            "chat_model_id": "8aea788c-bb30-4898-809e-e40c14ffb48c",
            "summary_config": {
                "max_tokens": 0,
                "repeat_penalty": 1,
                "top_k": 0,
                "top_p": 0,
                "frequency_penalty": 0,
                "presence_penalty": 0,
                "prompt": "这是用户和助手之间的对话。",
                "context_template": "你是一个专业的智能信息检索助手，xxx",
                "no_match_prefix": "<think>\n</think>\nNO_MATCH",
                "temperature": 0.3,
                "seed": 0,
                "max_completion_tokens": 2048
            },
            "fallback_strategy": "",
            "fallback_response": "抱歉，我无法回答这个问题。"
        }
    },
    "success": true
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>