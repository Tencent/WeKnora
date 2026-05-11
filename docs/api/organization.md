# 组织管理 API

[返回目录](./README.md)

组织（Organization，又称"空间"）是 WeKnora 的多租户协作单元。一个用户可以创建/加入多个组织，并以 owner / admin / editor / viewer 的角色参与；知识库与智能体可以共享到组织内，组织成员根据角色获得相应的访问权限。

本页文档覆盖以下六类接口：

- 组织管理：组织 CRUD、邀请码、搜索、加入与离开
- 成员管理：成员列表、角色变更、移除、邀请
- 加入请求：申请列表、审核
- 知识库共享：将知识库共享到组织 / 取消共享 / 修改权限
- 智能体共享：将智能体共享到组织 / 取消共享
- 我的共享视图：当前用户可访问的所有共享知识库 / 智能体

公共说明：
- 所有路径前缀为 `/api/v1`
- 鉴权头：`X-API-Key: sk-xxxxx`（或 `Authorization: Bearer ...`）
- 错误响应统一为 `{ "success": false, "error": "..." }`，HTTP 状态码遵循 RESTful 语义
- 角色 (`OrgMemberRole`) 取值：`owner` / `admin` / `editor` / `viewer`
- 共享权限 (`permission`) 取值：`viewer` / `editor`（创建时通常只允许这两个）

## 路由总览

### 组织管理

| 方法   | 路径                                          | 描述                                |
| ------ | --------------------------------------------- | ----------------------------------- |
| POST   | `/organizations`                              | 创建组织                            |
| GET    | `/organizations`                              | 获取我的组织列表（含资源数量）        |
| GET    | `/organizations/preview/:code`                | 通过邀请码预览组织（不加入）          |
| POST   | `/organizations/join`                         | 通过邀请码加入组织                  |
| POST   | `/organizations/join-request`                 | 提交加入申请（针对需要审核的组织）    |
| GET    | `/organizations/search`                       | 搜索可加入的可被搜索的组织            |
| POST   | `/organizations/join-by-id`                   | 通过组织 ID 加入可被搜索的组织        |
| GET    | `/organizations/:id`                          | 获取组织详情                        |
| PUT    | `/organizations/:id`                          | 更新组织                            |
| DELETE | `/organizations/:id`                          | 删除组织                            |
| POST   | `/organizations/:id/leave`                    | 离开组织                            |
| POST   | `/organizations/:id/request-upgrade`          | 现有成员申请角色升级                |
| POST   | `/organizations/:id/invite-code`              | 重新生成邀请码                      |

### 成员管理

| 方法   | 路径                                          | 描述                              |
| ------ | --------------------------------------------- | --------------------------------- |
| GET    | `/organizations/:id/search-users`             | 搜索可邀请的用户（仅管理员）       |
| POST   | `/organizations/:id/invite`                   | 直接添加用户为成员（仅管理员）     |
| GET    | `/organizations/:id/members`                  | 获取成员列表                      |
| PUT    | `/organizations/:id/members/:user_id`         | 更新成员角色                      |
| DELETE | `/organizations/:id/members/:user_id`         | 移除成员                          |

### 加入请求

| 方法 | 路径                                                    | 描述                       |
| ---- | ------------------------------------------------------- | -------------------------- |
| GET  | `/organizations/:id/join-requests`                      | 获取待审核加入申请列表       |
| PUT  | `/organizations/:id/join-requests/:request_id/review`   | 审核加入申请（仅管理员）     |

### 知识库共享

| 方法   | 路径                                          | 描述                                  |
| ------ | --------------------------------------------- | ------------------------------------- |
| POST   | `/knowledge-bases/:id/shares`                 | 将知识库共享到组织                    |
| GET    | `/knowledge-bases/:id/shares`                 | 获取该知识库的共享列表                 |
| PUT    | `/knowledge-bases/:id/shares/:share_id`       | 更新共享权限                          |
| DELETE | `/knowledge-bases/:id/shares/:share_id`       | 取消共享                              |
| GET    | `/organizations/:id/shares`                   | 获取组织下被共享进来的知识库列表        |
| GET    | `/organizations/:id/shared-knowledge-bases`   | 组织内全部知识库（含我共享的，空间视图） |

