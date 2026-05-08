# Vector Store API

[返回目录](./README.md)

向量存储（VectorStore）API 用于管理向量数据库连接配置。支持 Elasticsearch、PostgreSQL、Qdrant、Milvus、Weaviate、Tencent VectorDB、SQLite 七种引擎类型。

| 方法   | 路径                         | 描述                           |
| ------ | ---------------------------- | ------------------------------ |
| GET    | `/vector-stores/types`       | 获取支持的引擎类型及配置字段   |
| POST   | `/vector-stores/test`        | 使用原始凭据测试连接（不保存） |
| POST   | `/vector-stores`             | 创建向量存储                   |
| GET    | `/vector-stores`             | 获取向量存储列表               |
| GET    | `/vector-stores/:id`         | 获取向量存储详情               |
| PUT    | `/vector-stores/:id`         | 更新向量存储（仅名称可修改）   |
| DELETE | `/vector-stores/:id`         | 删除向量存储（软删除）         |
| POST   | `/vector-stores/:id/test`    | 测试已保存的向量存储连接       |

---

## GET `/vector-stores/types` - 获取支持的引擎类型

返回所有支持的引擎类型，包含连接配置和索引配置的字段定义，可用于前端表单生成。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/vector-stores/types' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "type": "elasticsearch",
            "display_name": "Elasticsearch (Keywords + Vector)",
            "connection_fields": [
                {"name": "addr", "type": "string", "required": true, "description": "Elasticsearch URL (e.g., http://localhost:9200)"},
                {"name": "username", "type": "string", "required": false},
                {"name": "password", "type": "string", "required": false, "sensitive": true}
            ],
            "index_fields": [
                {"name": "index_name", "type": "string", "required": false, "default": "xwrag_default"},
                {"name": "number_of_shards", "type": "number", "required": false},
                {"name": "number_of_replicas", "type": "number", "required": false}
            ]
        },
        {
            "type": "postgres",
            "display_name": "PostgreSQL (Keywords + Vector)",
            "connection_fields": [
                {"name": "use_default_connection", "type": "boolean", "required": false, "default": true, "description": "Use the application's default database connection"},
                {"name": "addr", "type": "string", "required": false, "description": "PostgreSQL connection string (required if use_default_connection is false)"},
                {"name": "username", "type": "string", "required": false},
                {"name": "password", "type": "string", "required": false, "sensitive": true}
            ]
        }
    ]
}
```

---

## POST `/vector-stores/test` - 测试原始凭据连接

使用提供的凭据执行连接测试，不保存任何数据。成功时返回自动检测到的服务器版本。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/vector-stores/test' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "engine_type": "elasticsearch",
    "connection_config": {
        "addr": "http://es:9200",
        "username": "elastic",
        "password": "changeme"
    }
}'
```

**响应（成功）**:

```json
{
    "success": true,
    "version": "7.10.1"
}
```

> `version` 字段包含自动检测到的服务器版本。如果无法检测（如 Milvus、SQLite），则为空字符串。

**响应（失败）**:

```json
{
    "success": false,
    "error": "failed to connect to elasticsearch: connection refused or authentication failed"
}
```

---

## POST `/vector-stores` - 创建向量存储

创建一个新的向量存储配置。同一 endpoint + index 组合不允许重复注册（包括环境变量配置的存储）。

Tencent VectorDB 使用 `engine_type: "tencent_vectordb"`。`connection_config` 中 `addr`、`username`、`api_key` 为必填，`database` 可选；`index_config.collection_name` 表示集合名前缀，实际集合会按向量维度追加后缀，例如 `weknora_embeddings_768`。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/vector-stores' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "elasticsearch-hot",
    "engine_type": "elasticsearch",
    "connection_config": {
        "addr": "http://es-hot:9200",
        "username": "elastic",
        "password": "changeme"
    },
    "index_config": {
        "index_name": "my_index"
    }
}'
```

**Tencent VectorDB 请求示例**:

```curl
curl --location 'http://localhost:8080/api/v1/vector-stores' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "tencent-vectordb",
    "engine_type": "tencent_vectordb",
    "connection_config": {
        "addr": "http://your-instance.tencentvectordb.com",
        "username": "root",
        "api_key": "your_api_key",
        "database": "weknora"
    },
    "index_config": {
        "collection_name": "weknora_embeddings"
    }
}'
```

**响应** (201):

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "elasticsearch-hot",
        "engine_type": "elasticsearch",
        "connection_config": {
            "addr": "http://es-hot:9200",
            "username": "elastic",
            "password": "***"
        },
        "index_config": {
            "index_name": "my_index"
        },
        "source": "user",
        "readonly": false,
        "created_at": "2026-04-07T10:00:00Z",
        "updated_at": "2026-04-07T10:00:00Z"
    }
}
```

