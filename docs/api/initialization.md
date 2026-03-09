# 初始化配置 API

[返回目录](./README.md)

| 方法   | 路径                                              | 描述                       |
| ------ | ------------------------------------------------- | -------------------------- |
| GET    | `/initialization/config/:kb_id`                   | 获取知识库初始化配置       |
| POST   | `/initialization/initialize/:kb_id`               | 初始化知识库模型配置       |
| PUT    | `/initialization/config/:kb_id`                   | 更新知识库模型配置         |
| GET    | `/initialization/ollama/status`                   | 检查 Ollama 状态           |
| GET    | `/initialization/ollama/models`                   | 获取本地 Ollama 模型列表   |
| POST   | `/initialization/ollama/models/check`             | 检查 Ollama 模型是否可用   |
| POST   | `/initialization/ollama/models/download`          | 下载 Ollama 模型           |
| GET    | `/initialization/ollama/download/progress/:task_id` | 获取下载进度             |
| GET    | `/initialization/ollama/download/tasks`           | 获取所有下载任务           |
| POST   | `/initialization/remote/check`                    | 检查远程模型 API           |
| POST   | `/initialization/embedding/test`                  | 测试嵌入模型               |
| POST   | `/initialization/rerank/check`                    | 检查重排序模型             |
| POST   | `/initialization/multimodal/test`                 | 测试多模态模型             |
| POST   | `/initialization/extract/text-relation`           | 提取文本关系               |

## GET `/initialization/config/:kb_id` - 获取知识库初始化配置

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/config/kb-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "chat_model_id": "model-00000001",
        "embedding_model_id": "model-00000002",
        "rerank_model_id": "model-00000003",
        "multimodal_id": "model-00000004"
    },
    "success": true
}
```

## POST `/initialization/initialize/:kb_id` - 初始化知识库模型配置

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/initialize/kb-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "chat_model_id": "model-00000001",
    "embedding_model_id": "model-00000002",
    "rerank_model_id": "model-00000003",
    "multimodal_id": "model-00000004"
}'
```

**响应**:

```json
{
    "success": true
}
```

## PUT `/initialization/config/:kb_id` - 更新知识库模型配置

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/initialization/config/kb-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "chat_model_id": "model-00000010",
    "embedding_model_id": "model-00000002"
}'
```

**响应**:

```json
{
    "success": true
}
```

## GET `/initialization/ollama/status` - 检查 Ollama 状态

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/ollama/status' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "available": true
    },
    "success": true
}
```

## GET `/initialization/ollama/models` - 获取本地 Ollama 模型列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/ollama/models' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "name": "llama3:8b",
            "size": 4661211648,
            "modified_at": "2025-08-10T15:30:00+08:00"
        },
        {
            "name": "nomic-embed-text:latest",
            "size": 274302976,
            "modified_at": "2025-08-11T09:00:00+08:00"
        }
    ],
    "success": true
}
```

## POST `/initialization/ollama/models/check` - 检查 Ollama 模型是否可用

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/ollama/models/check' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "models": ["llama3:8b", "nomic-embed-text:latest", "mistral:7b"]
}'
```

**响应**:

```json
{
    "data": {
        "llama3:8b": true,
        "nomic-embed-text:latest": true,
        "mistral:7b": false
    },
    "success": true
}
```

## POST `/initialization/ollama/models/download` - 下载 Ollama 模型

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/ollama/models/download' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "model": "mistral:7b"
}'
```

**响应**:

```json
{
    "data": {
        "id": "task-00000001",
        "modelName": "mistral:7b",
        "status": "downloading",
        "progress": 0,
        "message": "开始下载",
        "startTime": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

## GET `/initialization/ollama/download/progress/:task_id` - 获取下载进度

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/ollama/download/progress/task-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "task-00000001",
        "modelName": "mistral:7b",
        "status": "downloading",
        "progress": 45.6,
        "message": "正在下载 2.1GB / 4.6GB",
        "startTime": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

## GET `/initialization/ollama/download/tasks` - 获取所有下载任务

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/ollama/download/tasks' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "task-00000001",
            "modelName": "mistral:7b",
            "status": "completed",
            "progress": 100,
            "message": "下载完成",
            "startTime": "2025-08-12T10:00:00+08:00",
            "endTime": "2025-08-12T10:15:00+08:00"
        },
        {
            "id": "task-00000002",
            "modelName": "llama3:70b",
            "status": "downloading",
            "progress": 30.2,
            "message": "正在下载 12.5GB / 41.4GB",
            "startTime": "2025-08-12T10:20:00+08:00"
        }
    ],
    "success": true
}
```

## POST `/initialization/remote/check` - 检查远程模型 API

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/remote/check' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "api_url": "https://api.openai.com/v1",
    "api_key": "sk-xxxxx",
    "model": "gpt-4o"
}'
```

**响应**:

```json
{
    "data": {
        "success": true,
        "message": "模型可用"
    },
    "success": true
}
```

## POST `/initialization/embedding/test` - 测试嵌入模型

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/embedding/test' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "api_url": "https://api.openai.com/v1",
    "api_key": "sk-xxxxx",
    "model": "text-embedding-3-small"
}'
```

**响应**:

```json
{
    "data": {
        "success": true,
        "message": "嵌入模型测试通过"
    },
    "success": true
}
```

## POST `/initialization/rerank/check` - 检查重排序模型

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/rerank/check' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "api_url": "https://api.cohere.ai/v1",
    "api_key": "sk-xxxxx",
    "model": "rerank-english-v3.0"
}'
```

**响应**:

```json
{
    "data": {
        "success": true,
        "message": "重排序模型可用"
    },
    "success": true
}
```

## POST `/initialization/multimodal/test` - 测试多模态模型

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/multimodal/test' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "api_url": "https://api.openai.com/v1",
    "api_key": "sk-xxxxx",
    "model": "gpt-4o"
}'
```

**响应**:

```json
{
    "data": {
        "success": true,
        "message": "多模态模型测试通过"
    },
    "success": true
}
```

## POST `/initialization/extract/text-relation` - 提取文本关系

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/initialization/extract/text-relation' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "text": "WeKnora 是一个知识管理平台，支持多种文档格式的解析和检索。",
    "model_id": "model-00000001"
}'
```

**响应**:

```json
{
    "data": {
        "entities": [
            {"name": "WeKnora", "type": "Product"},
            {"name": "知识管理平台", "type": "Concept"}
        ],
        "relations": [
            {
                "source": "WeKnora",
                "target": "知识管理平台",
                "relation": "is_a"
            }
        ]
    },
    "success": true
}
```
