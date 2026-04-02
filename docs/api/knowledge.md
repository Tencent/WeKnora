# 知识管理 API

[返回目录](./README.md)

| 方法   | 路径                                  | 描述                     |
| ------ | ------------------------------------- | ------------------------ |
| POST   | `/knowledge-bases/:id/knowledge/file` | 从文件创建知识           |
| POST   | `/knowledge-bases/:id/knowledge/url`  | 从 URL 创建知识          |
| POST   | `/knowledge-bases/:id/knowledge/manual` | 创建手工 Markdown 知识 |
| GET    | `/knowledge-bases/:id/knowledge`      | 获取知识库下的知识列表   |
| GET    | `/knowledge/:id`                      | 获取知识详情             |
| DELETE | `/knowledge/:id`                      | 删除知识                 |
| GET    | `/knowledge/:id/download`             | 下载知识文件             |
| PUT    | `/knowledge/:id`                      | 更新知识                 |
| PUT    | `/knowledge/manual/:id`               | 更新手工 Markdown 知识   |
| PUT    | `/knowledge/image/:id/:chunk_id`      | 更新图像分块信息         |
| PUT    | `/knowledge/tags`                     | 批量更新知识标签         |
| GET    | `/knowledge/batch`                    | 批量获取知识             |
| POST   | `/knowledge/:id/reparse`              | 重新解析知识             |
| GET    | `/knowledge/search`                   | 搜索/过滤知识条目        |
| POST   | `/knowledge/move`                     | 迁移知识到另一个知识库   |
| GET    | `/knowledge/move/progress/:task_id`   | 获取知识迁移进度         |
| GET    | `/knowledge/:id/preview`              | 预览知识文件             |

## POST `/knowledge-bases/:id/knowledge/file` - 从文件创建知识

**表单参数**：
- `file`: 上传的文件（必填）
- `metadata`: JSON 格式的元数据（可选）
- `enable_multimodel`: 是否启用多模态处理（可选，true/false）
- `fileName`: 自定义文件名，用于文件夹上传时保留路径（可选）

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
        "channel": "web",
        "tag_id": "",
        "summary_status": "none",
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

## POST `/knowledge-bases/:id/knowledge/url` - 从 URL 创建知识

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
        "channel": "web",
        "tag_id": "",
        "summary_status": "none",
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

## GET `/knowledge-bases/:id/knowledge` - 获取知识库下的知识列表

**查询参数**：
- `page`: 页码（默认 1）
- `page_size`: 每页条数（默认 20）
- `tag_id`: 按标签ID筛选（可选）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge?page_size=1&page=1&tag_id=tag-00000001' \
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
            "channel": "web",
            "tag_id": "",
            "summary_status": "none",
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

## GET `/knowledge/:id` - 获取知识详情

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
        "channel": "web",
        "tag_id": "",
        "summary_status": "none",
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

## GET `/knowledge/batch` - 批量获取知识

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
            "channel": "web",
            "tag_id": "",
            "summary_status": "none",
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
            "channel": "web",
            "tag_id": "",
            "summary_status": "none",
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

## DELETE `/knowledge/:id` - 删除知识

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

## GET `/knowledge/:id/download` - 下载知识文件

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

## PUT `/knowledge/:id` - 更新知识

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "title": "更新的标题",
    "description": "更新的描述",
    "tag_id": "tag-00000001"
}'
```

**响应**:

```json
{
    "message": "Updated successfully",
    "success": true
}
```

## POST `/knowledge-bases/:id/knowledge/manual` - 创建手工 Markdown 知识

创建手工 Markdown 知识条目，适用于直接编写内容而非上传文件的场景。

**请求参数**:
- `title`: 知识标题（必填）
- `content`: Markdown 内容（必填）
- `tag_id`: 标签ID（可选）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge/manual' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "title": "产品使用指南",
    "content": "# 产品使用指南\n\n## 快速入门\n\n这是一份产品使用指南...",
    "tag_id": "tag-00000001"
}'
```

**响应**:

```json
{
    "data": {
        "id": "5a3b2c1d-0e9f-4a8b-7c6d-5e4f3a2b1c0d",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "manual",
        "title": "产品使用指南",
        "description": "",
        "source": "",
        "channel": "web",
        "tag_id": "",
        "summary_status": "none",
        "parse_status": "processing",
        "enable_status": "disabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "",
        "file_type": "md",
        "file_size": 0,
        "file_hash": "",
        "file_path": "",
        "storage_size": 0,
        "metadata": null,
        "created_at": "2025-08-12T12:00:00.000000+08:00",
        "updated_at": "2025-08-12T12:00:00.000000+08:00",
        "processed_at": null,
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

## PUT `/knowledge/manual/:id` - 更新手工 Markdown 知识

**请求参数**:
- `title`: 新标题（可选）
- `content`: 新 Markdown 内容（可选）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/manual/5a3b2c1d-0e9f-4a8b-7c6d-5e4f3a2b1c0d' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "title": "产品使用指南 V2",
    "content": "# 产品使用指南 V2\n\n## 更新内容\n\n..."
}'
```