### 智能体共享

| 方法   | 路径                                          | 描述                                  |
| ------ | --------------------------------------------- | ------------------------------------- |
| POST   | `/agents/:id/shares`                          | 将智能体共享到组织                    |
| GET    | `/agents/:id/shares`                          | 获取该智能体的共享列表                |
| DELETE | `/agents/:id/shares/:share_id`                | 取消共享                              |
| GET    | `/organizations/:id/agent-shares`             | 获取组织下被共享进来的智能体列表        |
| GET    | `/organizations/:id/shared-agents`            | 组织内全部智能体（含我共享的，空间视图） |

### 我的共享视图

| 方法 | 路径                          | 描述                                    |
| ---- | ----------------------------- | --------------------------------------- |
| GET  | `/shared-knowledge-bases`     | 获取所有共享给我的知识库（跨组织）        |
| GET  | `/shared-agents`              | 获取所有共享给我的智能体（跨组织）        |

---

## 组织管理

### POST `/organizations` - 创建组织

**请求体**:

| 字段                        | 类型    | 必填 | 说明                                              |
| --------------------------- | ------- | ---- | ------------------------------------------------- |
| name                        | string  | 是   | 组织名称（1-255 字符）                            |
| description                 | string  | 否   | 组织描述（最多 1000 字符）                        |
| avatar                      | string  | 否   | 头像 URL（最多 512 字符）                         |
| invite_code_validity_days   | int     | 否   | 邀请码有效天数：`0`=永久，`1` / `7` / `30`，默认 7 |
| member_limit                | int     | 否   | 成员上限，`0`=不限，默认 50                       |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "AI 技术团队",
    "description": "专注于 AI 技术研究与知识管理",
    "invite_code_validity_days": 7,
    "member_limit": 50
}'
```

**响应**（`201 Created`）：

```json
{
    "data": {
        "id": "org-00000001",
        "name": "AI 技术团队",
        "description": "专注于 AI 技术研究与知识管理",
        "avatar": "",
        "owner_id": "user-00000001",
        "invite_code": "",
        "invite_code_validity_days": 7,
        "require_approval": false,
        "searchable": false,
        "member_limit": 50,
        "member_count": 1,
        "share_count": 0,
        "agent_share_count": 0,
        "pending_join_request_count": 0,
        "is_owner": true,
        "my_role": "owner",
        "has_pending_upgrade": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

### GET `/organizations` - 获取我的组织列表

返回当前用户所属的全部组织；`resource_counts` 字段附带每个空间内的知识库数与智能体数，供列表页侧栏直接渲染。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "organizations": [
            {
                "id": "org-00000001",
                "name": "AI 技术团队",
                "description": "专注于 AI 技术研究与知识管理",
                "owner_id": "user-00000001",
                "invite_code": "ABC123XY",
                "invite_code_expires_at": "2025-08-19T10:00:00+08:00",
                "invite_code_validity_days": 7,
                "require_approval": false,
                "searchable": false,
                "member_limit": 50,
                "member_count": 3,
                "share_count": 2,
                "agent_share_count": 1,
                "pending_join_request_count": 0,
                "is_owner": true,
                "my_role": "owner",
                "has_pending_upgrade": false,
                "created_at": "2025-08-12T10:00:00+08:00",
                "updated_at": "2025-08-12T10:00:00+08:00"
            }
        ],
        "total": 1,
        "resource_counts": {
            "knowledge_bases": { "by_organization": { "org-00000001": 5 } },
            "agents":         { "by_organization": { "org-00000001": 2 } }
        }
    },
    "success": true
}
```

### GET `/organizations/preview/:code` - 通过邀请码预览组织

不需要事先成为成员，可用于"加入前预览"页面。`:code` 为邀请码字符串。

**路径参数**:

| 字段 | 类型   | 说明   |
| ---- | ------ | ------ |
| code | string | 邀请码 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/preview/ABC123XY' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "id": "org-00000001",
        "name": "AI 技术团队",
        "description": "专注于 AI 技术研究与知识管理",
        "avatar": "",
        "member_count": 3,
        "share_count": 2,
        "agent_share_count": 1,
        "is_already_member": false,
        "require_approval": true,
        "created_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

### POST `/organizations/join` - 通过邀请码加入组织

仅适用于 `require_approval: false` 的组织。需要审核的组织请改用 `/organizations/join-request`。

**请求体**:

| 字段        | 类型   | 必填 | 说明                  |
| ----------- | ------ | ---- | --------------------- |
| invite_code | string | 是   | 邀请码（8-32 字符）   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/join' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "invite_code": "ABC123XY"
}'
```

