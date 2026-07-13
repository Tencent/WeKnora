# 系统管理 API（管理员）

[返回目录](./README.md)

系统管理员接口用于平台级别的管理操作，包括管理员权限管理、系统设置和跨租户操作。所有接口需要 SystemAdmin 角色。

## 说明

- **权限要求**：所有接口需要调用者是 SystemAdmin（`User.IsSystemAdmin == true`）。
- 认证方式：`X-API-Key` 或 `Authorization: Bearer`。
- 这些操作是**平台级**的，独立于租户的 Owner/Admin/Contributor/Viewer 角色。

## 端点一览

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| POST | `/system/admin/promote` | 提升用户为系统管理员 |
| POST | `/system/admin/revoke` | 撤销系统管理员权限 |
| GET | `/system/admin/list` | 获取系统管理员列表 |
| GET | `/system/admin/settings` | 获取所有系统设置 |
| GET | `/system/admin/settings/:key` | 获取指定系统设置 |
| PUT | `/system/admin/settings/:key` | 更新或创建系统设置 |
| DELETE | `/system/admin/settings/:key` | 重置系统设置为默认值 |
| POST | `/system/admin/tenants/apply-default-storage-quota` | 批量应用默认存储配额 |
| GET | `/system/admin/audit-log` | 获取平台审计日志 |

---

## POST `/system/admin/promote` - 提升用户为系统管理员

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `user_id` | string | 是 | 目标用户 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/system/admin/promote' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "user_id": "usr-00000001"
}'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "id": "usr-00000001",
        "username": "admin",
        "is_system_admin": true
    }
}
```

**错误**: 非 SystemAdmin → 403；用户不存在 → 404。

---

## POST `/system/admin/revoke` - 撤销系统管理员权限

不能撤销最后一个 SystemAdmin，也不能自我撤销。

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `user_id` | string | 是 | 目标用户 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/system/admin/revoke' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "user_id": "usr-00000001"
}'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "id": "usr-00000001",
        "username": "user1",
        "is_system_admin": false
    }
}
```

**错误**: 非 SystemAdmin → 403；会移除最后一个管理员 → 400；自我撤销 → 400。

---

## GET `/system/admin/list` - 获取系统管理员列表

**查询参数**:

| 字段 | 类型 | 默认 | 说明 |
| ---- | ---- | ---- | ---- |
| `offset` | int | 0 | 偏移量 |
| `limit` | int | 50 | 每页条数（最大 200） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/system/admin/list?offset=0&limit=50' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "items": [
            {
                "id": "usr-00000001",
                "username": "admin",
                "email": "admin@example.com",
                "is_system_admin": true
            }
        ],
        "total": 1
    }
}
```

---

## GET `/system/admin/settings` - 获取所有系统设置

返回所有 DB 存储的运行时可调系统设置。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/system/admin/settings' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "key": "file.max_size_mb",
            "value": "100",
            "type": "number",
            "description": "上传文件最大大小（MB）"
        },
        {
            "key": "tenant.default_storage_quota",
            "value": "10737418240",
            "type": "number",
            "description": "默认租户存储配额（字节）"
        }
    ]
}
```

---

## GET `/system/admin/settings/:key` - 获取指定系统设置

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `key` | string | 设置键名（如 `file.max_size_mb`） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/system/admin/settings/file.max_size_mb' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "key": "file.max_size_mb",
        "value": "100",
        "type": "number",
        "description": "上传文件最大大小（MB）"
    }
}
```

---

## PUT `/system/admin/settings/:key` - 更新系统设置

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `key` | string | 设置键名 |

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `value` | any | 是 | 新值（类型需与设置键的类型匹配） |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/system/admin/settings/file.max_size_mb' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "value": "200"
}'
```

**响应**（200 OK）: 返回更新后的设置行，结构同 GET。

---

## DELETE `/system/admin/settings/:key` - 重置系统设置

将指定设置恢复为默认值。幂等操作。

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `key` | string | 设置键名 |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/system/admin/settings/file.max_size_mb' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "Setting reset acknowledged"
}
```

---

## POST `/system/admin/tenants/apply-default-storage-quota` - 批量应用默认存储配额

将当前的默认存储配额设置写入所有现有租户。不修改系统设置本身——只写入租户行。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/system/admin/tenants/apply-default-storage-quota' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "affected": 5,
        "quota_bytes": 10737418240
    }
}
```

---

## GET `/system/admin/audit-log` - 获取平台审计日志

获取 `tenant_id=0` 的平台级审计事件（设定变更、管理员提权/撤销等）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/system/admin/audit-log' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）: 审计日志列表，结构同租户审计日志接口。
