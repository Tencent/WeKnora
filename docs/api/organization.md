# 组织管理 API

[返回目录](./README.md)

## 组织 CRUD

| 方法   | 路径                      | 描述             |
| ------ | ------------------------- | ---------------- |
| POST   | `/organizations`          | 创建组织         |
| GET    | `/organizations`          | 获取我的组织列表 |
| GET    | `/organizations/:id`      | 获取组织详情     |
| PUT    | `/organizations/:id`      | 更新组织         |
| DELETE | `/organizations/:id`      | 删除组织         |

## 成员管理

| 方法   | 路径                                          | 描述               |
| ------ | --------------------------------------------- | ------------------ |
| POST   | `/organizations/join`                         | 通过邀请码加入组织 |
| POST   | `/organizations/join-request`                 | 提交加入申请       |
| GET    | `/organizations/search`                       | 搜索组织           |
| POST   | `/organizations/join-by-id`                   | 通过组织ID加入     |
| GET    | `/organizations/preview/:invite_code`         | 预览组织信息       |
| POST   | `/organizations/:id/leave`                    | 离开组织           |
| POST   | `/organizations/:id/request-upgrade`          | 请求角色升级       |
| POST   | `/organizations/:id/invite-code`              | 生成邀请码         |
| GET    | `/organizations/:id/search-users`             | 搜索可邀请用户     |
| POST   | `/organizations/:id/invite`                   | 邀请成员           |
| GET    | `/organizations/:id/members`                  | 获取成员列表       |
| PUT    | `/organizations/:id/members/:user_id`         | 更新成员角色       |
| DELETE | `/organizations/:id/members/:user_id`         | 移除成员           |

## 加入请求

| 方法 | 路径                                                    | 描述             |
| ---- | ------------------------------------------------------- | ---------------- |
| GET  | `/organizations/:id/join-requests`                      | 获取加入请求列表 |
| PUT  | `/organizations/:id/join-requests/:request_id/review`   | 审核加入请求     |

## 知识库共享

| 方法   | 路径                                          | 描述             |
| ------ | --------------------------------------------- | ---------------- |
| POST   | `/knowledge-bases/:id/shares`                 | 共享知识库       |
| GET    | `/knowledge-bases/:id/shares`                 | 获取知识库共享列表 |
| PUT    | `/knowledge-bases/:id/shares/:share_id`       | 更新共享权限     |
| DELETE | `/knowledge-bases/:id/shares/:share_id`       | 取消知识库共享   |

## 智能体共享

| 方法   | 路径                                    | 描述             |
| ------ | --------------------------------------- | ---------------- |
| POST   | `/agents/:id/shares`                    | 共享智能体       |
| GET    | `/agents/:id/shares`                    | 获取智能体共享列表 |
| DELETE | `/agents/:id/shares/:share_id`          | 取消智能体共享   |

## 共享资源

| 方法 | 路径                        | 描述               |
| ---- | --------------------------- | ------------------ |
| GET  | `/shared-knowledge-bases`   | 获取共享知识库列表 |
| GET  | `/shared-agents`            | 获取共享智能体列表 |

---

## POST `/organizations` - 创建组织

**请求参数**:
- `name`: 组织名称（必填）
- `description`: 组织描述（可选）
- `avatar`: 组织头像 URL（可选）
- `invite_code_validity_days`: 邀请码有效天数（可选）
- `member_limit`: 成员上限（可选）

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

**响应**:

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

## GET `/organizations` - 获取我的组织列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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
                "avatar": "",
                "owner_id": "user-00000001",
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
        ]
    },
    "success": true
}
```

## GET `/organizations/:id` - 获取组织详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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

## PUT `/organizations/:id` - 更新组织

**请求参数**（均为可选）:
- `name`: 组织名称
- `description`: 组织描述
- `avatar`: 组织头像 URL
- `require_approval`: 是否需要审核加入
- `searchable`: 是否可被搜索
- `invite_code_validity_days`: 邀请码有效天数
- `member_limit`: 成员上限

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

**响应**:

```json
{
    "data": {
        "id": "org-00000001",
        "name": "AI 技术团队",
        "description": "专注于 AI 技术研究与知识管理（更新）",
        "avatar": "",
        "owner_id": "user-00000001",
        "invite_code_validity_days": 7,
        "require_approval": true,
        "searchable": true,
        "member_limit": 50,
        "member_count": 3,
        "share_count": 2,
        "agent_share_count": 1,
        "pending_join_request_count": 0,
        "is_owner": true,
        "my_role": "owner",
        "has_pending_upgrade": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T12:00:00+08:00"
    },
    "success": true
}
```

## DELETE `/organizations/:id` - 删除组织

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/organizations/org-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true
}
```

