# Vector Store API

[返回目录](./README.md)

向量存储（VectorStore）API 用于管理租户的向量数据库连接配置，支持 Elasticsearch、PostgreSQL、Qdrant、Milvus、Weaviate、Tencent VectorDB、SQLite 等引擎。接口同时管理用户在 DB 中创建的配置（`source: "user"`）以及通过 `RETRIEVE_DRIVER` 环境变量配置的虚拟存储（`source: "env"`，只读）。

| 方法   | 路径                         | 描述                             |
| ------ | ---------------------------- | -------------------------------- |
| GET    | `/vector-stores/types`       | 获取支持的引擎类型及字段元数据     |
| POST   | `/vector-stores/test`        | 使用原始凭据测试连接（不落库）      |
| POST   | `/vector-stores`             | 创建向量存储                     |
| GET    | `/vector-stores`             | 获取向量存储列表                 |
| GET    | `/vector-stores/:id`         | 获取向量存储详情                 |
| PUT    | `/vector-stores/:id`         | 更新向量存储（仅名称可改）        |
| DELETE | `/vector-stores/:id`         | 删除向量存储（软删除）            |
| POST   | `/vector-stores/:id/test`    | 测试已保存或环境变量存储的连通性   |

## GET `/vector-stores/types` - 获取支持的引擎类型

返回所有支持的引擎类型及其连接配置字段、索引配置字段的定义，可用于前端动态表单生成。系统级元数据，无需鉴权感知，但仍需 `X-API-Key`。

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
                { "name": "addr", "type": "string", "required": true, "description": "Elasticsearch URL (e.g., http://localhost:9200)" },
                { "name": "username", "type": "string", "required": false },
                { "name": "password", "type": "string", "required": false, "sensitive": true }
            ],
            "index_fields": [
                { "name": "index_name", "type": "string", "required": false, "default": "xwrag_default" },
                { "name": "number_of_shards", "type": "number", "required": false },
                { "name": "number_of_replicas", "type": "number", "required": false }
            ]
        },
        {
            "type": "postgres",
            "display_name": "PostgreSQL (Keywords + Vector)",
            "connection_fields": [
                { "name": "use_default_connection", "type": "boolean", "required": false, "default": true, "description": "Use the application's default database connection" },
                { "name": "addr", "type": "string", "required": false, "description": "PostgreSQL connection string (required if use_default_connection is false)" },
                { "name": "username", "type": "string", "required": false },
                { "name": "password", "type": "string", "required": false, "sensitive": true }
            ]
        }
    ]
}
```

## POST `/vector-stores/test` - 使用原始凭据测试连接

用前端表单中尚未保存的凭据执行一次连通性测试，不会写入数据库。成功时返回自动检测到的服务器版本（如 ES 版本号）；某些引擎（如 Milvus、SQLite）无法检测版本，`version` 会返回空字符串。

**参数说明（请求体）**:

| 字段              | 类型   | 必填 | 说明                                                          |
| ----------------- | ------ | ---- | ------------------------------------------------------------- |
| engine_type       | string | 是   | 引擎类型，取自 `/vector-stores/types` 的 `type`                |
| connection_config | object | 是   | 该引擎对应的连接配置字段（与 `connection_fields` 对应）         |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/vector-stores/test' \
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

**响应（失败）**:

```json
{
    "success": false,
    "error": "failed to connect to elasticsearch: connection refused or authentication failed"
}
```

> 注意：测试失败时 HTTP 状态码仍为 `200`，错误信息通过 `success: false` + `error` 字段返回。

## POST `/vector-stores` - 创建向量存储

为当前租户创建一个新的向量存储配置。同一 endpoint + index 组合在租户内不允许重复（与环境变量配置的存储也会冲突）。

**参数说明（请求体）**:

| 字段              | 类型   | 必填 | 说明                                                            |
| ----------------- | ------ | ---- | --------------------------------------------------------------- |
| name              | string | 是   | 存储显示名（租户内友好名）                                       |
| engine_type       | string | 是   | 引擎类型，取自 `/vector-stores/types`                            |
| connection_config | object | 是   | 连接配置（与所选引擎的 `connection_fields` 对应）                |
| index_config      | object | 否   | 索引配置（与所选引擎的 `index_fields` 对应）                     |

> Tencent VectorDB 使用 `engine_type: "tencent_vectordb"`。`connection_config` 中 `addr`、`username`、`api_key` 必填，`database` 可选；`index_config.collection_name` 表示集合名前缀，实际集合会按向量维度追加后缀（例如 `weknora_embeddings_768`）。该适配器同时支持向量检索和基于 BM25 sparse vector 的关键词检索；旧版本已创建且没有 `sparse_vector` 索引的集合需要重建并重新导入数据后才能启用关键词检索。

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

> 响应中的敏感字段（`password`、`api_key` 等）会被掩码为 `"***"`。`connection_config.version` 字段在连接测试成功后才会自动填充，创建时为空。

## GET `/vector-stores` - 获取向量存储列表

返回当前租户的所有向量存储，包含 `RETRIEVE_DRIVER` 环境变量配置的虚拟存储（`source: "env"`、`readonly: true`）和用户在 DB 中创建的存储（`source: "user"`、`readonly: false`）。环境变量存储排列在前。

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

## GET `/vector-stores/:id` - 获取向量存储详情

根据 ID 获取单个向量存储。支持 DB 存储 UUID 和 `__env_*` 形式的环境变量存储 ID（例如 `__env_postgres__`）。

**路径参数**:

| 字段 | 类型   | 必填 | 说明                                                |
| ---- | ------ | ---- | --------------------------------------------------- |
| id   | string | 是   | 向量存储 ID（DB UUID 或 `__env_{driver}__`）          |

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

## PUT `/vector-stores/:id` - 更新向量存储

仅支持更新 `name`。`engine_type`、`connection_config`、`index_config` 创建后不可变更；环境变量存储不可修改（返回 `400`）。

**路径参数**:

| 字段 | 类型   | 必填 | 说明           |
| ---- | ------ | ---- | -------------- |
| id   | string | 是   | 向量存储 ID    |

**参数说明（请求体）**:

| 字段 | 类型   | 必填 | 说明              |
| ---- | ------ | ---- | ----------------- |
| name | string | 是   | 新的存储显示名     |

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

## DELETE `/vector-stores/:id` - 删除向量存储

对 DB 中的存储执行软删除。环境变量存储不可删除（返回 `400`）。

**Phase 2 — 绑定保护**：

删除请求在事务中执行，并按 `(tenant_id, vector_store_id)` 复合索引统计当前租户中仍绑定到该存储的活跃知识库数量。**只要存在任意绑定的知识库（已软删除的 KB 不计入），删除即被拒绝**，调用者必须先解绑或删除这些知识库才能继续。在 PostgreSQL 上，事务期间会对 `vector_stores` 行加 `SELECT … FOR UPDATE` 行锁，阻止并发的知识库创建请求悄悄落到正在被删除的存储上（SQLite 上则依赖 WAL + 单写入序列化达成同样语义）。

**路径参数**:

| 字段 | 类型   | 必填 | 说明           |
| ---- | ------ | ---- | -------------- |
| id   | string | 是   | 向量存储 ID    |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/vector-stores/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx'
```