**响应**:

```json
{
    "data": {
        "id": "5a3b2c1d-0e9f-4a8b-7c6d-5e4f3a2b1c0d",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "manual",
        "title": "产品使用指南 V2",
        "parse_status": "processing",
        "created_at": "2025-08-12T12:00:00.000000+08:00",
        "updated_at": "2025-08-12T12:30:00.000000+08:00"
    },
    "success": true
}
```

## PUT `/knowledge/image/:id/:chunk_id` - 更新图像分块信息

更新知识条目中指定分块的图像描述信息。

**请求参数**:
- `image_info`: 图像信息（JSON 格式字符串）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/image/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "image_info": "{\"description\": \"产品架构图\", \"alt_text\": \"WeKnora 系统架构\"}"
}'
```

**响应**:

```json
{
    "message": "Updated successfully",
    "success": true
}
```

## PUT `/knowledge/tags` - 批量更新知识标签

批量更新多个知识条目的标签关联。

**请求参数**:
- `updates`: 知识ID到标签ID的映射（设为 `null` 可清除标签）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/tags' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "updates": {
        "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5": "tag-00000001",
        "9c8af585-ae15-44ce-8f73-45ad18394651": null
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

## POST `/knowledge/:id/reparse` - 重新解析知识

触发知识的异步重新解析。此操作会删除现有的文档内容，然后使用最新的解析配置重新解析知识。
适用于解析配置更新后需要刷新内容，或者原始解析失败需要重试的场景。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/reparse' \
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
        "parse_status": "pending",
        "enable_status": "enabled",
        "created_at": "2025-08-12T11:52:36.168632+08:00",
        "updated_at": "2025-08-12T13:00:00.000000+08:00"
    },
    "success": true
}
```

注：重新解析为异步操作，返回后 `parse_status` 将变为 `pending`，随后进入 `processing` 状态。

## GET `/knowledge/search` - 搜索/过滤知识条目

按关键词搜索和过滤知识条目，支持按文件类型和 Agent ID 筛选。

**查询参数**:
- `keyword`: 搜索关键词（可选）
- `offset`: 偏移量（默认 0）
- `limit`: 返回数量（默认 20）
- `file_types`: 文件类型过滤，多个类型用逗号分隔（可选）
- `agent_id`: 按 Agent ID 筛选（可选）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/search?keyword=%E5%BD%97%E6%98%9F&offset=0&limit=10&file_types=txt,pdf' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "data": [
            {
                "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
                "tenant_id": 1,
                "knowledge_base_id": "kb-00000001",
                "type": "file",
                "title": "彗星.txt",
                "description": "彗星是由冰和尘埃构成的太阳系小天体...",
                "file_name": "彗星.txt",
                "file_type": "txt",
                "file_size": 7710,
                "parse_status": "completed",
                "enable_status": "enabled",
                "created_at": "2025-08-12T11:52:36.168632+08:00",
                "updated_at": "2025-08-12T11:52:53.376871+08:00"
            }
        ],
        "has_more": false
    },
    "success": true
}
```

## POST `/knowledge/move` - 迁移知识到另一个知识库

将知识条目从一个知识库迁移到另一个知识库。此操作为异步任务，返回任务ID用于查询迁移进度。

**请求参数**:
- `knowledge_ids`: 待迁移的知识ID列表（必填）
- `source_kb_id`: 源知识库ID（必填）
- `target_kb_id`: 目标知识库ID（必填）
- `mode`: 迁移模式，`reuse_vectors` 复用向量数据，`reparse` 重新解析（必填）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/move' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "knowledge_ids": ["4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5"],
    "source_kb_id": "kb-00000001",
    "target_kb_id": "kb-00000002",
    "mode": "reuse_vectors"
}'
```

**响应**:

```json
{
    "data": {
        "task_id": "task-move-00000001",
        "source_kb_id": "kb-00000001",
        "target_kb_id": "kb-00000002",
        "knowledge_count": 1,
        "message": "知识迁移任务已创建"
    },
    "success": true
}
```

## GET `/knowledge/move/progress/:task_id` - 获取知识迁移进度

查询知识迁移任务的执行进度。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/move/progress/task-move-00000001' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "task_id": "task-move-00000001",
        "status": "completed",
        "progress": 100,
        "total": 1,
        "processed": 1,
        "message": "迁移完成",
        "error": ""
    },
    "success": true
}
```

注：`status` 可能的值为 `pending`、`processing`、`completed`、`failed`。

## GET `/knowledge/:id/preview` - 预览知识文件

在浏览器中内联预览知识文件内容。响应会设置相应的 `Content-Type` 和 `Content-Disposition` 头，用于浏览器端直接展示文件。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/preview' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ'
```

**响应**:

```
Content-Type: text/plain; charset=utf-8
Content-Disposition: inline; filename="彗星.txt"

(文件内容)
```
