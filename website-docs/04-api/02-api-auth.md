# API 参考：认证与用户

路由注册：`internal/router/router.go` 的 `RegisterAuthRoutes` 与 `RegisterMyInvitationRoutes`。Handler：`internal/handler/auth.go`、`internal/handler/auth_register_by_invite.go`、`internal/handler/tenant_invitation.go`。

除特别标注外，本组接口在认证中间件之后仅要求“已登录”（无角色下限）。免认证接口见各条目。

## 认证（/api/v1/auth）

### POST /api/v1/auth/register

用途：注册新用户（自助注册模式）。免认证。Handler: `internal/handler/auth.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `username` | string | 是（`binding:"required"`） | 用户名 |
| `email` | string | 是（`binding:"required"`） | 邮箱 |
| `password` | string | 是（`binding:"required"`） | 密码 |
| `tenant_provisioning` | string | 否 | 空间开通策略 |

响应：201 `{"success":true,"message":"...","user":{User}}`

```bash
curl -X POST $BASE/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"username":"alice","email":"a@ex.com","password":"secret123"}'
```

### POST /api/v1/auth/register-by-invite

用途：通过邀请/分享链接 token 注册并加入空间。免认证，IP 限流 30 次/分钟。Handler: `internal/handler/auth_register_by_invite.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `token` | string | 是（`binding:"required"`） | 邀请 token |
| `email` | string | 是（`binding:"required,email"`） | 邮箱 |
| `username` | string | 是（`binding:"required"`） | 用户名 |
| `password` | string | 是（`binding:"required,min=6"`） | 密码（≥6 位） |

响应：201，同 Login（`user/active_tenant/memberships/token/refresh_token`）。

```bash
curl -X POST $BASE/api/v1/auth/register-by-invite -H 'Content-Type: application/json' \
  -d '{"token":"<invite_token>","email":"a@ex.com","username":"alice","password":"secret123"}'
```

### POST /api/v1/auth/invitations/lookup

用途：匿名查询邀请 token 对应的空间信息（注册前预览）。免认证，IP 限流。Handler: `internal/handler/auth_register_by_invite.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `token` | string | 是（`binding:"required"`） | 邀请 token |

响应：200 `{"success":true,"data":{"tenant_id","tenant_name","role","expires_at"}}`

```bash
curl -X POST $BASE/api/v1/auth/invitations/lookup -H 'Content-Type: application/json' -d '{"token":"<invite_token>"}'
```

### POST /api/v1/auth/login

用途：邮箱密码登录。免认证。Handler: `internal/handler/auth.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `email` | string | 是（`binding:"required"`） | 邮箱 |
| `password` | string | 是（`binding:"required"`） | 密码 |

响应：200 `{"success":true,"user":{...},"active_tenant":{...},"memberships":[...],"token":"...","refresh_token":"..."}`

```bash
curl -X POST $BASE/api/v1/auth/login -H 'Content-Type: application/json' -d '{"email":"a@ex.com","password":"secret123"}'
```

### POST /api/v1/auth/auto-setup

用途：一键初始化（本地/Lite 场景自动建号建空间）。免认证，无请求体。Handler: `internal/handler/auth.go`

响应：200，同 Login。

```bash
curl -X POST $BASE/api/v1/auth/auto-setup
```

### GET /api/v1/auth/config

用途：查询注册模式等认证配置。免认证。Handler: `internal/handler/auth.go`

响应：200 `{"success":true,"registration_mode":"self_serve|invite_only"}`

```bash
curl $BASE/api/v1/auth/config
```

### POST /api/v1/auth/switch-tenant

用途：切换当前活跃空间并换发 token。需登录（无空间也可调用）。Handler: `internal/handler/auth.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `tenant_id` | uint64 | 是（`binding:"required"`） | 目标空间 ID |
| `refresh_token` | string | 否 | 用于换发新 token |

响应：200，同 Login。

换签成功后服务端会把目标空间记入「最近活跃租户」偏好，下次登录（密码/OIDC/换设备）自动回到该空间，客户端无需再补发 `PUT /auth/me/preferences` 请求。

```bash
curl -X POST $BASE/api/v1/auth/switch-tenant -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"tenant_id":2}'
```

### GET /api/v1/auth/oidc/config

用途：查询 OIDC 是否启用及显示名。免认证。Handler: `internal/handler/auth.go`

响应：200 `{"success":true,"enabled":bool,"provider_display_name":"..."}`

```bash
curl $BASE/api/v1/auth/oidc/config
```

### GET /api/v1/auth/oidc/url

用途：获取 OIDC 授权跳转 URL。免认证。Handler: `internal/handler/auth.go`

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `redirect_uri` | string | 是 | 回调地址 |

响应：200 `{"success":true,"authorization_url":"...","nonce":"..."}`

```bash
curl "$BASE/api/v1/auth/oidc/url?redirect_uri=https://app.example.com/callback"
```

### GET /api/v1/auth/oidc/callback

用途：OIDC 授权回调（浏览器重定向进入）。免认证。Handler: `internal/handler/auth.go`

查询参数：`code`、`state`、`error`、`error_description`（均由 OIDC 提供方带回）。

响应：302 重定向到前端，成功携带 `#oidc_result=<base64url>`，失败携带 `#oidc_error=...`。

