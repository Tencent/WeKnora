# 智能体（Agent）管理 API

[返回目录](./README.md)

## 概述

智能体 API 用于管理自定义智能体（Custom Agent）。系统提供了内置智能体，同时支持用户创建自定义智能体来满足不同的业务场景需求。

### 内置智能体

系统默认提供以下内置智能体：

| ID | 名称 | 描述 | 模式 |
|----|------|------|------|
| `builtin-quick-answer` | 快速问答 | 基于知识库的 RAG 问答，快速准确地回答问题 | quick-answer |
| `builtin-smart-reasoning` | 智能推理 | ReAct 推理框架，支持多步思考和工具调用 | smart-reasoning |
| `builtin-data-analyst` | 数据分析师 | 专业数据分析智能体，支持 CSV/Excel 文件的 SQL 查询与统计分析 | smart-reasoning |

### 智能体模式

| 模式 | 说明 |
|------|------|
| `quick-answer` | RAG 模式，快速问答，直接基于知识库检索结果生成回答 |
| `smart-reasoning` | ReAct 模式，支持多步推理和工具调用 |

## API 列表

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | `/agents` | 创建智能体 |
| GET | `/agents` | 获取智能体列表 |
| GET | `/agents/:id` | 获取智能体详情 |
| PUT | `/agents/:id` | 更新智能体 |
| DELETE | `/agents/:id` | 删除智能体 |
| POST | `/agents/:id/copy` | 复制智能体 |
| GET | `/agents/placeholders` | 获取占位符定义 |

---

## POST `/agents` - 创建智能体

创建新的自定义智能体。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents' \
--header 'X-API-Key: your_api_key' \
--header 'Content-Type: application/json' \
--data '{
    "name": "我的智能体",
    "description": "自定义智能体描述",
    "avatar": "🤖",
    "config": {
        "agent_mode": "smart-reasoning",
        "system_prompt": "你是一个专业的助手...",
        "temperature": 0.7,
        "max_iterations": 10,
        "kb_selection_mode": "all",
        "web_search_enabled": true,
        "multi_turn_enabled": true,
        "history_turns": 5
    }
}'
```

**请求参数**:

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | 智能体名称 |
| `description` | string | 否 | 智能体描述 |
| `avatar` | string | 否 | 智能体头像（emoji 或图标名称） |
| `config` | object | 否 | 智能体配置，详见 [配置参数](#配置参数) |

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "我的智能体",
        "description": "自定义智能体描述",
        "avatar": "🤖",
        "is_builtin": false,
        "tenant_id": 1,
        "created_by": "user-123",
        "config": {
            "agent_mode": "smart-reasoning",
            "system_prompt": "你是一个专业的助手...",
            "temperature": 0.7,
            "max_iterations": 10
        },
        "created_at": "2025-01-19T10:00:00Z",
        "updated_at": "2025-01-19T10:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误 | 说明 |
|--------|--------|------|------|
| 400 | 1000 | Bad Request | 请求参数错误或智能体名称为空 |
| 500 | 1007 | Internal Server Error | 服务器内部错误 |

---

## GET `/agents` - 获取智能体列表

获取当前租户的所有智能体，包括内置智能体和自定义智能体。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "id": "builtin-quick-answer",
            "name": "快速问答",
            "description": "基于知识库的 RAG 问答，快速准确地回答问题",
            "avatar": "💬",
            "is_builtin": true,
            "tenant_id": 10000,
            "created_by": "",
            "config": {
                "agent_mode": "quick-answer",
                "system_prompt": "你是一个专业的智能信息检索助手，名为WeKnora。你犹如专业的高级秘书，依据检索到的信息回答用户问题，不能利用任何先验知识。\n当用户提出问题时，助手会基于特定的信息进行解答。助手首先在心中思考推理过程，然后向用户提供答案。\n",
                "context_template": "...",
                "model_id": "...",
                "rerank_model_id": "",
                "temperature": 0.3,
                "max_completion_tokens": 2048,
                "max_iterations": 10,
                "allowed_tools": [],
                "reflection_enabled": false,
                "mcp_selection_mode": "",
                "mcp_services": null,
                "kb_selection_mode": "all",
                "knowledge_bases": [],
                "supported_file_types": null,
                "faq_priority_enabled": false,
                "faq_direct_answer_threshold": 0,
                "faq_score_boost": 0,
                "web_search_enabled": false,
                "web_search_max_results": 5,
                "multi_turn_enabled": true,
                "history_turns": 5,
                "embedding_top_k": 10,
                "keyword_threshold": 0.3,
                "vector_threshold": 0.5,
                "rerank_top_k": 5,
                "rerank_threshold": 0.5,
                "enable_query_expansion": true,
                "enable_rewrite": true,
                "rewrite_prompt_system": "...",
                "rewrite_prompt_user": "...",
                "fallback_strategy": "fixed",
                "fallback_response": "...",
                "fallback_prompt": "..."
            },
            "created_at": "2025-12-29T20:06:01.696308+08:00",
            "updated_at": "2025-12-29T20:06:01.696308+08:00",
            "deleted_at": null
        },
        {
            "id": "builtin-smart-reasoning",
            "name": "智能推理",
            "description": "ReAct 推理框架，支持多步思考和工具调用",
            "is_builtin": true,
            "config": {
                "agent_mode": "smart-reasoning"
  
            }
        },
        {
            "id": "550e8400-e29b-41d4-a716-446655440000",
            "name": "我的智能体",
            "description": "自定义智能体描述",
            "is_builtin": false,
            "config": {
                "agent_mode": "smart-reasoning"
            }
        }
    ]
}
```