**响应**: 返回加入后的组织信息（结构同 `GET /organizations/:id`）。

### POST `/organizations/join-request` - 提交加入申请

当组织开启审核（`require_approval: true`）时使用。

**请求体**:

| 字段        | 类型   | 必填 | 说明                                        |
| ----------- | ------ | ---- | ------------------------------------------- |
| invite_code | string | 是   | 邀请码（8-32 字符）                         |
| message     | string | 否   | 申请留言（最多 500 字符）                   |
| role        | string | 否   | 期望角色：`viewer` / `editor` / `admin`     |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/join-request' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "invite_code": "ABC123XY",
    "message": "希望加入团队参与知识库建设",
    "role": "editor"
}'
```

**响应**:

```json
{
    "data": {
        "id": "jr-00000001",
        "user_id": "user-00000002",
        "request_type": "join",
        "requested_role": "editor",
        "status": "pending",
        "created_at": "2025-08-14T10:00:00+08:00"
    },
    "success": true
}
```

### GET `/organizations/search` - 搜索可加入的组织

返回设置了 `searchable: true` 的组织。仅返回元数据，不返回邀请码。

**查询参数**:

| 字段  | 类型   | 必填 | 说明                              |
| ----- | ------ | ---- | --------------------------------- |
| q     | string | 否   | 搜索关键词（名称或描述模糊匹配）   |
| limit | int    | 否   | 返回数量（1-100，默认 20）         |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/search?q=AI&limit=10' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "id": "org-00000001",
            "name": "AI 技术团队",
            "description": "专注于 AI 技术研究与知识管理",
            "avatar": "",
            "member_count": 3,
            "member_limit": 50,
            "share_count": 2,
            "agent_share_count": 1,
            "is_already_member": false,
            "require_approval": true
        }
    ],
    "total": 1,
    "success": true
}
```

### POST `/organizations/join-by-id` - 通过组织 ID 加入

用于"搜索可加入空间"流程，无需邀请码；目标组织必须 `searchable: true`。如果组织开启了审核，会创建加入申请；否则直接加入。

**请求体**:

| 字段             | 类型   | 必填 | 说明                                       |
| ---------------- | ------ | ---- | ------------------------------------------ |
| organization_id  | string | 是   | 目标组织 ID                                |
| message          | string | 否   | 申请留言（最多 500 字符）                  |
| role             | string | 否   | 期望角色：`viewer` / `editor` / `admin`    |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/join-by-id' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "organization_id": "org-00000001",
    "message": "希望加入贵团队",
    "role": "viewer"
}'
```

**响应**: 返回加入后的组织信息（结构同 `GET /organizations/:id`）。

### GET `/organizations/:id` - 获取组织详情

**路径参数**:

| 字段 | 类型   | 说明    |
| ---- | ------ | ------- |
| id   | string | 组织 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "id": "org-00000001",
        "name": "AI 技术团队",
        "description": "专注于 AI 技术研究与知识管理",
        "avatar": "",
        "owner_id": "user-00000001",
        "invite_code": "ABC123XY",
        "invite_code_expires_at": "2025-08-19T10:00:00+08:00",
        "invite_code_validity_days": 7,
        "require_approval": false,
        "searchable": true,
        "member_limit": 50,
        "member_count": 3,
        "share_count": 2,
        "agent_share_count": 1,
        "pending_join_request_count": 1,
        "is_owner": true,
        "my_role": "owner",
        "has_pending_upgrade": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

`invite_code` 与 `invite_code_expires_at` 仅当前用户为 owner 或 admin 时返回。

### PUT `/organizations/:id` - 更新组织

**请求体**（全部字段均为可选，传 `null` 等同于不更新）:

| 字段                        | 类型    | 说明                                          |
| --------------------------- | ------- | --------------------------------------------- |
| name                        | string  | 组织名称（1-255 字符）                        |
| description                 | string  | 组织描述（最多 1000 字符）                    |
| avatar                      | string  | 头像 URL（最多 512 字符）                     |
| require_approval            | bool    | 加入是否需要审核                              |
| searchable                  | bool    | 是否在 `/organizations/search` 中可被发现     |
| invite_code_validity_days   | int     | 邀请码有效天数（0=永久，1/7/30）              |
| member_limit                | int     | 成员上限（0=不限）                            |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/organizations/org-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "description": "专注于 AI 技术研究与知识管理（更新）",
    "require_approval": true,
    "searchable": true
}'
```

