# FAQ管理 API

[返回目录](./README.md)

FAQ 接口分为两组：

- `/knowledge-bases/:id/faq/*`：知识库范围内的 FAQ 条目 CRUD、批量操作、搜索与导入导出。
- `/faq/import/progress/:task_id`：**不属于知识库分组**，用于查询异步导入/dry-run 任务的进度，仅需任务 ID 即可调用。

| 方法   | 路径                                                              | 描述                              |
| ------ | ----------------------------------------------------------------- | --------------------------------- |
| GET    | `/knowledge-bases/:id/faq/entries`                                | 获取 FAQ 条目列表                 |
| GET    | `/knowledge-bases/:id/faq/entries/export`                         | 导出 FAQ 条目（CSV）              |
| GET    | `/knowledge-bases/:id/faq/entries/:entry_id`                      | 获取单个 FAQ 条目（按 seq_id）     |
| POST   | `/knowledge-bases/:id/faq/entries`                                | 批量 Upsert FAQ 条目（异步）       |
| POST   | `/knowledge-bases/:id/faq/entry`                                  | 同步创建单个 FAQ 条目             |
| PUT    | `/knowledge-bases/:id/faq/entries/:entry_id`                      | 更新单个 FAQ 条目                 |
| POST   | `/knowledge-bases/:id/faq/entries/:entry_id/similar-questions`    | 为 FAQ 条目追加相似问             |
| PUT    | `/knowledge-bases/:id/faq/entries/fields`                         | 批量更新字段（启用/推荐/标签）     |
| PUT    | `/knowledge-bases/:id/faq/entries/tags`                           | 批量更新标签                      |
| DELETE | `/knowledge-bases/:id/faq/entries`                                | 批量删除 FAQ 条目                 |
| POST   | `/knowledge-bases/:id/faq/search`                                 | FAQ 混合搜索                      |
| PUT    | `/knowledge-bases/:id/faq/import/last-result/display`             | 更新上次导入结果卡片显示状态       |
| GET    | `/faq/import/progress/:task_id`                                   | 查询 FAQ 导入任务进度（公共）     |

> **路径参数说明**：`:entry_id` 始终是 FAQ 条目的 `seq_id`（整数），不是字符串形式的 ID。同理，批量接口中的 `by_id` / `by_tag` / `exclude_ids` / `ids` 字段均为 `seq_id` 列表（整数）。

## GET `/knowledge-bases/:id/faq/entries` - 获取 FAQ 条目列表

支持分页、按标签过滤、关键字搜索与排序。

**查询参数**:

| 参数         | 类型   | 必填 | 说明                                                                                          |
| ------------ | ------ | ---- | --------------------------------------------------------------------------------------------- |
| page         | int    | 否   | 页码，默认 1                                                                                  |
| page_size    | int    | 否   | 每页数量，默认 20                                                                             |
| tag_id       | int    | 否   | 按标签 `seq_id` 过滤                                                                          |
| keyword      | string | 否   | 关键字搜索                                                                                    |
| search_field | string | 否   | 搜索字段：`standard_question` / `similar_questions` / `answers`，留空则全字段搜索              |
| sort_order   | string | 否   | 排序方式，`asc` 表示按更新时间正序，默认按更新时间倒序                                          |

**请求**:

```curl
# 全字段搜索
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries?page=1&page_size=10&keyword=密码' \
--header 'X-API-Key: sk-xxxxx'

# 仅搜索标准问
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries?keyword=密码&search_field=standard_question' \
--header 'X-API-Key: sk-xxxxx'
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
                "id": 1,
                "chunk_id": "chunk-00000001",
                "knowledge_id": "knowledge-00000001",
                "knowledge_base_id": "kb-00000001",
                "tag_id": 12,
                "tag_name": "账户",
                "is_enabled": true,
                "is_recommended": false,
                "standard_question": "如何重置密码？",
                "similar_questions": ["忘记密码怎么办", "密码找回"],
                "negative_questions": ["如何修改用户名"],
                "answers": ["您可以通过点击登录页面的'忘记密码'链接来重置密码。"],
                "answer_strategy": "all",
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

## GET `/knowledge-bases/:id/faq/entries/export` - 导出 FAQ 条目

将知识库下的所有 FAQ 条目导出为 CSV（UTF-8 带 BOM，Excel 兼容）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/export' \
--header 'X-API-Key: sk-xxxxx' \
--output faq_export.csv
```