```bash
curl -i "$BASE/api/v1/auth/oidc/callback?code=xxx&state=yyy"
```

### POST /api/v1/auth/refresh

用途：用 refresh token 换发新 token。免认证。Handler: `internal/handler/auth.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `refreshToken` | string | 是（`binding:"required"`） | refresh token |

响应：200 `{"success":true,"access_token":"...","refresh_token":"..."}`

```bash
curl -X POST $BASE/api/v1/auth/refresh -H 'Content-Type: application/json' -d '{"refreshToken":"<rt>"}'
```

### GET /api/v1/auth/validate

用途：校验当前 token 是否有效。需登录（无空间可调用）。Handler: `internal/handler/auth.go`

响应：200 `{"success":true,"message":"Token is valid","user":{UserInfo}}`

```bash
curl $BASE/api/v1/auth/validate -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/auth/logout

用途：登出（失效当前 token）。需登录。无请求体。Handler: `internal/handler/auth.go`

响应：200 `{"success":true,"message":"Logout successful"}`

```bash
curl -X POST $BASE/api/v1/auth/logout -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/auth/me

用途：查询当前调用者身份（用户/空间/成员关系/能力）。需登录；API key 亦可（策略 `apiKeyAny()`，任何有效 key）。Handler: `internal/handler/auth.go`

响应：200 `{"success":true,"data":{"user":{UserInfo},"tenant":{TenantResponse},"memberships":[...],"tenant_required":bool,"capabilities":{"can_create_tenant":bool}}}`

```bash
curl $BASE/api/v1/auth/me -H "X-API-Key: $API_KEY"
```

### PUT /api/v1/auth/me/preferences

用途：更新个人偏好（最近活跃空间）。需登录。Handler: `internal/handler/auth.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `last_active_tenant_id` | *uint64 | 否 | 最近活跃空间 ID，null 清除 |

响应：200 `{"success":true,"data":{UserPreferences}}`

```bash
curl -X PUT $BASE/api/v1/auth/me/preferences -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"last_active_tenant_id":2}'
```

### POST /api/v1/auth/change-password

用途：修改密码。需登录。Handler: `internal/handler/auth.go`

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `old_password` | string | 是（`binding:"required"`） | 旧密码 |
| `new_password` | string | 是（`binding:"required,min=6"`） | 新密码（≥6 位） |

响应：200 `{"success":true,"message":"Password changed successfully"}`

```bash
curl -X POST $BASE/api/v1/auth/change-password -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"old_password":"old","new_password":"newpass1"}'
```

## 我的邀请（/api/v1/me/invitations）

服务层保证“仅被邀请人可接受/拒绝”；无角色下限（无空间的新用户也可用）。Handler: `internal/handler/tenant_invitation.go`

### GET /api/v1/me/invitations

用途：列出发给我的邀请。

| 查询参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `include_terminal` | bool | 否 | `true` 时包含已完结的邀请 |

响应：200 `{"success":true,"data":{"invitations":[TenantInvitationResponse],"total":N}}`

```bash
curl $BASE/api/v1/me/invitations -H "Authorization: Bearer $TOKEN"
```

### GET /api/v1/me/invitations/pending-count

用途：待处理邀请计数（轻量轮询）。

响应：200 `{"success":true,"data":{"pending_count":N}}`

```bash
curl $BASE/api/v1/me/invitations/pending-count -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/me/invitations/:inv_id/accept

用途：接受邀请，写入成员关系。路径参数：`inv_id` 邀请 ID。无请求体。

响应：200 `{"success":true,"data":{"membership":{"tenant_id","role","status","joined_at"}}}`

```bash
curl -X POST $BASE/api/v1/me/invitations/12/accept -H "Authorization: Bearer $TOKEN"
```

### POST /api/v1/me/invitations/:inv_id/decline

用途：拒绝邀请。路径参数：`inv_id`。无请求体。

响应：200 `{"success":true}`

```bash
curl -X POST $BASE/api/v1/me/invitations/12/decline -H "Authorization: Bearer $TOKEN"
```