**响应**: 返回更新后的组织信息（结构同 `GET /organizations/:id`）。

### DELETE `/organizations/:id` - 删除组织

仅 owner 可删除；删除会同时清理成员关系与共享记录。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/organizations/org-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{ "success": true }
```

### POST `/organizations/:id/leave` - 离开组织

owner 不能离开自己的组织，需先转让所有权或删除组织。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/organizations/org-00000001/leave' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{ "success": true, "message": "Left organization successfully" }
```

### POST `/organizations/:id/request-upgrade` - 申请角色升级

由已加入的成员主动发起，等待管理员审核（出现在 `/organizations/:id/join-requests` 中的 `request_type: "upgrade"`）。

**请求体**:

| 字段           | 类型   | 必填 | 说明                                                 |
| -------------- | ------ | ---- | ---------------------------------------------------- |
| requested_role | string | 是   | 期望角色：`viewer` / `editor` / `admin`              |
| message        | string | 否   | 申请理由（最多 500 字符）                            |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/organizations/org-00000001/request-upgrade' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "requested_role": "admin",
    "message": "需要管理员权限来管理知识库共享"
}'
```

**响应**:

```json
{
    "data": {
        "id": "jr-00000002",
        "request_type": "upgrade",
        "prev_role": "editor",
        "requested_role": "admin",
        "status": "pending",
        "created_at": "2025-08-14T11:00:00+08:00"
    },
    "success": true
}
```

### POST `/organizations/:id/invite-code` - 重新生成邀请码

仅 owner / admin 可调用，会让旧邀请码失效。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/organizations/org-00000001/invite-code' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": { "invite_code": "NEW1CODE" },
    "success": true
}
```

---

## 成员管理

### GET `/organizations/:id/search-users` - 搜索可邀请的用户

仅 owner / admin 可调用。匹配用户名或邮箱，自动排除已在该组织内的用户。

**查询参数**:

| 字段  | 类型   | 必填 | 说明                          |
| ----- | ------ | ---- | ----------------------------- |
| q     | string | 是   | 关键词（用户名或邮箱）         |
| limit | int    | 否   | 返回数量上限，默认 10          |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/search-users?q=zhang&limit=10' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "id": "user-00000002",
            "username": "zhangsan",
            "email": "zhangsan@example.com",
            "avatar": ""
        }
    ],
    "success": true
}
```

### POST `/organizations/:id/invite` - 直接邀请用户

仅 owner / admin 可调用，被邀请用户直接成为成员（无需审核）。

**请求体**:

| 字段    | 类型   | 必填 | 说明                                        |
| ------- | ------ | ---- | ------------------------------------------- |
| user_id | string | 是   | 被邀请用户的 ID                             |
| role    | string | 是   | 角色：`viewer` / `editor` / `admin`         |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/invite' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "user_id": "user-00000002",
    "role": "editor"
}'
```

**响应**:

```json
{ "success": true, "message": "Member added successfully" }
```