---

## GET `/agents/:id` - 获取智能体详情

根据 ID 获取智能体的详细信息。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/builtin-quick-answer' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "builtin-quick-answer",
        "name": "快速问答",
        "description": "基于知识库的 RAG 问答，快速准确地回答问题",
        "is_builtin": true,
        "tenant_id": 1,
        "config": {
            "agent_mode": "quick-answer",
            "system_prompt": "",
            "context_template": "请根据以下参考资料回答用户问题...",
            "temperature": 0.7,
            "max_completion_tokens": 2048,
            "kb_selection_mode": "all",
            "web_search_enabled": true,
            "multi_turn_enabled": true,
            "history_turns": 5
        },
        "created_at": "2025-01-01T00:00:00Z",
        "updated_at": "2025-01-01T00:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误 | 说明 |
|--------|--------|------|------|
| 400 | 1000 | Bad Request | 智能体 ID 为空 |
| 404 | 1003 | Not Found | 智能体不存在 |
| 500 | 1007 | Internal Server Error | 服务器内部错误 |

---

## PUT `/agents/:id` - 更新智能体

更新智能体的名称、描述和配置。内置智能体不可修改。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/agents/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: your_api_key' \
--header 'Content-Type: application/json' \
--data '{
    "name": "更新后的智能体",
    "description": "更新后的描述",
    "config": {
        "agent_mode": "smart-reasoning",
        "temperature": 0.8,
        "max_iterations": 20
    }
}'
```

**请求参数**:

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 否 | 智能体名称 |
| `description` | string | 否 | 智能体描述 |
| `avatar` | string | 否 | 智能体头像 |
| `config` | object | 否 | 智能体配置 |

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "更新后的智能体",
        "description": "更新后的描述",
        "config": {
            "agent_mode": "smart-reasoning",
            "temperature": 0.8,
            "max_iterations": 20
        },
        "updated_at": "2025-01-19T11:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误 | 说明 |
|--------|--------|------|------|
| 400 | 1000 | Bad Request | 请求参数错误或智能体名称为空 |
| 403 | 1002 | Forbidden | 无法修改内置智能体的基本信息 |
| 404 | 1003 | Not Found | 智能体不存在 |
| 500 | 1007 | Internal Server Error | 服务器内部错误 |

---

## DELETE `/agents/:id` - 删除智能体

删除指定的自定义智能体。内置智能体不可删除。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/agents/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "message": "Agent deleted successfully"
}
```

**错误响应**:

| 状态码 | 错误码 | 错误 | 说明 |
|--------|--------|------|------|
| 400 | 1000 | Bad Request | 智能体 ID 为空 |
| 403 | 1002 | Forbidden | 无法删除内置智能体 |
| 404 | 1003 | Not Found | 智能体不存在 |
| 500 | 1007 | Internal Server Error | 服务器内部错误 |

---

## POST `/agents/:id/copy` - 复制智能体

复制指定的智能体，创建一个新的副本。支持复制内置智能体。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/agents/builtin-smart-reasoning/copy' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "660e8400-e29b-41d4-a716-446655440001",
        "name": "智能推理 (副本)",
        "description": "ReAct 推理框架，支持多步思考和工具调用",
        "is_builtin": false,
        "config": {
            "agent_mode": "smart-reasoning",
            "max_iterations": 50
        },
        "created_at": "2025-01-19T12:00:00Z",
        "updated_at": "2025-01-19T12:00:00Z"
    }
}
```

**错误响应**:

| 状态码 | 错误码 | 错误 | 说明 |
|--------|--------|------|------|
| 400 | 1000 | Bad Request | 智能体 ID 为空 |
| 404 | 1003 | Not Found | 智能体不存在 |
| 500 | 1007 | Internal Server Error | 服务器内部错误 |

---

## GET `/agents/placeholders` - 获取占位符定义

获取所有可用的提示词占位符定义，按字段类型分组。这些占位符可用于系统提示词和上下文模板中。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/placeholders' \
--header 'X-API-Key: your_api_key'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "all": [...],
        "system_prompt": [...],
        "agent_system_prompt": [...],
        "context_template": [...],
        "rewrite_system_prompt": [...],
        "rewrite_prompt": [...],
        "fallback_prompt": [...]
    }
}
```

---

## 配置参数

智能体的 `config` 对象支持以下配置项：

