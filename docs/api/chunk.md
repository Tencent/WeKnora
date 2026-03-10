# 分块管理 API

[返回目录](./README.md)

| 方法   | 路径                        | 描述                     |
| ------ | --------------------------- | ------------------------ |
| GET    | `/chunks/:knowledge_id`     | 获取知识的分块列表       |
| PUT    | `/chunks/:knowledge_id/:id` | 更新分块                 |
| DELETE | `/chunks/:knowledge_id/:id` | 删除分块                 |
| DELETE | `/chunks/:knowledge_id`     | 删除知识下的所有分块     |
| GET    | `/chunks/get-by-id/:id`     | 根据ID直接获取分块       |
| DELETE | `/chunks/:id/delete-question` | 删除分块的生成问题     |

## GET `/chunks/:knowledge_id?page=&page_size=` - 获取知识的分块列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5?page=1&page_size=1' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7",
            "tenant_id": 1,
            "knowledge_id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
            "knowledge_base_id": "kb-00000001",
            "tag_id": "",
            "content": "彗星xxxx",
            "chunk_index": 0,
            "is_enabled": true,
            "status": 2,
            "start_at": 0,
            "end_at": 964,
            "pre_chunk_id": "",
            "next_chunk_id": "",
            "chunk_type": "text",
            "parent_chunk_id": "",
            "relation_chunks": null,
            "indirect_relation_chunks": null,
            "metadata": null,
            "content_hash": "",
            "image_info": "",
            "created_at": "2025-08-12T11:52:36.168632+08:00",
            "updated_at": "2025-08-12T11:52:53.376871+08:00",
            "deleted_at": null
        }
    ],
    "page": 1,
    "page_size": 1,
    "success": true,
    "total": 5
}
```

## PUT `/chunks/:knowledge_id/:id` - 更新分块

更新指定分块的内容和属性。

**请求参数**:
- `content`: 分块内容（可选）
- `chunk_index`: 分块索引（可选）
- `is_enabled`: 是否启用（可选）
- `start_at`: 起始位置（可选）
- `end_at`: 结束位置（可选）
- `image_info`: 图片信息（可选）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "content": "更新后的分块内容",
    "is_enabled": true
}'
```

**响应**:

```json
{
    "data": {
        "id": "df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7",
        "tenant_id": 1,
        "knowledge_id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "knowledge_base_id": "kb-00000001",
        "tag_id": "",
        "content": "更新后的分块内容",
        "chunk_index": 0,
        "is_enabled": true,
        "status": 2,
        "start_at": 0,
        "end_at": 964,
        "pre_chunk_id": "",
        "next_chunk_id": "",
        "chunk_type": "text",
        "parent_chunk_id": "",
        "relation_chunks": null,
        "indirect_relation_chunks": null,
        "metadata": null,
        "content_hash": "",
        "image_info": "",
        "created_at": "2025-08-12T11:52:36.168632+08:00",
        "updated_at": "2025-08-12T12:00:00.000000+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## DELETE `/chunks/:knowledge_id/:id` - 删除分块

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "Chunk deleted",
    "success": true
}
```

## DELETE `/chunks/:knowledge_id` - 删除知识下的所有分块

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "message": "All chunks under knowledge deleted",
    "success": true
}
```

## GET `/chunks/get-by-id/:id` - 根据ID直接获取分块

根据分块ID直接获取分块信息，无需提供知识ID。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/chunks/get-by-id/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7",
        "tenant_id": 1,
        "knowledge_id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "knowledge_base_id": "kb-00000001",
        "tag_id": "",
        "content": "彗星xxxx",
        "chunk_index": 0,
        "is_enabled": true,
        "status": 2,
        "start_at": 0,
        "end_at": 964,
        "pre_chunk_id": "",
        "next_chunk_id": "",
        "chunk_type": "text",
        "parent_chunk_id": "",
        "relation_chunks": null,
        "indirect_relation_chunks": null,
        "metadata": null,
        "content_hash": "",
        "image_info": "",
        "created_at": "2025-08-12T11:52:36.168632+08:00",
        "updated_at": "2025-08-12T11:52:53.376871+08:00",
        "deleted_at": null
    },
    "success": true
}
```

## DELETE `/chunks/:id/delete-question` - 删除分块的生成问题

删除指定分块关联的生成问题。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7/delete-question' \
--header 'X-API-Key: sk-vQHV2NZI_LK5W7wHQvH3yGYExX8YnhaHwZipUYbiZKCYJbBQ' \
--header 'Content-Type: application/json' \
--data '{
    "question_id": "q-00000001"
}'
```

**响应**:

```json
{
    "message": "Question deleted successfully",
    "success": true
}
```