> **注意**: 响应中的敏感字段（`password`、`api_key`）会被掩码为 `"***"`。
> `connection_config.version` 字段在连接测试成功后自动填充（如 `"7.10.1"`），创建时为空。

---

## GET `/vector-stores` - 获取向量存储列表

返回当前租户的所有向量存储，包含环境变量配置的存储（`source: "env"`）和用户创建的存储（`source: "user"`）。环境变量存储排列在前。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/vector-stores' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "id": "__env_postgres__",
            "name": "postgres (env)",
            "engine_type": "postgres",
            "connection_config": {
                "use_default_connection": true
            },
            "source": "env",
            "readonly": true
        },
        {
            "id": "550e8400-e29b-41d4-a716-446655440000",
            "name": "elasticsearch-hot",
            "engine_type": "elasticsearch",
            "connection_config": {
                "addr": "http://es-hot:9200",
                "username": "elastic",
                "password": "***"
            },
            "source": "user",
            "readonly": false
        }
    ]
}
```

---

## GET `/vector-stores/:id` - 获取向量存储详情

根据 ID 获取单个向量存储详情。支持 `__env_*` 格式的环境变量存储 ID。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/vector-stores/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "elasticsearch-hot",
        "engine_type": "elasticsearch",
        "connection_config": {
            "addr": "http://es-hot:9200",
            "username": "elastic",
            "password": "***",
            "version": "7.10.1"
        },
        "index_config": {
            "index_name": "my_index"
        },
        "source": "user",
        "readonly": false,
        "created_at": "2026-04-07T10:00:00Z",
        "updated_at": "2026-04-07T10:00:00Z"
    }
}
```

---

## PUT `/vector-stores/:id` - 更新向量存储

更新向量存储的名称。`engine_type`、`connection_config`、`index_config` 创建后不可变更。环境变量存储不可修改（返回 400）。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/vector-stores/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "elasticsearch-hot-renamed"
}'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "elasticsearch-hot-renamed",
        "engine_type": "elasticsearch",
        "connection_config": {
            "addr": "http://es-hot:9200",
            "username": "elastic",
            "password": "***"
        },
        "index_config": {
            "index_name": "my_index"
        },
        "source": "user",
        "readonly": false,
        "created_at": "2026-04-07T10:00:00Z",
        "updated_at": "2026-04-07T10:05:00Z"
    }
}
```

---

## DELETE `/vector-stores/:id` - 删除向量存储

软删除向量存储。环境变量存储不可删除（返回 400）。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/vector-stores/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true
}
```

---

## POST `/vector-stores/:id/test` - 测试已保存的向量存储连接

对已保存的向量存储或环境变量存储执行连接测试。成功时返回检测到的服务器版本，并自动更新存储记录中的 `connection_config.version` 字段。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/vector-stores/550e8400-e29b-41d4-a716-446655440000/test' \
--header 'X-API-Key: sk-xxxxx'
```

**响应（成功）**:

```json
{
    "success": true,
    "version": "7.10.1"
}
```

> 对于已保存的存储（非环境变量存储），检测到的版本会自动保存到 `connection_config.version` 中。

**响应（失败）**:

```json
{
    "success": false,
    "error": "failed to connect to elasticsearch: connection refused or authentication failed"
}
```

---

## 环境变量存储

通过 `RETRIEVE_DRIVER` 环境变量配置的向量存储以虚拟条目形式出现在列表中。这些条目的特征：

- **ID 格式**: `__env_{driver}__`（如 `__env_postgres__`、`__env_elasticsearch_v8__`）
- **source**: `"env"`
- **readonly**: `true`
- **不可修改/删除**: PUT 和 DELETE 返回 400
- **可测试连接**: POST `/:id/test` 正常工作

## 错误码

| HTTP 状态码 | 含义 |
|-------------|------|
| 400 | 请求参数错误、验证失败、尝试修改环境变量存储 |
| 401 | 未认证 |
| 404 | 向量存储不存在 |
| 409 | 同一 endpoint + index 组合已存在 |
| 500 | 内部服务器错误 |
