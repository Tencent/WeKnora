# 邀请管理 API

[返回目录](./README.md)

我的邀请接口用于查看和处理发送给当前用户的租户邀请。

## 说明

- 所有接口需要认证（`X-API-Key` 或 `Authorization: Bearer`）。
- 这些接口仅作用于**当前用户**收到的邀请——无法查看或处理他人的邀请。
- 租户管理员发送邀请和查看租户下所有邀请的接口见 [tenant.md](./tenant.md)。

## 端点一览

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/me/invitations` | 获取我的邀请列表 |
| GET | `/me/invitations/pending-count` | 获取待处理邀请数量 |
| POST | `/me/invitations/:inv_id/accept` | 接受邀请 |
| POST | `/me/invitations/:inv_id/decline` | 拒绝邀请 |

---

## GET `/me/invitations` - 获取我的邀请列表

返回发送给当前用户的所有租户邀请。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/me/invitations' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "id": "inv-00000001",
            "tenant_id": 2,
            "tenant_name": "研发团队工作区",
            "inviter_name": "admin",
            "role": "editor",
            "status": "pending",
            "created_at": "2025-08-12T10:00:00+08:00"
        }
    ]
}
```

**响应字段说明**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `id` | string | 邀请 ID |
| `tenant_id` | uint64 | 目标租户 ID |
| `tenant_name` | string | 目标租户名称 |
| `inviter_name` | string | 邀请人用户名 |
| `role` | string | 邀请的角色：`admin` / `editor` / `viewer` |
| `status` | string | 邀请状态：`pending`（待处理） |
| `created_at` | string | 邀请发送时间 |

---

## GET `/me/invitations/pending-count` - 获取待处理邀请数量

轻量端点，用于顶部头像行的红点 badge 轮询。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/me/invitations/pending-count' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "count": 2
    }
}
```

---

## POST `/me/invitations/:inv_id/accept` - 接受邀请

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `inv_id` | string | 邀请 ID |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/me/invitations/inv-00000001/accept' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "已加入租户"
}
```

**错误**: 邀请不存在或已过期 → 400/404。

---

## POST `/me/invitations/:inv_id/decline` - 拒绝邀请

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `inv_id` | string | 邀请 ID |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/me/invitations/inv-00000001/decline' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "已拒绝邀请"
}
```

---

## 枚举值

### 邀请状态 (Invitation Status)

| 值 | 说明 |
| ---- | ---- |
| `pending` | 待处理（等待被邀请人接受或拒绝） |
| `accepted` | 已接受 |
| `declined` | 已拒绝 |
| `expired` | 已过期 |
| `revoked` | 已被邀请人撤销 |