### 基础设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `agent_mode` | string | - | 智能体模式：`quick-answer`（RAG）或 `smart-reasoning`（ReAct） |
| `system_prompt` | string | - | 系统提示词，支持使用占位符 |
| `system_prompt_id` | string | - | 系统提示词模板 ID（引用 `prompt_templates/` YAML 文件中的模板） |
| `context_template` | string | - | 上下文模板（仅 quick-answer 模式使用） |
| `context_template_id` | string | - | 上下文模板 ID（引用 `prompt_templates/` YAML 文件中的模板） |

### 模型设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `model_id` | string | - | 对话模型 ID |
| `rerank_model_id` | string | - | 重排序模型 ID |
| `temperature` | float | 0.7 | 温度参数（0-1） |
| `max_completion_tokens` | int | 2048 | 最大生成 token 数 |
| `thinking` | *bool | nil | 是否启用思考模式（适用于支持扩展思考的模型） |

### Agent 模式设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `max_iterations` | int | 10 | ReAct 最大迭代次数 |
| `allowed_tools` | []string | - | 允许使用的工具列表 |
| `mcp_selection_mode` | string | - | MCP 服务选择模式：`all`/`selected`/`none` |
| `mcp_services` | []string | - | 选中的 MCP 服务 ID 列表 |
| `skills_selection_mode` | string | - | Skills 选择模式：`all`/`selected`/`none` |
| `selected_skills` | []string | - | 选中的 Skill 名称列表（mode 为 `selected` 时） |

### 知识库设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `kb_selection_mode` | string | - | 知识库选择模式：`all`/`selected`/`none` |
| `knowledge_bases` | []string | - | 关联的知识库 ID 列表 |
| `retrieve_kb_only_when_mentioned` | bool | false | 仅在用户通过 @ 显式提及时才检索知识库 |
| `supported_file_types` | []string | - | 支持的文件类型（如 `["csv", "xlsx"]`） |

### 图片上传 / 多模态设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `image_upload_enabled` | bool | false | 是否允许上传图片 |
| `vlm_model_id` | string | - | 图片分析所用的 VLM 模型 ID |
| `image_storage_provider` | string | - | 图片存储提供者：`local`/`minio`/`cos`/`tos`，为空使用全局默认 |

### FAQ 策略设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `faq_priority_enabled` | bool | true | FAQ 优先策略开关 |
| `faq_direct_answer_threshold` | float | 0.9 | FAQ 直接回答阈值 |
| `faq_score_boost` | float | 1.2 | FAQ 分数加成系数 |

### 网络搜索设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `web_search_enabled` | bool | true | 是否启用网络搜索 |
| `web_search_max_results` | int | 5 | 网络搜索最大结果数 |
| `web_search_provider_id` | string | - | 网络搜索提供者 ID，为空使用租户默认提供者 |
| `web_fetch_enabled` | bool | false | 是否自动获取重排后的搜索结果页面全文 |
| `web_fetch_top_n` | int | 3 | 重排后获取全文的最大页面数 |

### 多轮对话设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `multi_turn_enabled` | bool | true | 是否启用多轮对话 |
| `history_turns` | int | 5 | 保留的历史轮次数 |

### 检索策略设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `embedding_top_k` | int | 10 | 向量检索 TopK |
| `keyword_threshold` | float | 0.3 | 关键词检索阈值 |
| `vector_threshold` | float | 0.5 | 向量检索阈值 |
| `rerank_top_k` | int | 5 | 重排序 TopK |
| `rerank_threshold` | float | 0.5 | 重排序阈值 |

### 推荐问题设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `suggested_prompts` | []string | - | 推荐问题列表，用于在前端对话面板展示快捷提问 |

### 高级设置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enable_query_expansion` | bool | true | 是否启用查询扩展 |
| `enable_rewrite` | bool | true | 是否启用多轮对话查询改写 |
| `rewrite_prompt_system` | string | - | 改写系统提示词 |
| `rewrite_prompt_user` | string | - | 改写用户提示词模板 |
| `fallback_strategy` | string | `model` | 回退策略：`fixed`（固定回复）或 `model`（模型生成）；未设置时在服务端默认为 `model` |
| `fallback_response` | string | - | 固定回退回复（`fallback_strategy` 为 `fixed` 时使用） |
| `fallback_prompt` | string | - | 回退提示词（`fallback_strategy` 为 `model` 时使用） |

---

## 使用 Agent 进行问答

创建或获取智能体后，可以通过 `/agent-chat/:session_id` 接口使用智能体进行问答。详情请参考 [聊天功能 API](./chat.md)。

在问答请求中使用 `agent_id` 参数指定要使用的智能体：

```curl
curl --location 'http://localhost:8080/api/v1/agent-chat/session-123' \
--header 'X-API-Key: your_api_key' \
--header 'Content-Type: application/json' \
--data '{
    "query": "帮我分析一下这份数据",
    "agent_enabled": true,
    "agent_id": "builtin-data-analyst"
}'
```
