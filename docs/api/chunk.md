# 分块管理 API

[返回目录](./README.md)

| 方法   | 路径                              | 描述                       |
| ------ | --------------------------------- | -------------------------- |
| GET    | `/chunks/:knowledge_id`           | 获取知识的分块列表         |
| PUT    | `/chunks/:knowledge_id/:id`       | 更新分块                   |
| DELETE | `/chunks/:knowledge_id/:id`       | 删除单个分块               |
| DELETE | `/chunks/:knowledge_id`           | 删除知识下的所有分块       |
| GET    | `/chunks/by-id/:id`               | 根据分块 ID 直接获取分块    |
| DELETE | `/chunks/by-id/:id/questions`     | 删除分块下的某个生成问题   |

## GET `/chunks/:knowledge_id` - 获取知识的分块列表

**路径参数**:

| 字段          | 类型   | 说明        |
| ------------- | ------ | ----------- |
| knowledge_id  | string | 知识 ID     |

**查询参数**:

| 字段       | 类型 | 默认 | 说明       |
| ---------- | ---- | ---- | ---------- |
| page       | int  | 1    | 页码       |
| page_size  | int  | 20   | 每页条数   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5?page=1&page_size=1' \
--header 'X-API-Key: sk-xxxxx' \
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

更新指定分块的内容和属性。所有字段均可选，未传则保留原值。

**路径参数**:

| 字段          | 类型   | 说明        |
| ------------- | ------ | ----------- |
| knowledge_id  | string | 知识 ID     |
| id            | string | 分块 ID     |

**参数说明（请求体）**:

| 字段         | 类型    | 必填 | 说明                  |
| ------------ | ------- | ---- | --------------------- |
| content      | string  | 否   | 分块内容               |
| chunk_index  | int     | 否   | 分块在知识中的序号     |
| is_enabled   | boolean | 否   | 是否启用               |
| start_at     | int     | 否   | 起始位置（字符偏移）   |
| end_at       | int     | 否   | 结束位置（字符偏移）   |
| image_info   | string  | 否   | 图像分块的元信息（JSON 字符串） |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-xxxxx' \
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
        "content": "更新后的分块内容",
        "is_enabled": true,
        "...": "其他字段同 GET 响应"
    },
    "success": true
}
```

## DELETE `/chunks/:knowledge_id/:id` - 删除单个分块

**路径参数**: 同 PUT。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "message": "Chunk deleted",
    "success": true
}
```

## DELETE `/chunks/:knowledge_id` - 删除知识下的所有分块

**路径参数**:

| 字段          | 类型   | 说明        |
| ------------- | ------ | ----------- |
| knowledge_id  | string | 知识 ID     |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "message": "All chunks under knowledge deleted",
    "success": true
}
```

## GET `/chunks/by-id/:id` - 根据 ID 直接获取分块

无需提供 `knowledge_id` 即可获取分块。常用于跨知识库的引用展示。

**路径参数**:

| 字段 | 类型   | 说明    |
| ---- | ------ | ------- |
| id   | string | 分块 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/chunks/by-id/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**: 同 `GET /chunks/:knowledge_id` 列表中的单条 data。

## DELETE `/chunks/by-id/:id/questions` - 删除分块下的某个生成问题

删除指定分块关联的某条生成问题。

**路径参数**:

| 字段 | 类型   | 说明    |
| ---- | ------ | ------- |
| id   | string | 分块 ID |

**参数说明（请求体）**:

| 字段        | 类型   | 必填 | 说明        |
| ----------- | ------ | ---- | ----------- |
| question_id | string | 是   | 问题 ID     |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/chunks/by-id/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7/questions' \
--header 'X-API-Key: sk-xxxxx' \
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
