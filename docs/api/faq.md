# FAQ管理 API

[返回目录](./README.md)

| 方法   | 路径                                        | 描述                     |
| ------ | ------------------------------------------- | ------------------------ |
| GET    | `/knowledge-bases/:id/faq/entries`          | 获取FAQ条目列表          |
| POST   | `/knowledge-bases/:id/faq/entries`          | 批量导入FAQ条目          |
| POST   | `/knowledge-bases/:id/faq/entry`            | 创建单个FAQ条目          |
| GET    | `/knowledge-bases/:id/faq/entries/:entry_id`| 获取单个FAQ条目          |
| PUT    | `/knowledge-bases/:id/faq/entries/:entry_id`| 更新单个FAQ条目          |
| POST   | `/knowledge-bases/:id/faq/entries/:entry_id/similar-questions` | 添加相似问题 |
| PUT    | `/knowledge-bases/:id/faq/entries/fields`   | 批量更新FAQ字段          |
| PUT    | `/knowledge-bases/:id/faq/entries/tags`     | 批量更新FAQ标签          |
| DELETE | `/knowledge-bases/:id/faq/entries`          | 批量删除FAQ条目          |
| POST   | `/knowledge-bases/:id/faq/search`           | 混合搜索FAQ              |
| GET    | `/knowledge-bases/:id/faq/entries/export`   | 导出FAQ条目(CSV)         |
| GET    | `/faq/import/progress/:task_id`             | 获取FAQ导入进度          |
| PUT    | `/knowledge-bases/:id/faq/import/last-result/display` | 更新导入结果显示状态 |

## GET `/knowledge-bases/:id/faq/entries` - 获取FAQ条目列表

**查询参数**:
- `page`: 页码（默认 1）
- `page_size`: 每页条数（默认 20）
- `tag_id`: 按标签ID筛选（可选）
- `keyword`: 关键字搜索（可选）
- `search_field`: 搜索字段（可选），可选值：
  - `standard_question`: 只搜索标准问题
  - `similar_questions`: 只搜索相似问法
  - `answers`: 只搜索答案
  - 留空或不传：搜索全部字段
- `sort_order`: 排序方式（可选），`asc` 表示按更新时间正序，默认按更新时间倒序

**请求**:

```curl
# 搜索全部字段
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries?page=1&page_size=10&keyword=密码' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'

# 只搜索标准问题
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries?keyword=密码&search_field=standard_question' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'

# 只搜索相似问法
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries?keyword=忘记&search_field=similar_questions' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'

# 只搜索答案
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries?keyword=点击&search_field=answers' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```json
{
    "data": {
        "total": 100,
        "page": 1,
        "page_size": 10,
        "data": [
            {
                "id": "faq-00000001",
                "chunk_id": "chunk-00000001",
                "knowledge_id": "knowledge-00000001",
                "knowledge_base_id": "kb-00000001",
                "tag_id": "tag-00000001",
                "is_enabled": true,
                "standard_question": "如何重置密码？",
                "similar_questions": ["忘记密码怎么办", "密码找回"],
                "negative_questions": ["如何修改用户名"],
                "answers": ["您可以通过点击登录页面的'忘记密码'链接来重置密码。"],
                "index_mode": "hybrid",
                "chunk_type": "faq",
                "created_at": "2025-08-12T10:00:00+08:00",
                "updated_at": "2025-08-12T10:00:00+08:00"
            }
        ]
    },
    "success": true
}
```

## POST `/knowledge-bases/:id/faq/entries` - 批量导入FAQ条目

**请求参数**:
- `mode`: 导入模式，`append`（追加）或 `replace`（替换）
- `entries`: FAQ条目数组
- `knowledge_id`: 关联的知识ID（可选）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "mode": "append",
    "entries": [
        {
            "standard_question": "如何联系客服？",
            "similar_questions": ["客服电话", "在线客服"],
            "answers": ["您可以通过拨打400-xxx-xxxx联系我们的客服。"],
            "tag_id": "tag-00000001"
        },
        {
            "standard_question": "退款政策是什么？",
            "answers": ["我们提供7天无理由退款服务。"]
        }
    ]
}'
```

