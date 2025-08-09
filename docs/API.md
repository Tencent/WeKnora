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

1. **创建租户时获取**：通过 `POST /api/v1/tenants` 接口创建新租户时，响应中会自动返回生成的 API Key。
   ```json
   {
     "success": true,
     "data": {
       "id": 1,
       "name": "租户名称",
       "description": "租户描述",
       "api_key": "生成的API密钥",
       "created_at": "2023-01-01T00:00:00Z",
       "updated_at": "2023-01-01T00:00:00Z"
     }
   }
   ```

2. **查看现有租户信息**：通过 `GET /api/v1/tenants/:id` 接口查看已有租户的详细信息，响应中包含 API Key。

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

**请求体**:

```json
{
  "name": "租户名称",
  "description": "租户描述"
}
```

**响应**:

```json
{
  "success": true,
  "data": {
    "id": 1,
    "name": "租户名称",
    "description": "租户描述",
    "api_key": "生成的API密钥",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### GET `/tenants/:id` - 获取指定租户信息

**响应**:

```json
{
  "success": true,
  "data": {
    "id": 1,
    "name": "租户名称",
    "description": "租户描述",
    "api_key": "API密钥",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### PUT `/tenants/:id` - 更新租户信息

**请求体**:

```json
{
  "name": "新租户名称",
  "description": "新租户描述"
}
```

**响应**:

```json
{
  "success": true,
  "data": {
    "id": 1,
    "name": "新租户名称",
    "description": "新租户描述",
    "api_key": "API密钥",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### DELETE `/tenants/:id` - 删除租户

**响应**:

```json
{
  "success": true,
  "message": "租户删除成功"
}
```

#### GET `/tenants` - 获取租户列表

**响应**:

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": 1,
        "name": "租户1",
        "description": "租户1描述",
        "api_key": "API密钥1",
        "created_at": "2023-01-01T00:00:00Z",
        "updated_at": "2023-01-01T00:00:00Z"
      },
      {
        "id": 2,
        "name": "租户2",
        "description": "租户2描述",
        "api_key": "API密钥2",
        "created_at": "2023-01-01T00:00:00Z",
        "updated_at": "2023-01-01T00:00:00Z"
      }
    ]
  }
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

**请求体**:

```json
{
  "name": "知识库名称",
  "description": "知识库描述",
  "config": {
    "chunk_size": 1000,
    "chunk_overlap": 200
  },
  "embedding_model_id": "模型ID"
}
```

**响应**:

```json
{
  "success": true,
  "data": {
    "id": "知识库ID",
    "tenant_id": 1,
    "name": "知识库名称",
    "description": "知识库描述",
    "config": {
      "chunk_size": 1000,
      "chunk_overlap": 200
    },
    "embedding_model_id": "模型ID",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### GET `/knowledge-bases` - 获取知识库列表

**响应**:

```json
{
  "success": true,
  "data": [
    {
      "id": "知识库ID1",
      "tenant_id": 1,
      "name": "知识库1",
      "description": "知识库1描述",
      "config": {
        "chunk_size": 1000,
        "chunk_overlap": 200
      },
      "embedding_model_id": "模型ID",
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    },
    {
      "id": "知识库ID2",
      "tenant_id": 1,
      "name": "知识库2",
      "description": "知识库2描述",
      "config": {
        "chunk_size": 1000,
        "chunk_overlap": 200
      },
      "embedding_model_id": "模型ID",
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    }
  ]
}
```

#### GET `/knowledge-bases/:id` - 获取知识库详情

**响应**:

```json
{
  "success": true,
  "data": {
    "id": "知识库ID",
    "tenant_id": 1,
    "name": "知识库名称",
    "description": "知识库描述",
    "config": {
      "chunk_size": 1000,
      "chunk_overlap": 200
    },
    "embedding_model_id": "模型ID",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### PUT `/knowledge-bases/:id` - 更新知识库

**请求体**:

```json
{
  "name": "新知识库名称",
  "description": "新知识库描述",
  "config": {
    "chunk_size": 800,
    "chunk_overlap": 150
  }
}
```

**响应**:

```json
{
  "success": true,
  "data": {
    "id": "知识库ID",
    "tenant_id": 1,
    "name": "新知识库名称",
    "description": "新知识库描述",
    "config": {
      "chunk_size": 800,
      "chunk_overlap": 150
    },
    "embedding_model_id": "模型ID",
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### DELETE `/knowledge-bases/:id` - 删除知识库

**响应**:

```json
{
  "success": true
}
```

#### GET `/knowledge-bases/:id/hybrid-search` - 混合搜索知识库内容

**查询参数**:

- `query`: 搜索关键词(必填)

**响应**:

```json
{
  "success": true,
  "data": [
    {
      "id": "分块ID",
      "content": "匹配到的内容片段",
      "metadata": {
        "source": "来源文件",
        "page": 1
      },
      "knowledge_title": "人工智能导论.pdf",
      "score": 0.92
    },
    {
      "id": "分块ID",
      "content": "匹配到的内容片段",
      "metadata": {
        "source": "来源文件",
        "page": 2
      },
      "knowledge_title": "人工智能导论.pdf",
      "score": 0.88
    }
  ]
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

**请求格式**:

```
Content-Type: multipart/form-data

file: [上传的文件]
```

**响应**:

```json
{
  "success": true,
  "data": {
    "id": "4f0845e3-8f6e-4d5a-816b-196e34070617",
    "tenant_id": 1,
    "knowledge_base_id": "kb-00000003",
    "type": "file",
    "title": "彗星.txt",
    "description": "",
    "source": "",
    "parse_status": "failed",
    "enable_status": "disabled",
    "embedding_model_id": "model-embedding-00000001",
    "file_name": "彗星.txt",
    "file_type": "txt",
    "file_size": 7710,
    "file_path": "",
    "created_at": "2025-04-16T04:15:01.633797Z",
    "updated_at": "2025-04-16T04:15:03.24784Z",
    "processed_at": null,
    "error_message": "EmbedBatch API error: Http Status 403 Forbidden"
  }
}
```

#### POST `/knowledge-bases/:id/knowledge/url` - 从 URL 创建知识

请求体:

```json
{
  "url": "https://example.com/document.pdf"
}
```

响应:

```json
{
  "success": true,
  "data": {
    "id": "4f0845e3-8f6e-4d5a-816b-196e34070617",
    "tenant_id": 1,
    "knowledge_base_id": "kb-00000003",
    "type": "file",
    "title": "彗星.txt",
    "description": "",
    "source": "",
    "parse_status": "failed",
    "enable_status": "disabled",
    "embedding_model_id": "model-embedding-00000001",
    "file_name": "彗星.txt",
    "file_type": "txt",
    "file_size": 7710,
    "file_path": "",
    "created_at": "2025-04-16T04:15:01.633797Z",
    "updated_at": "2025-04-16T04:15:03.24784Z",
    "processed_at": null,
    "error_message": "EmbedBatch API error: Http Status 403 Forbidden"
  }
}
```

#### GET `/knowledge-bases/:id/knowledge?page=&page_size` - 获取知识库下的知识列表

响应:

```json
{
  "success": true,
  "data": [
    {
      "id": "4f0845e3-8f6e-4d5a-816b-196e34070617",
      "tenant_id": 1,
      "knowledge_base_id": "kb-00000003",
      "type": "file",
      "title": "彗星.txt",
      "description": "",
      "source": "",
      "parse_status": "failed",
      "enable_status": "disabled",
      "embedding_model_id": "model-embedding-00000001",
      "file_name": "彗星.txt",
      "file_type": "txt",
      "file_size": 7710,
      "file_path": "",
      "created_at": "2025-04-16T04:15:01.633797Z",
      "updated_at": "2025-04-16T04:15:03.24784Z",
      "processed_at": null,
      "error_message": "EmbedBatch API error: Http Status 403 Forbidden"
    }
  ],
  "total": 98,
  "page": 1,
  "page_size": 20
}
```

注：parse_status 包含 `pending/processing/failed/completed` 四种状态

#### GET `/knowledge/:id` - 获取知识详情

响应:

```json
{
  "success": true,
  "data": {
    "id": "4f0845e3-8f6e-4d5a-816b-196e34070617",
    "tenant_id": 1,
    "knowledge_base_id": "kb-00000003",
    "type": "file",
    "title": "彗星.txt",
    "description": "",
    "source": "",
    "parse_status": "failed",
    "enable_status": "disabled",
    "embedding_model_id": "model-embedding-00000001",
    "file_name": "彗星.txt",
    "file_type": "txt",
    "file_size": 7710,
    "file_path": "",
    "created_at": "2025-04-16T04:15:01.633797Z",
    "updated_at": "2025-04-16T04:15:03.24784Z",
    "processed_at": null,
    "error_message": "EmbedBatch API error: Http Status 403 Forbidden"
  }
}
```

#### GET `/knowledge/batch` - 批量获取知识

查询参数:

- `ids`: 知识 ID 列表，例如：`?ids=knowledge_id_1&ids=knowledge_id_2`

响应:

```json
{
  "success": true,
  "data": [
    {
      "id": "知识ID1",
      "tenant_id": 1,
      "knowledge_base_id": "知识库ID",
      "type": "file",
      "title": "文档1.pdf",
      "description": "",
      "source": "",
      "parse_status": "completed",
      "enable_status": "enabled",
      "embedding_model_id": "model-embedding-00000001",
      "file_name": "文档1.pdf",
      "file_type": "pdf",
      "file_size": 1024,
      "file_path": "",
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z",
      "processed_at": "2023-01-01T00:00:01Z",
      "error_message": ""
    },
    {
      "id": "知识ID2",
      "tenant_id": 1,
      "knowledge_base_id": "知识库ID",
      "type": "url",
      "title": "网页文档",
      "description": "",
      "source": "https://example.com/doc",
      "parse_status": "completed",
      "enable_status": "enabled",
      "embedding_model_id": "model-embedding-00000001",
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z",
      "processed_at": "2023-01-01T00:00:01Z",
      "error_message": ""
    }
  ]
}
```

#### DELETE `/knowledge/:id` - 删除知识

响应:

```json
{
  "success": true,
  "message": "删除成功"
}
```

#### GET `/knowledge/:id/download` - 下载知识文件

响应

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

```json
{
  "name": "模型名称",
  "type": "KnowledgeQA",  
  "source": "remote",     # remote / local
  "description": "模型描述",
  "parameters": {
    "model": "gpt-4",
    "api_key": "sk-xxxxx",
    "base_url": "xxx"
  }
}
```

创建嵌入模型（Embedding）请求体:

```json
{
  "name": "模型名称",
  "type": "Embedding",  
  "source": "remote",     # remote / local
  "description": "模型描述",
  "parameters": {
    "model": "bge-m3",
    "api_key": "sk-xxxxx",
    "base_url": "xxx",
    "embedding_parameters": {
      "dimension": 768,
      "truncate_prompt_tokens": 512
    }
  }
}
```

创建排序模型（Rerank）请求体:

```json
{
  "name": "模型名称",
  "type": "Rerank",  
  "source": "remote",     # remote / local
  "description": "模型描述",
  "parameters": {
    "model": "gte-reranker",
    "api_key": "sk-xxxxx",
    "base_url": "xxx"
  }
}
```


响应:

```json
{
  "success": true,
  "data": {
    "id": "模型ID",
    "tenant_id": 1,
    "name": "模型名称",
    "type": "LLM",
    "source": "OPENAI",
    "description": "模型描述",
    "parameters": {
      "model": "gpt-4",
      "api_key": "sk-xxxxx",
      "base_url": "https://api.openai.com"
    },
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### GET `/models` - 获取模型列表

响应:

```json
{
  "success": true,
  "data": [
    {
      "id": "模型ID1",
      "tenant_id": 1,
      "name": "模型1",
      "type": "LLM",
      "source": "OPENAI",
      "description": "模型1描述",
      "parameters": {
        "model": "gpt-4",
        "api_key": "sk-xxxxx",
        "base_url": "https://api.openai.com"
      },
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    },
    {
      "id": "模型ID2",
      "tenant_id": 1,
      "name": "模型2",
      "type": "EMBEDDING",
      "source": "OPENAI",
      "description": "模型2描述",
      "parameters": {
        "model": "text-embedding-ada-002",
        "api_key": "sk-xxxxx",
        "base_url": "https://api.openai.com"
      },
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    }
  ]
}
```

#### GET `/models/:id` - 获取模型详情

响应:

```json
{
  "success": true,
  "data": {
    "id": "模型ID",
    "tenant_id": 1,
    "name": "模型名称",
    "type": "LLM",
    "source": "OPENAI",
    "description": "模型描述",
    "parameters": {
      "model": "gpt-4",
      "api_key": "sk-xxxxx",
      "base_url": "https://api.openai.com"
    },
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### PUT `/models/:id` - 更新模型

请求体:

```json
{
  "name": "新模型名称",
  "description": "新模型描述",
  "parameters": {
    "model": "gpt-4-turbo",
    "api_key": "sk-xxxxx",
    "base_url": "https://api.openai.com"
  },
  "is_default": false
}
```

响应:

```json
{
  "success": true,
  "data": {
    "id": "模型ID",
    "tenant_id": 1,
    "name": "新模型名称",
    "type": "LLM",
    "source": "OPENAI",
    "description": "新模型描述",
    "parameters": {
      "model": "gpt-4-turbo",
      "api_key": "sk-xxxxx",
      "base_url": "https://api.openai.com"
    },
    "is_default": false,
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### DELETE `/models/:id` - 删除模型

响应:

```json
{
  "success": true,
  "message": "模型删除成功"
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 分块管理API

| 方法   | 路径                        | 描述                     |
| ------ | --------------------------- | ------------------------ |
| GET    | `/chunks/:knowledge_id`     | 获取知识的分块列表       |
| PUT    | `/chunks/:knowledge_id/:id` | 更新分块                 |
| DELETE | `/chunks/:knowledge_id/:id` | 删除分块                 |
| DELETE | `/chunks/:knowledge_id`     | 删除知识下的所有分块     |

#### GET `/chunks/:knowledge_id?page=&page_size=` - 获取知识的分块列表

查询参数:

- `page`: 页码(默认 1)
- `page_size`: 每页条数(默认 20)

响应:

```json
{
  "success": true,
  "data": [
    {
      "id": "分块ID1",
      "knowledge_id": "知识ID",
      "content": "分块1内容...",
      "is_enabled": true,
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    },
    {
      "id": "分块ID2",
      "knowledge_id": "知识ID",
      "content": "分块2内容...",
      "is_enable": true,
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    }
  ],
  "total": 50,
  "page": 1,
  "page_size": 20
}
```

#### PUT `/chunks/:knowledge_id/:id` - 更新分块

请求体:

```json
{
  "content": "更新后的分块内容",
  "metadata": {
    "page": 1,
    "position": "top",
    "custom_field": "自定义字段"
  }
}
```

响应:

```json
{
  "success": true,
  "data": {
    "id": "分块ID",
    "knowledge_id": "知识ID",
    "content": "更新后的分块内容",
    "metadata": {
      "page": 1,
      "position": "top",
      "custom_field": "自定义字段"
    },
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### DELETE `/chunks/:knowledge_id/:id` - 删除分块

响应:

```json
{
  "success": true,
  "message": "分块删除成功"
}
```

#### DELETE `/chunks/:knowledge_id` - 删除知识下的所有分块

响应:

```json
{
  "success": true,
  "message": "所有分块删除成功"
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

请求体:

```json
{
  "knowledge_base_id": "知识库ID",
  "session_strategy": {
    "max_rounds": 5,
    "enable_rewrite": true,
    "fallback_strategy": "FIXED_RESPONSE",
    "fallback_response": "对不起，我无法回答这个问题",
    "keyword_threshold": 0.5,
    "vector_threshold": 0.7,
    "rerank_model_id": "排序模型ID",
    "rerank_top_k": 3,
    "summary_model_id": "总结模型ID",
    "summary_parameters": {
      "max_tokens": 100,
      "top_p": 0.9,
      "top_k": 40,
      "frequency_penalty": 0.0,
      "presence_penalty": 0.0,
      "repeat_penalty": 1.1,
      "prompt": "总结对话内容",
      "context_template": "上下文模板"
    }
  }
}
```

响应:

```json
{
  "success": true,
  "data": {
    "id": "会话ID",
    "tenant_id": 1,
    "knowledge_base_id": "知识库ID",
    "title": "未命名会话",
    "max_rounds": 5,
    "enable_rewrite": true,
    "fallback_strategy": "FIXED_RESPONSE",
    "fallback_response": "对不起，我无法回答这个问题",
    "keyword_threshold": 0.5,
    "vector_threshold": 0.7,
    "rerank_model_id": "排序模型ID",
    "rerank_top_k": 3,
    "summary_model_id": "总结模型ID",
    "summary_parameters": {
      "max_tokens": 100,
      "top_p": 0.9,
      "top_k": 40,
      "frequency_penalty": 0.0,
      "presence_penalty": 0.0,
      "repeat_penalty": 1.1,
      "prompt": "总结对话内容",
      "context_template": "上下文模板"
    },
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### GET `/sessions/:id` - 获取会话详情

响应:

```json
{
  "success": true,
  "data": {
    "id": "会话ID",
    "tenant_id": 1,
    "knowledge_base_id": "知识库ID",
    "title": "会话标题",
    "max_rounds": 5,
    "enable_rewrite": true,
    "fallback_strategy": "FIXED_RESPONSE",
    "fallback_response": "对不起，我无法回答这个问题",
    "keyword_threshold": 0.5,
    "vector_threshold": 0.7,
    "rerank_model_id": "排序模型ID",
    "rerank_top_k": 3,
    "summary_model_id": "总结模型ID",
    "summary_parameters": {
      "max_tokens": 100,
      "top_p": 0.9,
      "top_k": 40,
      "frequency_penalty": 0.0,
      "presence_penalty": 0.0,
      "repeat_penalty": 1.1,
      "prompt": "总结对话内容",
      "context_template": "上下文模板"
    },
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### GET `/sessions?page=&page_size=` - 获取租户的会话列表

响应:

```json
{
  "success": true,
  "data": [
    {
      "id": "会话ID1",
      "tenant_id": 1,
      "knowledge_base_id": "知识库ID",
      "title": "会话1",
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    },
    {
      "id": "会话ID2",
      "tenant_id": 1,
      "knowledge_base_id": "知识库ID",
      "title": "会话2",
      "created_at": "2023-01-01T00:00:00Z",
      "updated_at": "2023-01-01T00:00:00Z"
    }
  ],
  "total": 98,
  "page": 1,
  "page_size": 20
}
```

#### PUT `/sessions/:id` - 更新会话

请求体:

```json
{
  "title": "新会话标题",
  "max_rounds": 10,
  "enable_rewrite": false,
  "fallback_strategy": "NO_FALLBACK",
  "fallback_response": "",
  "keyword_threshold": 0.6,
  "vector_threshold": 0.8,
  "rerank_model_id": "新排序模型ID",
  "rerank_top_k": 5,
  "summary_model_id": "新总结模型ID",
  "summary_parameters": {
    "max_tokens": 150,
    "top_p": 0.8,
    "top_k": 50,
    "frequency_penalty": 0.1,
    "presence_penalty": 0.1,
    "repeat_penalty": 1.2,
    "prompt": "总结这段对话",
    "context_template": "新上下文模板"
  }
}
```

响应:

```json
{
  "success": true,
  "data": {
    "id": "会话ID",
    "tenant_id": 1,
    "knowledge_base_id": "知识库ID",
    "title": "新会话标题",
    "max_rounds": 10,
    "enable_rewrite": false,
    "fallback_strategy": "NO_FALLBACK",
    "fallback_response": "",
    "keyword_threshold": 0.6,
    "vector_threshold": 0.8,
    "rerank_model_id": "新排序模型ID",
    "rerank_top_k": 5,
    "summary_model_id": "新总结模型ID",
    "summary_parameters": {
      "max_tokens": 150,
      "top_p": 0.8,
      "top_k": 50,
      "frequency_penalty": 0.1,
      "presence_penalty": 0.1,
      "repeat_penalty": 1.2,
      "prompt": "总结这段对话",
      "context_template": "新上下文模板"
    },
    "created_at": "2023-01-01T00:00:00Z",
    "updated_at": "2023-01-01T00:00:00Z"
  }
}
```

#### DELETE `/sessions/:id` - 删除会话

响应:

```json
{
  "success": true,
  "message": "会话删除成功"
}
```

#### POST `/sessions/:session_id/generate_title` - 生成会话标题

请求体:

```json
{
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
}
```

响应:

```json
{
  "success": true,
  "data": "关于人工智能的对话"
}
```

#### GET `/sessions/continue-stream/:session_id` - 继续未完成的会话

**查询参数**:
- `message_id`: 从 `/messages/:session_id/load` 接口中获取的 `is_completed` 为 `false` 的消息 ID

**响应格式**:
服务器端事件流（Server-Sent Events），与 `/knowledge-chat/:session_id` 返回结果一致

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 聊天功能API

| 方法 | 路径                          | 描述                     |
| ---- | ----------------------------- | ------------------------ |
| POST | `/knowledge-chat/:session_id` | 基于知识库的问答         |
| POST | `/knowledge-search`           | 基于知识库的搜索知识     |

#### POST `/knowledge-chat/:session_id` - 基于知识库的问答

**请求体**:

```json
{
  "query": "人工智能的定义是什么？"
}
```

**响应格式**:
服务器端事件流（Server-Sent Events，Content-Type: text/event-stream）

**响应示例**:

```
event: message
data: {"id":"消息ID","response_type":"references","done":false,"knowledge_references":[{"id":"分块ID1","content":"人工智能的定义...","knowledge_title":"人工智能导论.pdf"},{"id":"分块ID2","content":"关于AI的更多信息...","knowledge_title":"人工智能导论.pdf"}]}

event: message
data: {"id":"消息ID","content":"人工","created_at":"2023-01-01T00:00:00Z","done":false}

event: message
data: {"id":"消息ID","content":"人工智能是","created_at":"2023-01-01T00:00:00Z","done":false}

event: message
data: {"id":"消息ID","content":"人工智能是研究...","created_at":"2023-01-01T00:00:00Z","done":false}

event: message
data: {"id":"消息ID","content":"人工智能是研究、开发用于模拟、延伸和扩展人的智能的理论、方法、技术及应用系统的一门新的技术科学...","created_at":"2023-01-01T00:00:00Z","done":true}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>

### 消息管理API

| 方法   | 路径                         | 描述                     |
| ------ | ---------------------------- | ------------------------ |
| GET    | `/messages/:session_id/load` | 获取最近的会话消息列表   |
| DELETE | `/messages/:session_id/:id`  | 删除消息                 |

#### GET `/messages/:session_id/load?before_time=2025-04-18T11:57:31.310671+08:00&limit=20` - 获取最近的会话消息列表

查询参数:

- `before_time`: 上一次拉取的最早一条消息的 created_at 字段，为空拉取最近的消息
- `limit`: 每页条数(默认 20)

响应:

```json
{
  "data": [
    {
      "id": "22bafcce-a9c0-4dbd-8ce7-a881a2943e5f",
      "session_id": "76218775-0af1-4933-9f76-40d0a6622e76",
      "request_id": "6965bdcc-8264-43d5-b9f4-6bd844aa3aa7",
      "content": "印度的国土面积",
      "role": "user",
      "knowledge_references": null,
      "created_at": "2025-04-18T11:57:31.310671+08:00",
      "updated_at": "2025-04-18T11:57:31.320384+08:00",
      "is_completed": true
    },
    {
      "id": "6c9564c8-4801-47d6-86ee-2ed58c1e331e",
      "session_id": "76218775-0af1-4933-9f76-40d0a6622e76",
      "request_id": "6965bdcc-8264-43d5-b9f4-6bd844aa3aa7",
      "content": "",
      "role": "assistant",
      "knowledge_references": [
        {
          "id": "b3f489f8-278c-4f67-83ca-97b7bc39f35b",
          "content": "",
          "knowledge_id": "6c595abb-3086-408c-9e2e-40c543af23b6",
          "knowledge_tite": "openaiassets_cfa8f2941d6b41cc79b91b4553ff8a4f_110591700536724035.txt"
        },
        {
          "id": "71008e5c-12bd-4da3-bd8b-799b6af0c112",
          "content": "",
          "knowledge_id": "6c595abb-3086-408c-9e2e-40c543af23b6",
          "knowledge_tite": "openaiassets_cfa8f2941d6b41cc79b91b4553ff8a4f_110591700536724035.txt"
        }
      ],
      "created_at": "2025-04-18T11:57:45.499279+08:00",
      "updated_at": "2025-04-18T11:57:45.513845+08:00",
      "is_completed": false
    }
  ],
  "success": true
}
```

#### DELETE `/messages/:session_id/:id` - 删除消息

响应:

```json
{
  "success": true,
  "message": "消息删除成功"
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

**请求示例**:

```bash
curl --location 'http://localhost:8080/api/v1/evaluation/?task_id=:task_id' \
--header 'X-API-Key: sk-00000001abcdefg123456'
```

**响应示例**:

```json
{
  "data": {
    "id": ":task_id",
    "tenant_id": 1,
    "embedding_id": "default",
    "rerank_id": "default",
    "chat_id": "default",
    "start_time": "2025-04-27T17:59:35.662145114+08:00",
    "status": 1,
    "total": 100,
    "finished": 27,
    "metric": {
      "retrieval_metrics": {
        "precision": 0.26419753086419756,
        "recall": 1,
        "ndcg3": 0.9716533097411622,
        "ndcg10": 0.9914200384804508,
        "mrr": 1,
        "map": 0.9808641975308641
      },
      "generation_metrics": {
        "bleu1": 0.07886284400848455,
        "bleu2": 0.06475994660439699,
        "bleu4": 0.046991784754461315,
        "rouge1": 0.1922821051422303,
        "rouge2": 0.0941559283518759,
        "rougel": 0.1837134727619397
      }
    }
  },
  "success": true
}
```

#### POST `/evaluation` - 创建评估任务

**请求参数**:
- `embedding_id`（可选）: 评估使用的嵌入模型，默认使用 model-embedding-00000001 模型
- `chat_id`（可选）: 评估使用的对话模型，默认使用 model-knowledgeqa-00000003 模型
- `rerank_id`（可选）: 评估使用的重排序模型，默认使用 model-rerank-00000002 模型
- `dataset_id`（可选）: 评估使用的数据集，暂时只支持官方测试数据集

**请求示例**:

```bash
curl --location 'http://localhost:8080/api/v1/evaluation' \
--header 'X-API-Key: sk-00000001abcdefg123456' \
--header 'Content-Type: application/json' \
--data '{}'
```

**响应示例**:

```json
{
  "data": {
    "id": "6f43d272-d65f-4005-96ad-104b7761ea65",
    "tenant_id": 1,
    "embedding_id": "default",
    "rerank_id": "default",
    "chat_id": "default",
    "start_time": "2025-04-27T17:59:35.662145114+08:00",
    "status": 1
  },
  "success": true
}
```

<div align="right"><a href="#weknora-api-文档">返回顶部 ↑</a></div>