### GET `/organizations/:id/members` - 获取成员列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/members' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "members": [
            {
                "id": "mem-00000001",
                "user_id": "user-00000001",
                "username": "admin",
                "email": "admin@example.com",
                "avatar": "",
                "role": "owner",
                "tenant_id": 1,
                "joined_at": "2025-08-12T10:00:00+08:00"
            },
            {
                "id": "mem-00000002",
                "user_id": "user-00000002",
                "username": "zhangsan",
                "email": "zhangsan@example.com",
                "avatar": "",
                "role": "editor",
                "tenant_id": 2,
                "joined_at": "2025-08-13T09:00:00+08:00"
            }
        ],
        "total": 2
    },
    "success": true
}
```

### PUT `/organizations/:id/members/:user_id` - 更新成员角色

**路径参数**:

| 字段     | 类型   | 说明           |
| -------- | ------ | -------------- |
| id       | string | 组织 ID        |
| user_id  | string | 目标成员的用户 ID |

**请求体**:

| 字段 | 类型   | 必填 | 说明                                |
| ---- | ------ | ---- | ----------------------------------- |
| role | string | 是   | `viewer` / `editor` / `admin`       |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/organizations/org-00000001/members/user-00000002' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{ "role": "admin" }'
```

**响应**:

```json
{ "success": true }
```

### DELETE `/organizations/:id/members/:user_id` - 移除成员

仅 owner / admin 可调用，不能移除 owner。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/organizations/org-00000001/members/user-00000002' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{ "success": true }
```

---

## 加入请求

### GET `/organizations/:id/join-requests` - 获取待审核申请列表

仅 owner / admin 可调用。仅返回 `status: pending` 的记录；`request_type` 区分新加入申请（`join`）与升级申请（`upgrade`）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/join-requests' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "requests": [
            {
                "id": "jr-00000001",
                "user_id": "user-00000003",
                "username": "zhangwei",
                "email": "zhangwei@example.com",
                "message": "希望加入团队参与知识库建设",
                "request_type": "join",
                "prev_role": "",
                "requested_role": "editor",
                "status": "pending",
                "created_at": "2025-08-14T10:00:00+08:00"
            },
            {
                "id": "jr-00000002",
                "user_id": "user-00000002",
                "username": "zhangsan",
                "email": "zhangsan@example.com",
                "message": "需要管理员权限来管理知识库共享",
                "request_type": "upgrade",
                "prev_role": "editor",
                "requested_role": "admin",
                "status": "pending",
                "created_at": "2025-08-14T11:00:00+08:00"
            }
        ],
        "total": 2
    },
    "success": true
}
```

### PUT `/organizations/:id/join-requests/:request_id/review` - 审核加入申请

仅 owner / admin 可调用。

**路径参数**:

| 字段       | 类型   | 说明     |
| ---------- | ------ | -------- |
| id         | string | 组织 ID  |
| request_id | string | 申请 ID  |

**请求体**:

| 字段     | 类型   | 必填 | 说明                                                                  |
| -------- | ------ | ---- | --------------------------------------------------------------------- |
| approved | bool   | 是   | 是否通过                                                              |
| message  | string | 否   | 审核留言（最多 500 字符）                                             |
| role     | string | 否   | 通过时强制分配的角色（缺省按申请者请求的角色），仅 `viewer/editor/admin` |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/organizations/org-00000001/join-requests/jr-00000001/review' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "approved": true,
    "message": "欢迎加入",
    "role": "editor"
}'
```

**响应**:

```json
{ "success": true, "message": "Review completed" }
```

---

## 知识库共享

### POST `/knowledge-bases/:id/shares` - 共享知识库到组织

**路径参数**: `id` = 知识库 ID

**请求体**:

| 字段             | 类型   | 必填 | 说明                            |
| ---------------- | ------ | ---- | ------------------------------- |
| organization_id  | string | 是   | 目标组织 ID                     |
| permission       | string | 是   | 共享权限：`viewer` / `editor`   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "organization_id": "org-00000001",
    "permission": "viewer"
}'
```

**响应**（`201 Created`）：

```json
{
    "data": {
        "id": "kbs-00000001",
        "knowledge_base_id": "kb-00000001",
        "organization_id": "org-00000001",
        "shared_by_user_id": "user-00000001",
        "source_tenant_id": 1,
        "permission": "viewer",
        "created_at": "2025-08-15T10:00:00+08:00"
    },
    "success": true
}
```

