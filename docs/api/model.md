# 模型管理 API

[返回目录](./README.md)

| 方法   | 路径                    | 描述                  |
| ------ | ----------------------- | --------------------- |
| POST   | `/models`               | 创建模型              |
| GET    | `/models`               | 获取模型列表          |
| GET    | `/models/:id`           | 获取模型详情          |
| PUT    | `/models/:id`           | 更新模型              |
| DELETE | `/models/:id`           | 删除模型              |
| GET    | `/models/providers`     | 获取模型服务商列表    |

## 服务商支持 (Provider Support)

WeKnora 支持多种主流 AI 模型服务商，在创建模型时可通过 `provider` 字段指定服务商类型以获得更好的兼容性。

### 支持的服务商列表

| 服务商标识     | 名称                         | 支持的模型类型                  |
| -------------- | ---------------------------- | ------------------------------- |
| `generic`      | 自定义 (OpenAI兼容接口)  | Chat, Embedding, Rerank, VLLM   |
| `openai`       | OpenAI                       | Chat, Embedding, Rerank, VLLM   |
| `aliyun`       | 阿里云 DashScope             | Chat, Embedding, Rerank, VLLM   |
| `zhipu`        | 智谱 BigModel                | Chat, Embedding, Rerank, VLLM   |
| `volcengine`   | 火山引擎 Volcengine          | Chat, Embedding, VLLM           |
| `hunyuan`      | 腾讯混元 Hunyuan             | Chat, Embedding                 |
| `deepseek`     | DeepSeek                     | Chat                            |
| `minimax`      | MiniMax                      | Chat                            |
| `mimo`         | 小米 MiMo                    | Chat                            |
| `siliconflow`  | 硅基流动 SiliconFlow         | Chat, Embedding, Rerank, VLLM   |
| `jina`         | Jina                         | Embedding, Rerank               |
| `openrouter`   | OpenRouter                   | Chat, VLLM                      |
| `gemini`       | Google Gemini                | Chat                            |
| `modelscope`   | 魔搭 ModelScope              | Chat, Embedding, VLLM           |
| `moonshot`     | 月之暗面 Moonshot            | Chat, VLLM                      |
| `qianfan`      | 百度千帆 Baidu Cloud         | Chat, Embedding, Rerank, VLLM   |
| `qiniu`        | 七牛云 Qiniu                 | Chat                            |
| `longcat`      | LongCat AI                   | Chat                            |
| `gpustack`     | GPUStack                     | Chat, Embedding, Rerank, VLLM   |

## GET `/models/providers` - 获取模型服务商列表

根据模型类型获取支持的服务商列表及配置信息。

**请求参数**:

| 参数       | 类型   | 必填 | 描述                                           |
| ---------- | ------ | ---- | ---------------------------------------------- |
| model_type | string | 否   | 模型类型：`chat`, `embedding`, `rerank`, `vllm` |

**请求**:

```curl
# 获取所有服务商
curl --location 'http://localhost:8080/api/v1/models/providers' \
--header 'X-API-Key: your_api_key'

# 获取支持 Embedding 类型的服务商
curl --location 'http://localhost:8080/api/v1/models/providers?model_type=embedding' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "value": "aliyun",
            "label": "阿里云 DashScope",
            "description": "qwen-plus, tongyi-embedding-vision-plus, qwen3-rerank, etc.",
            "defaultUrls": {
                "chat": "https://dashscope.aliyuncs.com/compatible-mode/v1",
                "embedding": "https://dashscope.aliyuncs.com/compatible-mode/v1",
                "rerank": "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
            },
            "modelTypes": ["chat", "embedding", "rerank", "vllm"]
        },
        {
            "value": "zhipu",
            "label": "智谱 BigModel",
            "description": "glm-4.7, embedding-3, rerank, etc.",
            "defaultUrls": {
                "chat": "https://open.bigmodel.cn/api/paas/v4",
                "embedding": "https://open.bigmodel.cn/api/paas/v4/embeddings",
                "rerank": "https://open.bigmodel.cn/api/paas/v4/rerank"
            },
            "modelTypes": ["chat", "embedding", "rerank", "vllm"]
        }
    ]
}
```

## POST `/models` - 创建模型

### 创建对话模型（KnowledgeQA）

**本地 Ollama 模型**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "qwen3:8b",
    "type": "KnowledgeQA",
    "source": "local",
    "description": "LLM Model for Knowledge QA",
    "parameters": {
        "base_url": "",
        "api_key": ""
    }
}'
```

**远程 API 模型（指定服务商）**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "qwen-plus",
    "type": "KnowledgeQA",
    "source": "remote",
    "description": "阿里云 Qwen 大模型",
    "parameters": {
        "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "api_key": "sk-your-dashscope-api-key",
        "provider": "aliyun"
    }
}'
```

### 创建嵌入模型（Embedding）

**本地 Ollama 模型**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
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
    }
}'
```

**远程 API 模型（阿里云 DashScope）**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "text-embedding-v3",
    "type": "Embedding",
    "source": "remote",
    "description": "阿里云通义千问 Embedding 模型",
    "parameters": {
        "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "api_key": "sk-your-dashscope-api-key",
        "provider": "aliyun",
        "embedding_parameters": {
            "dimension": 1024,
            "truncate_prompt_tokens": 0
        }
    }
}'
```