**响应**:

```json
{
    "data": {
        "task_id": "task-00000001"
    },
    "success": true
}
```

注：批量导入为异步操作，返回任务ID用于追踪进度。

## POST `/knowledge-bases/:id/faq/entry` - 创建单个FAQ条目

同步创建单个FAQ条目，适用于单条录入场景。会自动检查标准问和相似问是否与已有FAQ重复。

**请求参数**:
- `standard_question`: 标准问（必填）
- `similar_questions`: 相似问数组（可选）
- `negative_questions`: 反例问题数组（可选）
- `answers`: 答案数组（必填）
- `tag_id`: 标签ID（可选）
- `is_enabled`: 是否启用（可选，默认true）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entry' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "standard_question": "如何联系客服？",
    "similar_questions": ["客服电话", "在线客服"],
    "answers": ["您可以通过拨打400-xxx-xxxx联系我们的客服。"],
    "tag_id": "tag-00000001",
    "is_enabled": true
}'
```

**响应**:

```json
{
    "data": {
        "id": "faq-00000001",
        "chunk_id": "chunk-00000001",
        "knowledge_id": "knowledge-00000001",
        "knowledge_base_id": "kb-00000001",
        "tag_id": "tag-00000001",
        "is_enabled": true,
        "standard_question": "如何联系客服？",
        "similar_questions": ["客服电话", "在线客服"],
        "negative_questions": [],
        "answers": ["您可以通过拨打400-xxx-xxxx联系我们的客服。"],
        "index_mode": "hybrid",
        "chunk_type": "faq",
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

**错误响应**（标准问或相似问重复时）:

```json
{
    "success": false,
    "error": {
        "code": "BAD_REQUEST",
        "message": "标准问与已有FAQ重复"
    }
}
```

## PUT `/knowledge-bases/:id/faq/entries/:entry_id` - 更新单个FAQ条目

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/faq-00000001' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "standard_question": "如何重置账户密码？",
    "similar_questions": ["忘记密码怎么办", "密码找回", "重置密码"],
    "answers": ["您可以通过以下步骤重置密码：1. 点击登录页面的\"忘记密码\" 2. 输入注册邮箱 3. 查收重置邮件"],
    "is_enabled": true
}'
```

**响应**:

```json
{
    "success": true
}
```

## GET `/knowledge-bases/:id/faq/entries/:entry_id` - 获取单个FAQ条目

根据 seq_id 获取单个 FAQ 条目的详细信息。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/1' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "faq-00000001",
        "seq_id": 1,
        "chunk_id": "chunk-00000001",
        "knowledge_id": "knowledge-00000001",
        "knowledge_base_id": "kb-00000001",
        "tag_id": "tag-00000001",
        "is_enabled": true,
        "standard_question": "如何重置密码？",
        "similar_questions": ["忘记密码怎么办", "密码找回"],
        "negative_questions": [],
        "answers": ["您可以通过点击登录页面的'忘记密码'链接来重置密码。"],
        "index_mode": "hybrid",
        "chunk_type": "faq",
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

## POST `/knowledge-bases/:id/faq/entries/:entry_id/similar-questions` - 添加相似问题

为指定的 FAQ 条目追加相似问法。

**请求参数**:
- `similar_questions`: 要追加的相似问题数组（必填）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/1/similar-questions' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "similar_questions": ["怎样修改密码", "密码重置方法"]
}'
```

**响应**:

```json
{
    "data": {
        "id": "faq-00000001",
        "seq_id": 1,
        "standard_question": "如何重置密码？",
        "similar_questions": ["忘记密码怎么办", "密码找回", "怎样修改密码", "密码重置方法"],
        "answers": ["您可以通过点击登录页面的'忘记密码'链接来重置密码。"],
        "is_enabled": true,
        "chunk_type": "faq",
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T11:00:00+08:00"
    },
    "success": true
}
```

## PUT `/knowledge-bases/:id/faq/entries/fields` - 批量更新FAQ字段

支持按条目ID或按标签ID批量更新 FAQ 条目的多个字段（启用状态、推荐状态、标签等）。

**请求参数**:
- `by_id`: 按条目 seq_id 更新（可选），键为 seq_id，值为要更新的字段
- `by_tag`: 按标签 seq_id 更新（可选），键为 tag_seq_id，值为要更新的字段
- `exclude_ids`: 排除的条目 seq_id 列表（与 by_tag 配合使用，可选）

每个更新对象支持的字段：
- `is_enabled`: 是否启用（可选）
- `is_recommended`: 是否推荐（可选）
- `tag_id`: 标签ID（可选）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/fields' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "by_id": {
        "1": {"is_enabled": true, "is_recommended": false},
        "2": {"is_enabled": false}
    },
    "by_tag": {
        "100": {"is_enabled": true}
    },
    "exclude_ids": [3, 4]
}'
```

**响应**:

```json
{
    "success": true
}
```

## PUT `/knowledge-bases/:id/faq/entries/tags` - 批量更新FAQ标签

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/tags' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "updates": {
        "faq-00000001": "tag-00000001",
        "faq-00000002": "tag-00000002",
        "faq-00000003": null
    }
}'
```

