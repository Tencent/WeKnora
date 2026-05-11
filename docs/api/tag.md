# 标签管理 API

[返回目录](./README.md)

| 方法   | 路径                                  | 描述                     |
| ------ | ------------------------------------- | ------------------------ |
| GET    | `/knowledge-bases/:id/tags`           | 获取知识库标签列表       |
| POST   | `/knowledge-bases/:id/tags`           | 创建标签                 |
| PUT    | `/knowledge-bases/:id/tags/:tag_id`   | 更新标签                 |
| DELETE | `/knowledge-bases/:id/tags/:tag_id`   | 删除标签                 |

## GET `/knowledge-bases/:id/tags` - 获取知识库标签列表

**查询参数**:
- `page`: 页码（默认 1）
- `page_size`: 每页条数（默认 20）
- `keyword`: 标签名称关键字搜索（可选）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/tags?page=1&page_size=10' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "total": 2,
        "page": 1,
        "page_size": 10,
        "data": [
            {
                "id": "tag-00000001",
                "tenant_id": 1,
                "knowledge_base_id": "kb-00000001",
                "name": "技术文档",
                "color": "#1890ff",
                "sort_order": 1,
                "created_at": "2025-08-12T10:00:00+08:00",
                "updated_at": "2025-08-12T10:00:00+08:00",
                "knowledge_count": 5,
                "chunk_count": 120
            },
            {
                "id": "tag-00000002",
                "tenant_id": 1,
                "knowledge_base_id": "kb-00000001",
                "name": "常见问题",
                "color": "#52c41a",
                "sort_order": 2,
                "created_at": "2025-08-12T10:00:00+08:00",
                "updated_at": "2025-08-12T10:00:00+08:00",
                "knowledge_count": 3,
                "chunk_count": 45
            }
        ]
    },
    "success": true
}
```

## POST `/knowledge-bases/:id/tags` - 创建标签

**路径参数**:

| 字段 | 类型   | 说明        |
| ---- | ------ | ----------- |
| id   | string | 知识库 ID    |

**参数说明（请求体）**:

| 字段       | 类型   | 必填 | 说明                     |
| ---------- | ------ | ---- | ------------------------ |
| name       | string | 是   | 标签名（同库内唯一）      |
| color      | string | 否   | 标签颜色（CSS 颜色字符串） |
| sort_order | int    | 否   | 排序值（数值越小越靠前）   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/tags' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "产品手册",
    "color": "#faad14",
    "sort_order": 3
}'
```

**响应**:

```json
{
    "data": {
        "id": "tag-00000003",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "name": "产品手册",
        "color": "#faad14",
        "sort_order": 3,
        "created_at": "2025-08-12T11:00:00+08:00",
        "updated_at": "2025-08-12T11:00:00+08:00"
    },
    "success": true
}
```

## PUT `/knowledge-bases/:id/tags/:tag_id` - 更新标签

**路径参数**:

| 字段   | 类型   | 说明        |
| ------ | ------ | ----------- |
| id     | string | 知识库 ID    |
| tag_id | string | 标签 ID      |

**参数说明（请求体）**: 同创建接口，所有字段均可选；未传则保留原值。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/tags/tag-00000003' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "产品手册更新",
    "color": "#ff4d4f"
}'
```

**响应**:

```json
{
    "data": {
        "id": "tag-00000003",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "name": "产品手册更新",
        "color": "#ff4d4f",
        "sort_order": 3,
        "created_at": "2025-08-12T11:00:00+08:00",
        "updated_at": "2025-08-12T11:30:00+08:00"
    },
    "success": true
}
```

## DELETE `/knowledge-bases/:id/tags/:tag_id` - 删除标签

**路径参数**:

| 字段   | 类型   | 说明     |
| ------ | ------ | -------- |
| id     | string | 知识库 ID |
| tag_id | string | 标签 ID   |

**查询参数**:

| 字段  | 类型    | 默认  | 说明                                          |
| ----- | ------- | ----- | --------------------------------------------- |
| force | boolean | false | 设置为 `true` 时强制删除（即使标签被引用）     |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/tags/tag-00000003?force=true' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true
}
```
