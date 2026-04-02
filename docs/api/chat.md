# 聊天功能 API

[返回目录](./README.md)

| 方法 | 路径                          | 描述                     |
| ---- | ----------------------------- | ------------------------ |
| POST | `/knowledge-chat/:session_id` | 基于知识库的问答         |
| POST | `/agent-chat/:session_id`     | 基于 Agent 的智能问答    |
| POST | `/knowledge-search`           | 基于知识库的搜索知识     |

## POST `/knowledge-chat/:session_id` - 基于知识库的问答

基于知识库的 RAG 问答，支持 SSE 流式响应。

**请求参数**：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | 是 | 查询文本 |
| `knowledge_base_ids` | string[] | 否 | 知识库 ID 列表 |
| `knowledge_ids` | string[] | 否 | 知识文件 ID 列表，指定具体文件进行检索 |
| `agent_id` | string | 否 | 自定义 Agent ID，指定使用的智能体 |
| `summary_model_id` | string | 否 | 覆盖默认的摘要模型 ID |
| `mentioned_items` | object[] | 否 | @提及的知识库和文件列表 |
| `disable_title` | bool | 否 | 是否禁用自动标题生成（默认 false） |
| `enable_memory` | bool | 否 | 是否启用记忆功能 |
| `images` | object[] | 否 | 附带的图片（base64 格式），需要 Agent 启用图片上传 |
| `channel` | string | 否 | 来源渠道标识：`web`、`api`、`im`、`browser_extension` |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-chat/ceb9babb-1e30-41d7-817d-fd584954304b' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query": "彗尾的形状",
    "knowledge_base_ids": ["kb-00000001"],
    "agent_id": "builtin-quick-answer"
}'
```

**响应格式**:
服务器端事件流（Server-Sent Events，Content-Type: text/event-stream）

**响应**:

```
event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"references","content":"","done":false,"knowledge_references":[{"id":"c8347bef-...","content":"彗星xxx。","knowledge_id":"a6790b93-...","chunk_index":0,"knowledge_title":"彗星.txt","score":4.04,"match_type":3,"chunk_type":"text","knowledge_filename":"彗星.txt"}]}

event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"answer","content":"彗尾的形状主要表现为...","done":false,"knowledge_references":null}

event: message
data: {"id":"3475c004-0ada-4306-9d30-d7f5efce50d2","response_type":"answer","content":"","done":true,"knowledge_references":null}
```

## POST `/agent-chat/:session_id` - 基于 Agent 的智能问答

Agent 模式支持更智能的问答，包括工具调用、网络搜索、多知识库检索等能力。

**请求参数**：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | 是 | 查询文本 |
| `knowledge_base_ids` | string[] | 否 | 知识库 ID 列表，可动态指定本次查询使用的知识库 |
| `knowledge_ids` | string[] | 否 | 知识文件 ID 列表，可动态指定本次查询使用的具体文件 |
| `agent_enabled` | bool | 否 | 是否启用 Agent 模式（默认 false，优先使用 Agent 配置） |
| `agent_id` | string | 否 | 自定义 Agent ID，指定使用的智能体（支持共享 Agent） |
| `web_search_enabled` | bool | 否 | 是否启用网络搜索（默认 false） |
| `summary_model_id` | string | 否 | 覆盖默认的摘要模型 ID |
| `mentioned_items` | object[] | 否 | @提及的知识库和文件列表 |
| `disable_title` | bool | 否 | 是否禁用自动标题生成（默认 false） |
| `enable_memory` | bool | 否 | 是否启用记忆功能 |
| `images` | object[] | 否 | 附带的图片（base64 格式），需要 Agent 启用图片上传 |
| `channel` | string | 否 | 来源渠道标识：`web`、`api`、`im`、`browser_extension` |

**mentioned_items 结构**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 知识库或文件 ID |
| `name` | string | 显示名称 |
| `type` | string | 类型：`kb`（知识库）或 `file`（文件） |
| `kb_type` | string | 知识库类型：`document` 或 `faq`（仅 `type=kb` 时） |

**images 结构**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `data` | string | base64 编码的图片数据（`data:image/png;base64,...`） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agent-chat/ceb9babb-1e30-41d7-817d-fd584954304b' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query": "帮我查询今天的天气",
    "agent_enabled": true,
    "web_search_enabled": true,
    "knowledge_base_ids": ["kb-00000001"],
    "agent_id": "builtin-smart-reasoning",
    "mentioned_items": [
        {
            "id": "kb-00000001",
            "name": "天气知识库",
            "type": "kb",
            "kb_type": "document"
        }
    ]
}'
```

**响应格式**:
服务器端事件流（Server-Sent Events，Content-Type: text/event-stream）

**响应类型说明**：

| response_type | 描述 |
|---------------|------|
| `agent_query` | Agent 开始处理查询 |
| `thinking` | Agent 思考过程 |
| `tool_call` | 工具调用信息 |
| `tool_result` | 工具调用结果 |
| `references` | 知识库检索引用 |
| `answer` | 最终回答内容 |
| `reflection` | Agent 反思内容 |
| `session_title` | 自动生成的会话标题 |
| `error` | 错误信息 |

**响应示例**:

```
event: message
data: {"id":"req-001","response_type":"thinking","content":"用户想查询天气，我需要使用网络搜索工具...","done":false}

event: message
data: {"id":"req-001","response_type":"tool_call","content":"","done":false,"data":{"tool_name":"web_search","arguments":{"query":"今天天气"}}}

event: message
data: {"id":"req-001","response_type":"tool_result","content":"搜索结果：今天晴，气温25°C...","done":false}

event: message
data: {"id":"req-001","response_type":"answer","content":"根据查询结果，今天天气晴朗，气温约25°C。","done":false}

event: message
data: {"id":"req-001","response_type":"answer","content":"","done":true}
```
