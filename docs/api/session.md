# 会话管理 API

[返回目录](./README.md)

| 方法   | 路径                                    | 描述                  |
| ------ | --------------------------------------- | --------------------- |
| POST   | `/sessions`                             | 创建会话              |
| GET    | `/sessions/:id`                         | 获取会话详情          |
| GET    | `/sessions`                             | 获取租户的会话列表    |
| PUT    | `/sessions/:id`                         | 更新会话              |
| DELETE | `/sessions/:id`                         | 删除会话              |
| POST   | `/sessions/:session_id/generate_title`  | 生成会话标题          |
| POST   | `/sessions/:session_id/stop`            | 停止会话              |
| GET    | `/sessions/continue-stream/:session_id` | 继续未完成的会话      |


## POST `/sessions` - 创建会话

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
        "agent_config": null,
        "context_config": null,
        "created_at": "2025-08-12T12:26:19.611616669+08:00",
        "updated_at": "2025-08-12T12:26:19.611616919+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## GET `/sessions/:id` - 获取会话详情

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
        "agent_config": null,
        "context_config": null,
        "created_at": "2025-08-12T10:24:38.308596+08:00",
        "updated_at": "2025-08-12T10:25:41.317761+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## GET `/sessions?page=&page_size=` - 获取租户的会话列表

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

## PUT `/sessions/:id` - 更新会话

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

## DELETE `/sessions/:id` - 删除会话

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

## POST `/sessions/:session_id/generate_title` - 生成会话标题

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

## POST `/sessions/:session_id/stop` - 停止会话

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/sessions/7c966c74-610e-4516-8d5b-05e14b2e4ee0/stop' \
--header 'X-API-Key: sk-An-W8T4tdZDbWKJgfwgdea5rS8ue_mRhCTZ8Smhnvku-bWEE' \
--header 'Content-Type: application/json' \
--data '{"message_id":"ebbf7e53-dfe6-44d5-882f-36a4104910b5"}'
```

**响应**:

```json
{
    "message": "Session stopped successfully",
    "success": true
}
```

## GET `/sessions/continue-stream/:session_id` - 继续未完成的会话

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