---

## POST `/organizations/join` - 通过邀请码加入组织

**请求参数**:
- `invite_code`: 邀请码（必填）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/join' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "invite_code": "ABC123XY"
}'
```

**响应**:

```json
{
    "success": true
}
```

## POST `/organizations/join-request` - 提交加入申请

当组织开启了审核加入（`require_approval: true`）时使用。

**请求参数**:
- `invite_code`: 邀请码（必填）
- `message`: 申请留言（可选）
- `role`: 申请角色（可选）

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
    "success": true
}
```

## GET `/organizations/search` - 搜索组织

**查询参数**:
- `keyword`: 搜索关键字（可选）
- `page`: 页码（默认 1）
- `page_size`: 每页条数（默认 20）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/search?keyword=AI&page=1&page_size=10' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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
                "avatar": "",
                "owner_id": "user-00000001",
                "invite_code_validity_days": 7,
                "require_approval": true,
                "searchable": true,
                "member_limit": 50,
                "member_count": 3,
                "share_count": 2,
                "agent_share_count": 1,
                "pending_join_request_count": 0,
                "is_owner": false,
                "my_role": "",
                "has_pending_upgrade": false,
                "created_at": "2025-08-12T10:00:00+08:00",
                "updated_at": "2025-08-12T10:00:00+08:00"
            }
        ]
    },
    "success": true
}
```

## POST `/organizations/join-by-id` - 通过组织ID加入

**请求参数**:
- `organization_id`: 组织 ID（必填）
- `message`: 申请留言（可选）
- `role`: 申请角色（可选）

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

**响应**:

```json
{
    "success": true
}
```

## GET `/organizations/preview/:invite_code` - 预览组织信息

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/preview/ABC123XY' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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
        "invite_code_validity_days": 7,
        "require_approval": true,
        "searchable": true,
        "member_limit": 50,
        "member_count": 3,
        "share_count": 0,
        "agent_share_count": 0,
        "pending_join_request_count": 0,
        "is_owner": false,
        "my_role": "",
        "has_pending_upgrade": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

## POST `/organizations/:id/leave` - 离开组织

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/organizations/org-00000001/leave' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true
}
```

## POST `/organizations/:id/request-upgrade` - 请求角色升级

**请求参数**:
- `requested_role`: 期望角色（必填）
- `message`: 申请理由（可选）

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
    "success": true
}
```

## POST `/organizations/:id/invite-code` - 生成邀请码

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/organizations/org-00000001/invite-code' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "invite_code": "NEW1CODE"
    },
    "success": true
}
```

## GET `/organizations/:id/search-users` - 搜索可邀请用户

**查询参数**:
- `keyword`: 用户名或邮箱关键字（可选）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/search-users?keyword=zhang' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "user-00000002",
            "username": "zhangsan",
            "email": "zhangsan@example.com"
        },
        {
            "id": "user-00000003",
            "username": "zhangwei",
            "email": "zhangwei@example.com"
        }
    ],
    "success": true
}
```

## POST `/organizations/:id/invite` - 邀请成员

**请求参数**:
- `user_id`: 用户 ID（必填）
- `role`: 角色（必填）

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
{
    "success": true
}
```

## GET `/organizations/:id/members` - 获取成员列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/members' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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
        ]
    },
    "success": true
}
```

## PUT `/organizations/:id/members/:user_id` - 更新成员角色

**请求参数**:
- `role`: 新角色（必填）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/organizations/org-00000001/members/user-00000002' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "role": "admin"
}'
```

**响应**:

```json
{
    "success": true
}
```

## DELETE `/organizations/:id/members/:user_id` - 移除成员

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/organizations/org-00000001/members/user-00000002' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true
}
```

---

## GET `/organizations/:id/join-requests` - 获取加入请求列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/organizations/org-00000001/join-requests' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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
            }
        ]
    },
    "success": true
}
```