### GET `/knowledge-bases/:id/shares` - 获取知识库共享列表

返回该知识库被共享到的所有组织。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "shares": [
            {
                "id": "kbs-00000001",
                "knowledge_base_id": "kb-00000001",
                "knowledge_base_name": "技术文档库",
                "knowledge_base_type": "document",
                "knowledge_count": 12,
                "chunk_count": 0,
                "organization_id": "org-00000001",
                "organization_name": "AI 技术团队",
                "shared_by_user_id": "user-00000001",
                "shared_by_username": "admin",
                "source_tenant_id": 1,
                "permission": "viewer",
                "my_role_in_org": "owner",
                "my_permission": "viewer",
                "created_at": "2025-08-15T10:00:00+08:00"
            }
        ],
        "total": 1
    },
    "success": true
}
```

### PUT `/knowledge-bases/:id/shares/:share_id` - 更新共享权限

**路径参数**:

| 字段     | 类型   | 说明           |
| -------- | ------ | -------------- |
| id       | string | 知识库 ID      |
| share_id | string | 共享记录 ID    |

**请求体**:

| 字段       | 类型   | 必填 | 说明                          |
| ---------- | ------ | ---- | ----------------------------- |
| permission | string | 是   | 新权限：`viewer` / `editor`   |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares/kbs-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{ "permission": "editor" }'
```

**响应**:

```json
{ "success": true }
```

### DELETE `/knowledge-bases/:id/shares/:share_id` - 取消知识库共享

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares/kbs-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{ "success": true }
```

### GET `/organizations/:id/shares` - 获取组织下被共享进来的知识库

仅本组织成员可查看。`my_permission = min(permission, my_role_in_org)`，即当前用户的有效权限。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/shares' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**: 结构同 `GET /knowledge-bases/:id/shares`。

### GET `/organizations/:id/shared-knowledge-bases` - 组织内知识库空间视图

供"切换到某个空间后"的知识库列表页使用。返回该组织中所有当前用户可见的知识库，包含：

1. 通过 `POST /knowledge-bases/:id/shares` 直接共享进来的知识库
2. 通过共享智能体（`agents/:id/shares`）携带进来的知识库（只读，`source_from_agent` 字段标识来源）
3. 当前用户自己共享出去的知识库（`is_mine: true`）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/shared-knowledge-bases' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "knowledge_base": {
                "id": "kb-00000001",
                "name": "技术文档库",
                "type": "document"
            },
            "share_id": "kbs-00000001",
            "organization_id": "org-00000001",
            "org_name": "AI 技术团队",
            "permission": "viewer",
            "source_tenant_id": 1,
            "shared_at": "2025-08-15T10:00:00+08:00",
            "is_mine": false,
            "source_from_agent": {
                "agent_id": "agent-00000005",
                "agent_name": "智能客服助手",
                "kb_selection_mode": "selected"
            }
        }
    ],
    "total": 1,
    "success": true
}
```

`source_from_agent` 仅在该 KB 是通过共享智能体引入时出现；直接共享的 KB 不含该字段。

---

## 智能体共享

### POST `/agents/:id/shares` - 共享智能体到组织

仅当前用户在目标组织中具有 editor 或 admin 角色时允许；智能体必须已完成配置（必填的对话模型、若启用了 knowledge_search 工具需配置重排模型等）。

**路径参数**: `id` = 智能体 ID

**请求体**:

| 字段             | 类型   | 必填 | 说明                            |
| ---------------- | ------ | ---- | ------------------------------- |
| organization_id  | string | 是   | 目标组织 ID                     |
| permission       | string | 是   | 共享权限：`viewer` / `editor`   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/shares' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "organization_id": "org-00000001",
    "permission": "viewer"
}'
```

**响应**（`201 Created`）：

```json
{
    "data": {
        "id": "as-00000001",
        "agent_id": "agent-00000001",
        "organization_id": "org-00000001",
        "shared_by_user_id": "user-00000001",
        "source_tenant_id": 1,
        "permission": "viewer",
        "created_at": "2025-08-15T11:00:00+08:00"
    },
    "success": true
}
```

### GET `/agents/:id/shares` - 获取智能体共享列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/shares' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "shares": [
            {
                "id": "as-00000001",
                "agent_id": "agent-00000001",
                "organization_id": "org-00000001",
                "organization_name": "AI 技术团队",
                "shared_by_user_id": "user-00000001",
                "source_tenant_id": 1,
                "permission": "viewer",
                "created_at": "2025-08-15T11:00:00+08:00"
            }
        ],
        "total": 1
    },
    "success": true
}
```

