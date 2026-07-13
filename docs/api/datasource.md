# 数据源管理 API

[返回目录](./README.md)

数据源接口用于管理外部数据连接器（飞书、Notion、语雀、RSS 等），支持连接验证、资源浏览、同步管理。

## 说明

- 所有接口需要认证（`X-API-Key` 或 `Authorization: Bearer`）。
- 数据源包含外部服务凭证，仅该租户可访问。
- 完整集成流程见 [../数据源导入开发文档.md](../数据源导入开发文档.md)。

## 端点一览

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/datasource/types` | 获取可用连接器类型列表 |
| POST | `/datasource/validate-credentials` | 验证凭证（不持久化） |
| POST | `/datasource` | 创建数据源 |
| GET | `/datasource` | 获取数据源列表 |
| GET | `/datasource/:id` | 获取数据源详情 |
| PUT | `/datasource/:id` | 更新数据源 |
| DELETE | `/datasource/:id` | 删除数据源 |
| PUT | `/datasource/:id/credentials` | 设置凭证字段 |
| DELETE | `/datasource/:id/credentials/:field` | 删除凭证字段 |
| POST | `/datasource/:id/validate` | 验证数据源连接 |
| GET | `/datasource/:id/resources` | 获取可用资源列表 |
| POST | `/datasource/:id/resource-ancestors` | 获取资源的上级路径 |
| POST | `/datasource/:id/sync` | 手动触发同步 |
| POST | `/datasource/:id/pause` | 暂停数据源 |
| POST | `/datasource/:id/resume` | 恢复数据源 |
| GET | `/datasource/:id/logs` | 获取同步日志 |
| GET | `/datasource/logs/:log_id` | 获取单条同步日志 |

---

## GET `/datasource/types` - 获取可用连接器类型列表

返回所有已注册的数据源连接器元数据。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/datasource/types' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "type": "feishu",
            "name": "飞书文档",
            "description": "导入飞书文档和知识库",
            "icon": "feishu.svg"
        },
        {
            "type": "notion",
            "name": "Notion",
            "description": "导入 Notion 页面和数据库",
            "icon": "notion.svg"
        },
        {
            "type": "yuque",
            "name": "语雀",
            "description": "导入语雀文档",
            "icon": "yuque.svg"
        }
    ]
}
```

---

## POST `/datasource/validate-credentials` - 验证凭证

验证凭证是否有效，不保存任何数据。用于"测试连接"按钮。

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `type` | string | 是 | 连接器类型 |
| `credentials` | object | 是 | 连接器凭证（各类型字段不同） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/datasource/validate-credentials' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "type": "feishu",
    "credentials": {
        "app_id": "cli_xxx",
        "app_secret": "xxx"
    }
}'
```

**响应**（200 OK，凭证有效）:

```json
{
    "success": true,
    "message": "凭证验证成功"
}
```

**错误**: 凭证无效 → 400。

---

## POST `/datasource` - 创建数据源

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `name` | string | 是 | 数据源名称 |
| `type` | string | 是 | 连接器类型 |
| `knowledge_base_id` | string | 是 | 关联的知识库 ID |
| `credentials` | object | 是 | 连接器凭证 |
| `sync_interval` | int | 否 | 同步间隔（秒，0 表示不自动同步） |
| `enabled` | bool | 否 | 是否启用（默认 true） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/datasource' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "name": "飞书文档导入",
    "type": "feishu",
    "knowledge_base_id": "kb-00000001",
    "credentials": {
        "app_id": "cli_xxx",
        "app_secret": "xxx"
    },
    "sync_interval": 3600,
    "enabled": true
}'
```

**响应**（201 Created）: 返回完整的 DataSource 对象。

---

## GET `/datasource` - 获取数据源列表

**查询参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `kb_id` | string | 按知识库 ID 过滤 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/datasource?kb_id=kb-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "id": "ds-00000001",
            "name": "飞书文档导入",
            "type": "feishu",
            "knowledge_base_id": "kb-00000001",
            "sync_interval": 3600,
            "enabled": true,
            "last_sync_at": "2025-08-12T10:00:00+08:00",
            "created_at": "2025-08-12T09:00:00+08:00"
        }
    ]
}
```

---

## GET `/datasource/:id` - 获取数据源详情

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 数据源 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/datasource/ds-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）: 返回完整 DataSource 对象（含脱敏后的凭证字段）。

---

## PUT `/datasource/:id` - 更新数据源

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 数据源 ID |

**请求体**: 同创建，所有字段可选。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/datasource/ds-00000001' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "name": "飞书文档导入 V2",
    "sync_interval": 7200
}'
```

**响应**（200 OK）: 返回更新后的 DataSource 对象。

---

## DELETE `/datasource/:id` - 删除数据源

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 数据源 ID |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/datasource/ds-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "数据源已删除"
}
```

---

## PUT `/datasource/:id/credentials` - 设置凭证字段

按字段设置数据源凭证，凭证从不经由主 PUT 体传输。

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 数据源 ID |

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `field` | string | 是 | 字段名（如 `app_secret`） |
| `value` | string | 是 | 字段值 |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/datasource/ds-00000001/credentials' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "field": "app_secret",
    "value": "new_secret_value"
}'
```

**响应**（200 OK）:

```json
{
    "success": true
}
```

---

## DELETE `/datasource/:id/credentials/:field` - 删除凭证字段

---

## POST `/datasource/:id/validate` - 验证数据源连接

使用已保存的凭证测试与外部服务的连接。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/datasource/ds-00000001/validate' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK，连接正常）:

```json
{
    "success": true,
    "message": "连接验证成功"
}
```

**错误**: 连接失败 → 400（含错误详情）。

---

## GET `/datasource/:id/resources` - 获取可用资源列表

浏览数据源中可导入的资源（如飞书文件夹/文档树）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/datasource/ds-00000001/resources' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "id": "res-001",
            "name": "产品文档",
            "type": "folder",
            "children": []
        },
        {
            "id": "res-002",
            "name": "技术方案.docx",
            "type": "file",
            "size": 102400
        }
    ]
}
```

---

## POST `/datasource/:id/resource-ancestors` - 获取资源上级路径

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `resource_id` | string | 是 | 资源 ID |

---

## POST `/datasource/:id/sync` - 手动触发同步

立即触发一次全量同步（异步任务）。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/datasource/ds-00000001/sync' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "同步任务已启动",
    "data": {
        "task_id": "sync_1_ds-00000001_xxxxx"
    }
}
```

---

## POST `/datasource/:id/pause` - 暂停数据源

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/datasource/ds-00000001/pause' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "数据源已暂停"
}
```

---

## POST `/datasource/:id/resume` - 恢复数据源

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/datasource/ds-00000001/resume' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "数据源已恢复"
}
```

---

## GET `/datasource/:id/logs` - 获取同步日志

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/datasource/ds-00000001/logs' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "id": "log-00000001",
            "status": "completed",
            "message": "同步完成，导入 5 条知识",
            "started_at": "2025-08-12T10:00:00+08:00",
            "finished_at": "2025-08-12T10:05:00+08:00"
        }
    ]
}
```

---

## GET `/datasource/logs/:log_id` - 获取单条同步日志详情

---

## 枚举值

### 同步状态 (Sync Status)

| 值 | 说明 |
| ---- | ---- |
| `pending` | 等待执行 |
| `running` | 同步中 |
| `completed` | 同步完成 |
| `failed` | 同步失败 |
| `cancelled` | 已取消 |