注：设置为 `null` 可清除标签关联。

**响应**:

```json
{
    "success": true
}
```

## DELETE `/knowledge-bases/:id/faq/entries` - 批量删除FAQ条目

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "ids": ["faq-00000001", "faq-00000002"]
}'
```

**响应**:

```json
{
    "success": true
}
```

## POST `/knowledge-bases/:id/faq/search` - 混合搜索FAQ

**请求参数**:
- `query_text`: 搜索查询文本
- `vector_threshold`: 向量相似度阈值（0-1）
- `match_count`: 返回结果数量（最大200）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/search' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "query_text": "如何重置密码",
    "vector_threshold": 0.5,
    "match_count": 10
}'
```

**响应**:

```json
{
    "data": [
        {
            "id": "faq-00000001",
            "chunk_id": "chunk-00000001",
            "knowledge_id": "knowledge-00000001",
            "knowledge_base_id": "kb-00000001",
            "tag_id": "tag-00000001",
            "is_enabled": true,
            "standard_question": "如何重置密码？",
            "similar_questions": ["忘记密码怎么办", "密码找回"],
            "answers": ["您可以通过点击登录页面的'忘记密码'链接来重置密码。"],
            "chunk_type": "faq",
            "score": 0.95,
            "match_type": "vector",
            "created_at": "2025-08-12T10:00:00+08:00",
            "updated_at": "2025-08-12T10:00:00+08:00"
        }
    ],
    "success": true
}
```

## GET `/knowledge-bases/:id/faq/entries/export` - 导出FAQ条目

将知识库下的所有 FAQ 条目导出为 CSV 文件。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/export' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--output faq_export.csv
```

**响应**:

CSV 文件下载（Content-Type: text/csv）

## GET `/faq/import/progress/:task_id` - 获取FAQ导入进度

查询异步 FAQ 导入任务的执行进度。任务 ID 由批量导入接口返回。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/faq/import/progress/task-00000001' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "task_id": "task-00000001",
        "status": "completed",
        "total": 100,
        "success_count": 98,
        "failed_count": 2,
        "failed_entries": [
            {
                "index": 5,
                "standard_question": "重复的问题",
                "error": "标准问与已有FAQ重复"
            }
        ],
        "success_entries": []
    },
    "success": true
}
```

注：`status` 可能的值为 `pending`、`processing`、`completed`、`failed`。

## PUT `/knowledge-bases/:id/faq/import/last-result/display` - 更新导入结果显示状态

更新上一次 FAQ 导入结果的显示状态，用于控制前端是否展示导入结果提示。

**请求参数**:
- `display_status`: 显示状态（如 `"dismissed"`）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/import/last-result/display' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "display_status": "dismissed"
}'
```

**响应**:

```json
{
    "success": true
}
```