### DELETE `/agents/:id/shares/:share_id` - 取消智能体共享

只有共享者本人或拥有相应权限的管理员可以取消。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/agents/agent-00000001/shares/as-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{ "success": true, "message": "Share removed successfully" }
```

### GET `/organizations/:id/agent-shares` - 获取组织下被共享进来的智能体

仅本组织成员可查看。返回结构在 `AgentShareResponse` 基础上额外补充 `my_role_in_org` / `my_permission`，并附带智能体的能力范围摘要（`scope_kb` / `scope_web_search` / `scope_mcp` 等）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/agent-shares' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "shares": [
            {
                "id": "as-00000001",
                "agent_id": "agent-00000001",
                "agent_name": "智能客服助手",
                "agent_avatar": "🤖",
                "organization_id": "org-00000001",
                "organization_name": "AI 技术团队",
                "shared_by_user_id": "user-00000001",
                "shared_by_username": "admin",
                "source_tenant_id": 1,
                "permission": "viewer",
                "my_role_in_org": "editor",
                "my_permission": "viewer",
                "created_at": "2025-08-15T11:00:00+08:00",
                "scope_kb": "selected",
                "scope_kb_count": 2,
                "scope_web_search": true,
                "scope_mcp": "none"
            }
        ],
        "total": 1
    },
    "success": true
}
```

### GET `/organizations/:id/shared-agents` - 组织内智能体空间视图

供"切换到某个空间后"的智能体列表页使用。返回该组织中所有当前用户可见的智能体，包含他人共享与本人共享（`is_mine: true`）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/shared-agents' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "agent": {
                "id": "agent-00000001",
                "name": "智能客服助手"
            },
            "share_id": "as-00000001",
            "organization_id": "org-00000001",
            "org_name": "AI 技术团队",
            "permission": "viewer",
            "source_tenant_id": 1,
            "shared_at": "2025-08-15T11:00:00+08:00",
            "shared_by_user_id": "user-00000001",
            "shared_by_username": "admin",
            "disabled_by_me": false,
            "is_mine": false
        }
    ],
    "total": 1,
    "success": true
}
```

---

## 我的共享视图

### GET `/shared-knowledge-bases` - 获取共享给我的知识库（跨组织）

返回当前用户通过所有组织（不限制 `:id`）共享获得的知识库列表，供"全部知识库"视图使用。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/shared-knowledge-bases' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "knowledge_base": {
                "id": "kb-00000001",
                "name": "技术文档库"
            },
            "share_id": "kbs-00000001",
            "organization_id": "org-00000001",
            "org_name": "AI 技术团队",
            "permission": "viewer",
            "source_tenant_id": 1,
            "shared_at": "2025-08-15T10:00:00+08:00"
        }
    ],
    "total": 1,
    "success": true
}
```

### GET `/shared-agents` - 获取共享给我的智能体（跨组织）

返回当前用户通过所有组织共享获得的智能体列表，供"全部智能体"视图使用。`disabled_by_me: true` 表示当前租户已在对话下拉中隐藏该智能体。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/shared-agents' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "agent": {
                "id": "agent-00000001",
                "name": "智能客服助手"
            },
            "share_id": "as-00000001",
            "organization_id": "org-00000001",
            "org_name": "AI 技术团队",
            "permission": "viewer",
            "source_tenant_id": 1,
            "shared_at": "2025-08-15T11:00:00+08:00",
            "shared_by_user_id": "user-00000001",
            "shared_by_username": "admin",
            "disabled_by_me": false
        }
    ],
    "total": 1,
    "success": true
}
```