**远程 API 模型（Jina AI）**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "jina-embeddings-v3",
    "type": "Embedding",
    "source": "remote",
    "description": "Jina AI Embedding 模型",
    "parameters": {
        "base_url": "https://api.jina.ai/v1",
        "api_key": "jina_your_api_key",
        "provider": "jina",
        "embedding_parameters": {
            "dimension": 1024,
            "truncate_prompt_tokens": 0
        }
    }
}'
```

### 创建排序模型（Rerank）

**远程 API 模型（阿里云 DashScope）**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "gte-rerank",
    "type": "Rerank",
    "source": "remote",
    "description": "阿里云 GTE Rerank 模型",
    "parameters": {
        "base_url": "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank",
        "api_key": "sk-your-dashscope-api-key",
        "provider": "aliyun"
    }
}'
```

**远程 API 模型（Jina AI）**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "jina-reranker-v2-base-multilingual",
    "type": "Rerank",
    "source": "remote",
    "description": "Jina AI Rerank 模型",
    "parameters": {
        "base_url": "https://api.jina.ai/v1",
        "api_key": "jina_your_api_key",
        "provider": "jina"
    }
}'
```

### 创建视觉模型（VLLM）

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "qwen-vl-plus",
    "type": "VLLM",
    "source": "remote",
    "description": "阿里云通义千问视觉模型",
    "parameters": {
        "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
        "api_key": "sk-your-dashscope-api-key",
        "provider": "aliyun"
    }
}'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "09c5a1d6-ee8b-4657-9a17-d3dcbd5c70cb",
        "tenant_id": 1,
        "name": "text-embedding-v3",
        "type": "Embedding",
        "source": "remote",
        "description": "阿里云通义千问 Embedding 模型",
        "parameters": {
            "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
            "api_key": "sk-***",
            "provider": "aliyun",
            "embedding_parameters": {
                "dimension": 1024,
                "truncate_prompt_tokens": 0
            }
        },
        "is_default": false,
        "status": "active",
        "created_at": "2025-08-12T10:39:01.454591766+08:00",
        "updated_at": "2025-08-12T10:39:01.454591766+08:00",
        "deleted_at": null
    }
}
```

## GET `/models` - 获取模型列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "tenant_id": 1,
            "name": "text-embedding-v3",
            "type": "Embedding",
            "source": "remote",
            "description": "阿里云通义千问 Embedding 模型",
            "parameters": {
                "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
                "api_key": "sk-***",
                "provider": "aliyun",
                "embedding_parameters": {
                    "dimension": 1024,
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
            "name": "qwen-plus",
            "type": "KnowledgeQA",
            "source": "remote",
            "description": "阿里云 Qwen 大模型",
            "parameters": {
                "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
                "api_key": "sk-***",
                "provider": "aliyun",
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
    ]
}
```

## GET `/models/:id` - 获取模型详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/models/dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "tenant_id": 1,
        "name": "text-embedding-v3",
        "type": "Embedding",
        "source": "remote",
        "description": "阿里云通义千问 Embedding 模型",
        "parameters": {
            "base_url": "https://dashscope.aliyuncs.com/compatible-mode/v1",
            "api_key": "sk-***",
            "provider": "aliyun",
            "embedding_parameters": {
                "dimension": 1024,
                "truncate_prompt_tokens": 0
            }
        },
        "is_default": true,
        "status": "active",
        "created_at": "2025-08-11T20:10:41.813832+08:00",
        "updated_at": "2025-08-11T20:10:41.822354+08:00",
        "deleted_at": null
    }
}
```

## PUT `/models/:id` - 更新模型

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/models/8fdc464d-8eaa-44d4-a85b-094b28af5330' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "gte-rerank-v2",
    "description": "阿里云 GTE Rerank 模型 V2",
    "parameters": {
        "base_url": "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank",
        "api_key": "sk-your-new-api-key",
        "provider": "aliyun"
    }
}'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "8fdc464d-8eaa-44d4-a85b-094b28af5330",
        "tenant_id": 1,
        "name": "gte-rerank-v2",
        "type": "Rerank",
        "source": "remote",
        "description": "阿里云 GTE Rerank 模型 V2",
        "parameters": {
            "base_url": "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank",
            "api_key": "sk-***",
            "provider": "aliyun",
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
    }
}
```

## DELETE `/models/:id` - 删除模型

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/models/8fdc464d-8eaa-44d4-a85b-094b28af5330' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "message": "Model deleted"
}
```

## 参数说明

### ModelType (模型类型)

| 值           | 说明         | 用途                           |
| ------------ | ------------ | ------------------------------ |
| KnowledgeQA  | 对话模型     | 知识库问答、对话生成           |
| Embedding    | 嵌入模型     | 文本向量化、知识库检索         |
| Rerank       | 排序模型     | 检索结果重排序、相关性优化     |
| VLLM         | 视觉语言模型 | 多模态分析、图文理解           |

### ModelSource (模型来源)

| 值       | 说明       | 配置要求                       |
| -------- | ---------- | ------------------------------ |
| local    | 本地模型   | 需要已安装 Ollama 并拉取模型   |
| remote   | 远程 API   | 需要提供 `base_url` 和 `api_key` |

### Parameters (模型参数)

| 字段                 | 类型   | 说明                                         |
| -------------------- | ------ | -------------------------------------------- |
| base_url             | string | API 服务地址（远程模型必填）                 |
| api_key              | string | API 密钥（远程模型必填）                     |
| provider             | string | 服务商标识（可选，用于选择特定的 API 适配器）|
| embedding_parameters | object | Embedding 模型专用参数                       |
| extra_config         | object | 服务商特定的额外配置                         |

### EmbeddingParameters (嵌入参数)

| 字段                   | 类型 | 说明                       |
| ---------------------- | ---- | -------------------------- |
| dimension              | int  | 向量维度（如：768, 1024）  |
| truncate_prompt_tokens | int  | 截断 Token 数（0 表示不截断）|