**响应**: `Content-Type: text/csv; charset=utf-8`，附带文件名 `faq_export.csv`。

## GET `/knowledge-bases/:id/faq/entries/:entry_id` - 获取单个 FAQ 条目

根据 `seq_id`（整数）获取单个 FAQ 条目详情。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/1' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "id": 1,
        "chunk_id": "chunk-00000001",
        "knowledge_id": "knowledge-00000001",
        "knowledge_base_id": "kb-00000001",
        "tag_id": 12,
        "tag_name": "账户",
        "is_enabled": true,
        "is_recommended": false,
        "standard_question": "如何重置密码？",
        "similar_questions": ["忘记密码怎么办", "密码找回"],
        "negative_questions": [],
        "answers": ["您可以通过点击登录页面的'忘记密码'链接来重置密码。"],
        "answer_strategy": "all",
        "index_mode": "hybrid",
        "chunk_type": "faq",
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

## POST `/knowledge-bases/:id/faq/entries` - 批量 Upsert FAQ 条目（异步）

**异步**批量导入或更新 FAQ 条目。接口立即返回 `task_id`，调用方需通过 `GET /faq/import/progress/:task_id` 查询进度与结果。

支持 `dry_run=true`：异步执行仅校验（格式 / 批内重复 / 与库内重复 / 内容安全），不实际写入。

**请求体（`types.FAQBatchUpsertPayload`）**:

| 字段         | 类型                       | 必填 | 说明                                                                                |
| ------------ | -------------------------- | ---- | ----------------------------------------------------------------------------------- |
| entries      | `[]FAQEntryPayload`        | 是   | FAQ 条目数组                                                                        |
| mode         | string                     | 是   | `append` 或 `replace`（替换会清空已有条目）                                          |
| knowledge_id | string                     | 否   | 关联的 FAQ Knowledge ID（不传则使用知识库默认 FAQ knowledge）                         |
| task_id      | string                     | 否   | 任务 ID，不传则自动生成 UUID                                                        |
| dry_run      | boolean                    | 否   | 仅验证不导入                                                                        |

`FAQEntryPayload` 字段：