## PUT `/organizations/:id/join-requests/:request_id/review` - 审核加入请求

**请求参数**:
- `approved`: 是否批准（必填，布尔值）
- `message`: 审核留言（可选）
- `role`: 分配角色（可选，批准时生效）

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
{
    "success": true
}
```

---

## POST `/knowledge-bases/:id/shares` - 共享知识库

**请求参数**:
- `organization_id`: 目标组织 ID（必填）
- `permission`: 权限级别（必填）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "organization_id": "org-00000001",
    "permission": "read"
}'
```

**响应**:

```json
{
    "data": {
        "id": "kbs-00000001",
        "knowledge_base_id": "kb-00000001",
        "knowledge_base_name": "技术文档库",
        "organization_id": "org-00000001",
        "organization_name": "AI 技术团队",
        "shared_by_user_id": "user-00000001",
        "shared_by_username": "admin",
        "source_tenant_id": 1,
        "permission": "read",
        "my_role_in_org": "owner",
        "my_permission": "read",
        "created_at": "2025-08-15T10:00:00+08:00"
    },
    "success": true
}
```

## GET `/knowledge-bases/:id/shares` - 获取知识库共享列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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
                "organization_id": "org-00000001",
                "organization_name": "AI 技术团队",
                "shared_by_user_id": "user-00000001",
                "shared_by_username": "admin",
                "source_tenant_id": 1,
                "permission": "read",
                "my_role_in_org": "owner",
                "my_permission": "read",
                "created_at": "2025-08-15T10:00:00+08:00"
            }
        ]
    },
    "success": true
}
```

## PUT `/knowledge-bases/:id/shares/:share_id` - 更新共享权限

**请求参数**:
- `permission`: 新权限级别（必填）

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares/kbs-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "permission": "write"
}'
```

**响应**:

```json
{
    "success": true
}
```

## DELETE `/knowledge-bases/:id/shares/:share_id` - 取消知识库共享

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/shares/kbs-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true
}
```

---

## POST `/agents/:id/shares` - 共享智能体

**请求参数**:
- `organization_id`: 目标组织 ID（必填）
- `permission`: 权限级别（必填）

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/shares' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "organization_id": "org-00000001",
    "permission": "read"
}'
```

**响应**:

```json
{
    "data": {
        "id": "as-00000001",
        "agent_id": "agent-00000001",
        "agent_name": "智能客服助手",
        "organization_id": "org-00000001",
        "organization_name": "AI 技术团队",
        "shared_by_user_id": "user-00000001",
        "shared_by_username": "admin",
        "source_tenant_id": 1,
        "permission": "read",
        "created_at": "2025-08-15T11:00:00+08:00"
    },
    "success": true
}
```

## GET `/agents/:id/shares` - 获取智能体共享列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/agents/agent-00000001/shares' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
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
                "organization_id": "org-00000001",
                "organization_name": "AI 技术团队",
                "shared_by_user_id": "user-00000001",
                "shared_by_username": "admin",
                "source_tenant_id": 1,
                "permission": "read",
                "created_at": "2025-08-15T11:00:00+08:00"
            }
        ]
    },
    "success": true
}
```

## DELETE `/agents/:id/shares/:share_id` - 取消智能体共享

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/agents/agent-00000001/shares/as-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true
}
```

---

## GET `/shared-knowledge-bases` - 获取共享知识库列表

获取当前用户通过组织共享获得的所有知识库。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/shared-knowledge-bases' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "share_id": "kbs-00000001",
            "organization_id": "org-00000001",
            "org_name": "AI 技术团队",
            "permission": "read",
            "source_tenant_id": 1,
            "shared_at": "2025-08-15T10:00:00+08:00"
        }
    ],
    "success": true
}
```

## GET `/shared-agents` - 获取共享智能体列表

获取当前用户通过组织共享获得的所有智能体。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/shared-agents' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "share_id": "as-00000001",
            "organization_id": "org-00000001",
            "org_name": "AI 技术团队",
            "permission": "read",
            "source_tenant_id": 1,
            "shared_at": "2025-08-15T11:00:00+08:00"
        }
    ],
    "success": true
}
```
