# 用户收藏 API

[返回目录](./README.md)

用户收藏接口用于管理当前用户在租户内的资源收藏（知识库、智能体）。

## 说明

- 收藏是**个人**导航辅助，不是共享资源——不能查看或修改其他用户的收藏。
- 所有接口需要认证（`X-API-Key` 或 `Authorization: Bearer`），且自动限定为当前用户和当前租户。

## 端点一览

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/user/favorites` | 获取我的收藏列表 |
| POST | `/user/favorites` | 添加收藏 |
| DELETE | `/user/favorites/:type/:id` | 取消收藏 |

---

## GET `/user/favorites` - 获取收藏列表

获取当前用户在租户内的收藏列表。

**查询参数**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `type` | string | 是 | 资源类型：`kb`（知识库）或 `agent`（智能体） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/user/favorites?type=kb' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "resource_type": "kb",
            "resource_id": "kb-00000001"
        }
    ]
}
```

---

## POST `/user/favorites` - 添加收藏

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `type` | string | 是 | 资源类型：`kb` 或 `agent` |
| `id` | string | 是 | 资源 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/user/favorites' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "type": "kb",
    "id": "kb-00000001"
}'
```

**响应**（200 OK）:

```json
{
    "success": true
}
```

---

## DELETE `/user/favorites/:type/:id` - 取消收藏

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `type` | string | 资源类型：`kb` 或 `agent` |
| `id` | string | 资源 ID |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/user/favorites/kb/kb-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true
}
```

---

## 枚举值

### 资源类型 (Resource Type)

| 值 | 说明 |
| ---- | ---- |
| `kb` | 知识库 |
| `agent` | 智能体 |