| 字段                | 类型      | 必填 | 说明                                                          |
| ------------------- | --------- | ---- | ------------------------------------------------------------- |
| id                  | int64     | 否   | 指定 `seq_id`（数据迁移场景，需小于自增起始值 100000000）       |
| standard_question   | string    | 是   | 标准问                                                        |
| similar_questions   | string[]  | 否   | 相似问列表                                                    |
| negative_questions  | string[]  | 否   | 反例问题列表                                                  |
| answers             | string[]  | 否   | 答案列表                                                      |
| answer_strategy     | string    | 否   | 答案返回策略：`all` 或 `random`                                |
| tag_id              | int64     | 否   | 标签 `seq_id`                                                 |
| tag_name            | string    | 否   | 标签名（用于按名匹配标签）                                    |
| is_enabled          | boolean   | 否   | 是否启用                                                      |
| is_recommended      | boolean   | 否   | 是否推荐                                                      |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "mode": "append",
    "entries": [
        {
            "standard_question": "如何联系客服？",
            "similar_questions": ["客服电话", "在线客服"],
            "answers": ["您可以通过拨打400-xxx-xxxx联系我们的客服。"],
            "tag_id": 1
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
    "data": { "task_id": "task-00000001" },
    "success": true
}
```

> 用 `GET /faq/import/progress/:task_id` 查询任务最终状态。

## POST `/knowledge-bases/:id/faq/entry` - 同步创建单个 FAQ 条目

**同步**创建单条 FAQ 条目，会即时校验标准问/相似问与库内已有条目的重复。

**请求体**: 同上 `FAQEntryPayload`（`standard_question` 必填）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entry' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "standard_question": "如何联系客服？",
    "similar_questions": ["客服电话", "在线客服"],
    "answers": ["您可以通过拨打400-xxx-xxxx联系我们的客服。"],
    "tag_id": 1,
    "is_enabled": true
}'
```

**响应**:

```json
{
    "data": {
        "id": 1,
        "chunk_id": "chunk-00000001",
        "knowledge_id": "knowledge-00000001",
        "knowledge_base_id": "kb-00000001",
        "tag_id": 1,
        "tag_name": "客服",
        "is_enabled": true,
        "is_recommended": false,
        "standard_question": "如何联系客服？",
        "similar_questions": ["客服电话", "在线客服"],
        "negative_questions": [],
        "answers": ["您可以通过拨打400-xxx-xxxx联系我们的客服。"],
        "answer_strategy": "all",
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

## PUT `/knowledge-bases/:id/faq/entries/:entry_id` - 更新单个 FAQ 条目

按 `seq_id` 更新单条 FAQ 条目，请求体同 `FAQEntryPayload`。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/1' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "standard_question": "如何重置账户密码？",
    "similar_questions": ["忘记密码怎么办", "密码找回", "重置密码"],
    "answers": ["您可以通过以下步骤重置密码：1. 点击登录页面的\"忘记密码\" 2. 输入注册邮箱 3. 查收重置邮件"],
    "is_enabled": true
}'
```

**响应**: 返回更新后的 FAQ 条目，结构同创建接口。

## POST `/knowledge-bases/:id/faq/entries/:entry_id/similar-questions` - 追加相似问

向指定 FAQ 条目（`seq_id`）追加相似问。

**请求体**:

| 字段              | 类型     | 必填 | 说明                  |
| ----------------- | -------- | ---- | --------------------- |
| similar_questions | string[] | 是   | 要追加的相似问数组    |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/1/similar-questions' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "similar_questions": ["怎样修改密码", "密码重置方法"]
}'
```

**响应**: 返回追加后的完整 FAQ 条目。

## PUT `/knowledge-bases/:id/faq/entries/fields` - 批量更新字段

**统一**的批量字段更新接口，支持同时更新 `is_enabled` / `is_recommended` / `tag_id`，并支持两种作用域：

- **按条目 ID** (`by_id`)：键为条目 `seq_id`，值为该条目要更新的字段。
- **按标签 ID** (`by_tag`)：键为标签 `seq_id`，对该标签下的所有条目应用相同的字段更新；可配合 `exclude_ids` 排除部分条目。

`by_id` 和 `by_tag` 至少传一项；二者可同时使用。

**请求体（`types.FAQEntryFieldsBatchUpdate`）**:

| 字段        | 类型                              | 必填 | 说明                                       |
| ----------- | --------------------------------- | ---- | ------------------------------------------ |
| by_id       | `map[int64]FAQEntryFieldsUpdate`  | 否   | 按条目 `seq_id` 更新                       |
| by_tag      | `map[int64]FAQEntryFieldsUpdate`  | 否   | 按标签 `seq_id` 对该标签下所有条目更新     |
| exclude_ids | `int64[]`                         | 否   | 与 `by_tag` 配合使用，排除指定条目 `seq_id` |

`FAQEntryFieldsUpdate` 字段（全部可选，仅传入的字段会被更新）：

| 字段           | 类型    | 说明           |
| -------------- | ------- | -------------- |
| is_enabled     | boolean | 是否启用       |
| is_recommended | boolean | 是否推荐       |
| tag_id         | int64   | 标签 `seq_id`  |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/fields' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "by_id": {
        "1": {"is_enabled": true, "is_recommended": false},
        "2": {"is_enabled": false}
    },
    "by_tag": {
        "100": {"is_recommended": true}
    },
    "exclude_ids": [3, 4]
}'
```

**响应**:

```json
{ "success": true }
```

## PUT `/knowledge-bases/:id/faq/entries/tags` - 批量更新标签

仅更新标签关联。键为条目 `seq_id`，值为目标标签 `seq_id`；值传 `null` 表示清除标签。

**请求体**:

| 字段    | 类型                  | 必填 | 说明                                         |
| ------- | --------------------- | ---- | -------------------------------------------- |
| updates | `map[int64]int64?`    | 是   | 键：条目 `seq_id`；值：标签 `seq_id` 或 `null` |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries/tags' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "updates": {
        "1": 10,
        "2": 11,
        "3": null
    }
}'
```

**响应**:

```json
{ "success": true }
```

## DELETE `/knowledge-bases/:id/faq/entries` - 批量删除

**请求体**:

| 字段 | 类型      | 必填 | 说明                              |
| ---- | --------- | ---- | --------------------------------- |
| ids  | `int64[]` | 是   | 要删除的 FAQ 条目 `seq_id` 列表    |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/entries' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "ids": [1, 2, 3]
}'
```

