# 模型管理 API

[返回目录](./README.md)

模型管理接口用于维护当前租户下可用的 LLM / Embedding / Rerank / VLLM / ASR 模型配置。

| 方法   | 路径                | 描述                  |
| ------ | ------------------- | --------------------- |
| GET    | `/models/providers` | 获取模型服务商列表    |
| POST   | `/models`           | 创建模型              |
| GET    | `/models`           | 获取模型列表          |
| GET    | `/models/:id`       | 获取模型详情          |
| PUT    | `/models/:id`       | 更新模型              |
| DELETE | `/models/:id`       | 删除模型              |

## 服务商支持 (Provider Support)

WeKnora 支持多种主流 AI 模型服务商，在创建模型时可通过 `parameters.provider` 字段指定服务商类型以获得更好的兼容性。

### 支持的服务商列表

| 服务商标识     | 名称                         | 支持的模型类型                  |
| -------------- | ---------------------------- | ------------------------------- |
| `generic`      | 自定义 (OpenAI 兼容接口)     | Chat, Embedding, Rerank, VLLM   |
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

> 实际可用的服务商以 `GET /models/providers` 返回为准。

## GET `/models/providers` - 获取模型服务商列表

根据模型类型获取支持的服务商列表及配置信息（系统级元数据，与租户无关）。

**查询参数**:

| 字段       | 类型   | 必填 | 说明                                                |
| ---------- | ------ | ---- | --------------------------------------------------- |
| model_type | string | 否   | 模型类型，可选值：`chat` / `embedding` / `rerank` / `vllm` / `asr`；省略则返回全部 |

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

为当前租户创建一个新的模型配置。

**参数说明（请求体）**:

| 字段        | 类型   | 必填 | 说明                                                            |
| ----------- | ------ | ---- | --------------------------------------------------------------- |
| name        | string | 是   | 模型名称（远程模型对应服务商的 model id，本地模型为 Ollama tag）|
| type        | string | 是   | 模型类型，可选值：`KnowledgeQA` / `Embedding` / `Rerank` / `VLLM` / `ASR` |
| source      | string | 是   | 模型来源，可选值：`local` / `remote`                            |
| description | string | 否   | 模型描述                                                        |
| parameters  | object | 是   | 模型参数，详见下方 [Parameters](#parameters-模型参数)           |

> 当 `parameters.base_url` 不为空时，后端会执行 SSRF 校验，校验失败将返回 400。

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

返回当前租户下的所有模型。内置模型（`is_builtin = true`）的 `base_url` 与 `api_key` 会被清空以隐藏敏感信息。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/models' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key'
```

**响应**: `data` 为数组，每个元素的字段结构同 `POST /models` 响应。内置模型的 `base_url` 与 `api_key` 字段为空字符串。

## GET `/models/:id` - 获取模型详情

**路径参数**:

| 字段 | 类型   | 必填 | 说明     |
| ---- | ------ | ---- | -------- |
| id   | string | 是   | 模型 ID  |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/models/dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key'
```

**响应**: 字段结构同 `POST /models` 响应。404 表示模型不存在。

## PUT `/models/:id` - 更新模型

**路径参数**:

| 字段 | 类型   | 必填 | 说明     |
| ---- | ------ | ---- | -------- |
| id   | string | 是   | 模型 ID  |

**参数说明（请求体）**:

| 字段        | 类型   | 必填 | 说明                                                            |
| ----------- | ------ | ---- | --------------------------------------------------------------- |
| name        | string | 否   | 模型名称（为空字符串时保留原值）                                |
| description | string | 否   | 模型描述（始终覆盖，传空字符串会清空）                          |
| type        | string | 否   | 模型类型，取值同创建接口                                        |
| source      | string | 否   | 模型来源，取值同创建接口                                        |
| parameters  | object | 否   | 模型参数；`parameter_size` 由后端管理，请求中无需提供；`extra_config` 为空时会沿用旧值 |

> 同样会对 `parameters.base_url` 做 SSRF 校验，失败时返回 400。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/models/8fdc464d-8eaa-44d4-a85b-094b28af5330' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: your_api_key' \
--data '{
    "name": "gte-rerank-v2",
    "type": "Rerank",
    "source": "remote",
    "description": "阿里云 GTE Rerank 模型 V2",
    "parameters": {
        "base_url": "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank",
        "api_key": "sk-your-new-api-key",
        "provider": "aliyun"
    }
}'
```

**响应**: 字段结构同 `POST /models` 响应，返回更新后的完整模型对象。

## DELETE `/models/:id` - 删除模型

**路径参数**:

| 字段 | 类型   | 必填 | 说明     |
| ---- | ------ | ---- | -------- |
| id   | string | 是   | 模型 ID  |

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

404 表示模型不存在。

## 参数说明

### ModelType (模型类型)

| 值          | 前端别名    | 说明         | 用途                           |
| ----------- | ----------- | ------------ | ------------------------------ |
| KnowledgeQA | `chat`      | 对话模型     | 知识库问答、对话生成           |
| Embedding   | `embedding` | 嵌入模型     | 文本向量化、知识库检索         |
| Rerank      | `rerank`    | 排序模型     | 检索结果重排序、相关性优化     |
| VLLM        | `vllm`      | 视觉语言模型 | 多模态分析、图文理解           |
| ASR         | `asr`       | 语音识别模型 | 音频转写                       |

> 创建/更新接口请求体的 `type` 字段使用第一列的后端枚举值（如 `KnowledgeQA`）；`GET /models/providers?model_type=` 查询参数使用第二列的前端别名（如 `chat`）。

### ModelSource (模型来源)

| 值       | 说明       | 配置要求                         |
| -------- | ---------- | -------------------------------- |
| local    | 本地模型   | 需要已安装 Ollama 并拉取模型     |
| remote   | 远程 API   | 需要提供 `base_url` 和 `api_key` |

### Parameters (模型参数)

| 字段                 | 类型              | 必填 | 说明                                                       |
| -------------------- | ----------------- | ---- | ---------------------------------------------------------- |
| base_url             | string            | 否   | API 服务地址；远程模型必填，会经过 SSRF 校验               |
| api_key              | string            | 否   | API 密钥；远程模型必填，存储时使用 AES-256 加密            |
| provider             | string            | 否   | 服务商标识（见上方支持列表），用于选择特定的 API 适配器    |
| interface_type       | string            | 否   | 接口风格标识（OpenAI 兼容请留空）                          |
| embedding_parameters | object            | 否   | Embedding 模型专用参数，见下方                             |
| parameter_size       | string            | 否   | 模型参数规模（如 `7B`/`13B`/`70B`），通常由后端写入        |
| extra_config         | object<string,string> | 否 | 服务商特定的额外配置                                       |
| custom_headers       | object<string,string> | 否 | 调用上游 API 时附加的自定义 HTTP 头；保留头会被忽略        |
| supports_vision      | bool              | 否   | 模型是否支持图像/多模态输入                                |

### EmbeddingParameters (嵌入参数)

| 字段                   | 类型 | 必填 | 说明                            |
| ---------------------- | ---- | ---- | ------------------------------- |
| dimension              | int  | 否   | 向量维度（如 768、1024）        |
| truncate_prompt_tokens | int  | 否   | 截断 Token 数（0 表示不截断）   |