**响应（成功）**:

```json
{
    "success": true
}
```

**响应（绑定保护触发）**:

```json
{
    "success": false,
    "error": {
        "code": 1000,
        "message": "vector store still has 3 knowledge base(s) bound to it; unbind or delete them before removing the store"
    }
}
```

HTTP `400`。错误消息中包含具体的知识库数量（便于运营定位），但不包含任何 KB 的 ID/名称，以避免跨租户信息泄漏。删除被拒绝时，DB 中的存储行保持原状，进程内引擎注册表也不会被清除。

## POST `/vector-stores/:id/test` - 测试已保存或环境变量存储的连接

对已保存的 DB 存储或环境变量虚拟存储执行一次连接测试。成功时返回检测到的服务器版本；对 DB 存储，检测到的版本会被自动写回 `connection_config.version`，环境变量存储不会更新。

**路径参数**:

| 字段 | 类型   | 必填 | 说明                                                |
| ---- | ------ | ---- | --------------------------------------------------- |
| id   | string | 是   | 向量存储 ID（DB UUID 或 `__env_{driver}__`）          |

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

**响应（失败）**:

```json
{
    "success": false,
    "error": "failed to connect to elasticsearch: connection refused or authentication failed"
}
```

> 与 `/vector-stores/test` 一致，测试失败时 HTTP 状态码仍为 `200`，错误通过 `success: false` + `error` 返回。

## 环境变量存储

通过 `RETRIEVE_DRIVER` 环境变量配置的向量存储以虚拟条目形式出现在列表和详情中。这些条目的特征：

- **ID 格式**：`__env_{driver}__`（如 `__env_postgres__`、`__env_elasticsearch_v8__`）
- **source**：`"env"`
- **readonly**：`true`
- **不可修改/删除**：`PUT` 和 `DELETE` 返回 `400`
- **可测试连通性**：`POST /vector-stores/:id/test` 正常工作
- **被知识库绑定时**：未指定 `vector_store_id` 创建的知识库默认使用环境变量存储；这种知识库在响应中显示为 `vector_store_name="System default"` + `vector_store_source="env"`。

## 错误码

| HTTP 状态码 | code | 含义                                                |
| ----------- | ---- | --------------------------------------------------- |
| 400         | 1000 | 请求参数错误、校验失败、尝试修改环境变量存储、删除时仍有知识库绑定 |
| 400         | 2200 | 知识库创建时引用的 `vector_store_id` 无效（不存在或属于其他租户） |
| 400         | 2201 | 知识库创建时引用的存储当前不可用（DB 中存在但未注册到引擎） |
| 401         | 1001 | 未认证（缺少租户上下文或 API Key）                    |
| 404         | 1003 | 向量存储不存在                                       |
| 409         | 1005 | 同一 endpoint + index 组合已存在                     |
| 500         | 1007 | 内部服务器错误                                       |

> `2200` / `2201` 由 `POST /knowledge-bases` 等知识库创建路径返回（详见 [knowledge-base.md](./knowledge-base.md)），列于此处仅为完整覆盖与向量存储相关的所有错误码。