**响应**:

```json
{ "success": true }
```

## POST `/knowledge-bases/:id/faq/search` - FAQ 混合搜索

向量 + 关键字混合检索，支持两级优先级标签召回。

**请求体（`types.FAQSearchRequest`）**:

| 字段                    | 类型      | 必填 | 说明                                                                                |
| ----------------------- | --------- | ---- | ----------------------------------------------------------------------------------- |
| query_text              | string    | 是   | 搜索文本                                                                            |
| vector_threshold        | float     | 否   | 向量相似度阈值（0–1）                                                               |
| match_count             | int       | 否   | 返回数量，默认 10，最大 200                                                         |
| first_priority_tag_ids  | `int64[]` | 否   | 第一优先级标签 `seq_id` 列表（最高优先召回范围）                                     |
| second_priority_tag_ids | `int64[]` | 否   | 第二优先级标签 `seq_id` 列表                                                        |
| only_recommended        | boolean   | 否   | 是否仅返回 `is_recommended=true` 的条目                                              |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/search' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "query_text": "如何重置密码",
    "vector_threshold": 0.5,
    "match_count": 10,
    "first_priority_tag_ids": [12],
    "only_recommended": false
}'
```

**响应**:

```json
{
    "data": [
        {
            "id": 1,
            "chunk_id": "chunk-00000001",
            "knowledge_id": "knowledge-00000001",
            "knowledge_base_id": "kb-00000001",
            "tag_id": 12,
            "tag_name": "账户",
            "is_enabled": true,
            "is_recommended": false,
            "standard_question": "如何重置密码？",
            "similar_questions": ["忘记密码怎么办", "密码找回"],
            "answers": ["您可以通过点击登录页面的'忘记密码'链接来重置密码。"],
            "answer_strategy": "all",
            "chunk_type": "faq",
            "score": 0.95,
            "match_type": "vector",
            "matched_question": "忘记密码怎么办",
            "created_at": "2025-08-12T10:00:00+08:00",
            "updated_at": "2025-08-12T10:00:00+08:00"
        }
    ],
    "success": true
}
```

## PUT `/knowledge-bases/:id/faq/import/last-result/display` - 更新上次导入结果显示状态

控制上次导入完成后，前端结果卡片的显示/隐藏。

**请求体**:

| 字段           | 类型   | 必填 | 说明                  |
| -------------- | ------ | ---- | --------------------- |
| display_status | string | 是   | `open` 或 `close`     |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/faq/import/last-result/display' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "display_status": "close"
}'
```

**响应**:

```json
{ "success": true }
```

## GET `/faq/import/progress/:task_id` - 查询 FAQ 导入进度

> **注意**：此接口**不在** `/knowledge-bases/:id/faq` 分组下，路径直接以 `/faq/import/progress/:task_id` 开头。任务 ID 由 `POST /knowledge-bases/:id/faq/entries` 返回。

**路径参数**:

| 参数    | 类型   | 说明           |
| ------- | ------ | -------------- |
| task_id | string | 导入任务的 ID  |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/faq/import/progress/task-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（节选关键字段）:

```json
{
    "data": {
        "task_id": "task-00000001",
        "kb_id": "kb-00000001",
        "knowledge_id": "knowledge-00000001",
        "status": "completed",
        "progress": 100,
        "total": 100,
        "processed": 100,
        "success_count": 95,
        "failed_count": 3,
        "partial_failed_count": 2,
        "skipped_count": 0,
        "failed_entries": [
            {
                "index": 5,
                "reason": "标准问与已有FAQ重复",
                "standard_question": "重复的问题"
            }
        ],
        "success_entries": [
            { "index": 0, "seq_id": 101, "standard_question": "如何联系客服？" }
        ],
        "message": "",
        "error": "",
        "created_at": 1736582400,
        "updated_at": 1736582460,
        "dry_run": false,
        "import_mode": "append",
        "imported_at": "2025-08-12T10:01:00+08:00",
        "display_status": "open",
        "processing_time": 60000
    },
    "success": true
}
```

`status` 可能取值：`pending` / `processing` / `completed` / `failed`。

当失败条目过多时，`failed_entries` 可能不直接返回，而通过 `failed_entries_url` 提供 CSV 下载地址。

`dry_run=true` 模式下的任务同样通过此接口查询，`success_entries` 中的 `seq_id` 不会真正写入。
