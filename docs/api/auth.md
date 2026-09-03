# 认证管理 API

[返回目录](./README.md)

OIDC 完整调用流程见 [../OIDC认证调用流程.md](../OIDC认证调用流程.md)。本文档作为端点参考。

## 说明

WeKnora 的 `/auth/*` 端点本身**不需要 X-API-Key**，但部分端点需要在 `Authorization: Bearer <token>` 头中携带由 `/auth/login` 或 `/auth/oidc/callback` 返回的 JWT：

| 端点 | 鉴权方式 |
| --- | --- |
| `/auth/register` `/auth/login` `/auth/config` | 无 |
| `/auth/oidc/config` `/auth/oidc/url` `/auth/oidc/callback` | 无 |
| `/auth/refresh` | refresh_token（请求体携带） |
| `/auth/validate` `/auth/me` `/auth/logout` `/auth/change-password` `/auth/switch-tenant` `/auth/me/preferences` | Bearer JWT |

注册接口可通过环境变量 `DISABLE_REGISTRATION=true` 关闭。密码策略默认 8–32 位且同时包含字母与数字；部署可通过环境变量 `WEKNORA_AUTH_COMPLEX_PASSWORD_ENABLED` 或系统设置 `auth.complex_password_enabled` 要求额外包含大小写字母与特殊字符。当前策略见 `GET /auth/config`。

## 端点一览

| 方法 | 路径                       | 描述                                       |
| ---- | -------------------------- | ------------------------------------------ |
| GET  | `/auth/config`             | 公开认证配置（注册模式、密码复杂度）     |
| POST | `/auth/register`           | 用户注册                                   |
| POST | `/auth/login`              | 用户登录                                   |
| GET  | `/auth/oidc/config`        | 获取 OIDC 配置元数据                       |
| GET  | `/auth/oidc/url`           | 获取 OIDC 授权链接                         |
| GET  | `/auth/oidc/callback`      | OIDC 授权回调（由 IdP 重定向触发）         |
| POST | `/auth/refresh`            | 用 refresh_token 换新的 access_token       |
| GET  | `/auth/validate`           | 验证 JWT 有效性                            |
| POST | `/auth/logout`             | 退出登录                                   |
| GET  | `/auth/me`                 | 获取当前用户信息                           |
| PUT  | `/auth/me/preferences`     | 更新最近活跃空间等个人偏好                 |
| POST | `/auth/switch-tenant`      | 切换激活空间并换发 token                   |
| POST | `/auth/change-password`    | 修改密码                                   |

---

## GET `/auth/config` - 公开认证配置

无需登录。前端用它决定是否展示注册入口，以及注册/改密表单应使用哪套密码规则。

**响应**:

```json
{
    "success": true,
    "registration_mode": "self_serve",
    "complex_password_enabled": false
}
```

| 字段 | 说明 |
| ---- | ---- |
| `registration_mode` | `self_serve` 允许公开注册；`invite_only` 仅邀请 |
| `complex_password_enabled` | `true` 时新密码须含大小写字母、数字和特殊字符 `!@#$%^&*()_+-=[]{}|;:,.<>?` |

---

## POST `/auth/register` - 用户注册

**参数说明（请求体）**:

| 字段     | 类型   | 必填 | 校验                       | 说明      |
| -------- | ------ | ---- | -------------------------- | --------- |
| username | string | 是   | 长度 2-50                   | 用户名    |
| email    | string | 是   | 邮箱格式                   | 邮箱      |
| password | string | 是   | 8–32 位，须含字母与数字；若 `complex_password_enabled` 则还须含大小写与特殊字符 | 密码      |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/register' \
--header 'Content-Type: application/json' \
--data '{
    "username": "alice",
    "email": "alice@example.com",
    "password": "secret123"
}'
```

**响应**（201 Created）:

```json
{
    "success": true,
    "message": "Registration successful",
    "user": {
        "id": "usr-...",
        "username": "alice",
        "email": "alice@example.com",
        "tenant_id": 1,
        "is_active": true,
        "created_at": "2026-05-11T10:00:00+08:00",
        "updated_at": "2026-05-11T10:00:00+08:00"
    },
    "tenant": {
        "id": 1,
        "name": "alice's workspace",
        "api_key": "sk-..."
    }
}
```

**错误**: 注册被禁用 → 403；参数校验失败 → 400。

---

## POST `/auth/login` - 用户登录

**参数说明（请求体）**:

| 字段     | 类型   | 必填 | 说明          |
| -------- | ------ | ---- | ------------- |
| email    | string | 是   | 注册邮箱      |
| password | string | 是   | 密码          |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/login' \
--header 'Content-Type: application/json' \
--data '{
    "email": "alice@example.com",
    "password": "secret123"
}'
```

**响应**:

```json
{
    "success": true,
    "message": "Login successful",
    "user": { "id": "usr-...", "username": "alice", "email": "alice@example.com" },
    "tenant": { "id": 1, "name": "alice's workspace", "api_key": "sk-..." },
    "token": "eyJhbGciOi...",
    "refresh_token": "eyJhbGciOi..."
}
```

**错误**: 邮箱或密码错误 → 401；账号被禁用 → 403。

---

## GET `/auth/oidc/config` - 获取 OIDC 配置元数据

返回 OIDC 是否启用以及 Provider 显示名，前端登录页据此决定是否展示 OIDC 登录按钮。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/oidc/config'
```

**响应**:

```json
{
    "success": true,
    "enabled": true,
    "provider_display_name": "WeKnora SSO"
}
```

---

## GET `/auth/oidc/url` - 获取 OIDC 授权链接

返回前端应跳转的 OIDC IdP 授权页 URL 与状态码。

**查询参数**:

| 字段       | 类型   | 必填 | 说明                                                    |
| ---------- | ------ | ---- | ------------------------------------------------------- |
| redirect   | string | 否   | 登录成功后前端期望落地的路径（如 `/dashboard`），透传到 state |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/oidc/url?redirect=%2Fdashboard'
```

**响应**:

```json
{
    "success": true,
    "provider_display_name": "WeKnora SSO",
    "authorization_url": "https://idp.example.com/oauth/authorize?client_id=...&state=...",
    "state": "abcdef..."
}
```

---

## GET `/auth/oidc/callback` - OIDC 授权回调

由 IdP 在用户授权后重定向到此端点。一般不需要客户端代码直接调用——它的作用是把登录结果通过浏览器 hash 传回前端首页。

**查询参数**:

| 字段              | 类型   | 必填 | 说明                          |
| ----------------- | ------ | ---- | ----------------------------- |
| code              | string | 是   | IdP 颁发的 authorization code |
| state             | string | 是   | 与 `/auth/oidc/url` 返回值一致 |
| error             | string | 否   | IdP 返回的错误标识            |
| error_description | string | 否   | IdP 返回的错误详情            |

**响应**：始终返回 `302 Found`，跳转到 `/`，并把结果编码进 URL hash：

- 成功：`/#oidc_result=<base64url(JSON payload)>`，其中 payload 包含 `success` / `user` / `tenant` / `token` / `refresh_token` / `is_new_user`，与登录响应一致。
- 失败：`/#oidc_error=<reason>[&oidc_error_description=<message>]`，常见 reason 包括 `invalid_state`、`missing_code`、`login_failed`、`payload_encode_failed`。

---

## GET `/auth/oidc/start` - 发起 OIDC 登录（直接 302）

与 `/auth/oidc/url` 不同，此端点**直接 302 重定向**到 OIDC Provider 的授权页，不返回 JSON，因此无需前端 JS 介入。适用于外部平台（如企业门户 / Nexus）直接给出一个链接即可触发 OIDC 授权码流程，借助 IdP 的 SSO session 实现免再次输密码。

回调地址由后端根据请求自身的 origin（`<scheme>://<host>/api/v1/auth/oidc/callback`）自动构造，无需调用方提供。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/oidc/start'
```

**响应**：`302 Found`，`Location` 指向 IdP 授权页（含 `client_id` / `state` / `redirect_uri` / `scope`）。

> 登录成功后的回调行为与 `/auth/oidc/callback` 一致：302 回前端首页并把登录结果编码进 URL hash。当前登录后固定落到默认首页 `/platform/knowledge-bases`（直达指定业务页的 `next` 参数为未来扩展）。

---

## POST `/auth/refresh` - 刷新令牌

**参数说明（请求体）**:

| 字段          | 类型   | 必填 | 说明              |
| ------------- | ------ | ---- | ----------------- |
| refreshToken  | string | 是   | 登录时颁发的 refresh_token |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/refresh' \
--header 'Content-Type: application/json' \
--data '{
    "refreshToken": "eyJhbGciOi..."
}'
```

**响应**:

```json
{
    "success": true,
    "message": "Token refreshed successfully",
    "access_token": "eyJhbGciOi...",
    "refresh_token": "eyJhbGciOi..."
}
```

**错误**: refresh_token 无效或过期 → 401。

---

## GET `/auth/validate` - 验证 JWT

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/validate' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**:

```json
{
    "success": true,
    "valid": true,
    "user_id": "usr-...",
    "tenant_id": 1
}
```

无效 token 返回 401。

---

## POST `/auth/logout` - 退出登录

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/auth/logout' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**: `{ "success": true, "message": "Logged out successfully" }`

---

## GET `/auth/me` - 获取当前用户信息

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/me' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "user": {
            "id": "usr-...",
            "username": "alice",
            "email": "alice@example.com",
            "avatar": "",
            "tenant_id": 1,
            "is_active": true,
            "can_access_all_tenants": false,
            "created_at": "2026-05-11T10:00:00+08:00",
            "updated_at": "2026-05-11T10:00:00+08:00"
        },
        "tenant": {
            "id": 1,
            "name": "My Workspace"
        },
        "memberships": [
            {
                "tenant_id": 1,
                "tenant_name": "My Workspace",
                "role": "owner"
            }
        ],
        "tenant_required": false,
        "capabilities": {
            "can_create_tenant": false,
            "auto_accept_invitation": false
        }
    }
}
```

`capabilities` 供 SPA 读取部署级开关，无需调用超管设置 API：

| 字段 | 说明 |
| ---- | ---- |
| `can_create_tenant` | 当前用户是否可自助创建空间 |
| `auto_accept_invitation` | 全局 `tenant.auto_accept_invitation`：邮箱邀请已注册用户时是否直接加入（无需收件箱确认） |

---

## POST `/auth/switch-tenant` - 切换激活空间

为当前用户在目标空间重新签发 access / refresh token 对。调用者须在目标空间有 **active** 成员关系（`CanAccessAllTenants` 超级用户切到非 home 空间除外）。

成功换签会把目标空间写入账号级「最近活跃租户」偏好（`users.preferences.last_active_tenant_id`）。refresh JWT **不含** `tenant_id`，因此 **下次登录与 refresh 都按该偏好落点**；一次换签会改变该用户所有设备的落点。偏好写入失败则整次换签失败，**不会**发出新 token。

切回 home 时服务端写入 home ID（与 SPA 发送 `0` 清偏好在当前落点语义上等价）。Web UI 切空间走 `X-Tenant-ID` + `PUT /auth/me/preferences`，不调用本接口。

**参数说明（请求体）**:

| 字段          | 类型   | 必填 | 说明                         |
| ------------- | ------ | ---- | ---------------------------- |
| tenant_id     | uint64 | 是   | 目标空间 ID                  |
| refresh_token | string | 否   | 当前 refresh token，成功后撤销 |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/auth/switch-tenant' \
--header 'Authorization: Bearer eyJhbGciOi...' \
--header 'Content-Type: application/json' \
--data '{
    "tenant_id": 2
}'
```

**响应**: 与登录相同的 `LoginResponse`（`user` / `active_tenant` / `memberships` / `token` / `refresh_token`）。`user.preferences.last_active_tenant_id` 与目标空间一致。

**错误**: 无成员关系或偏好写入失败 → 403；参数校验失败 → 400。

---

## PUT `/auth/me/preferences` - 更新个人偏好

按 PATCH 语义合并 `users.preferences`（仅覆盖请求体里出现的字段）。SPA 在 UI 切空间后用此接口记住落点；`POST /auth/switch-tenant` 会在服务端写同一字段，API 客户端不必再补发本请求。

**参数说明（请求体）**:

| 字段                   | 类型    | 必填 | 说明 |
| ---------------------- | ------- | ---- | ---- |
| last_active_tenant_id  | *uint64 | 否   | 正整数 = 设置/替换；`0` = 清除（下次登录回 home）；省略 = 不改 |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/auth/me/preferences' \
--header 'Authorization: Bearer eyJhbGciOi...' \
--header 'Content-Type: application/json' \
--data '{
    "last_active_tenant_id": 2
}'
```

**响应**: `{ "success": true, "data": { "last_active_tenant_id": 2 } }`

---

## POST `/auth/change-password` - 修改密码

修改当前用户的登录密码。新密码须满足 **8–32 位**且**同时包含字母与数字**；当 `GET /auth/config` 的 `complex_password_enabled` 为 true 时，还须包含大小写字母与特殊字符。不能与当前密码相同。成功后**所有会话被撤销**，需使用新密码重新登录。

**参数说明（请求体）**:

| 字段          | 类型   | 必填 | 校验    | 说明      |
| ------------- | ------ | ---- | ------- | --------- |
| old_password  | string | 是   |          | 当前密码  |
| new_password  | string | 是   | 8–32 位，须含字母与数字（复杂模式另需大小写与特殊字符），且不同于旧密码 | 新密码    |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/change-password' \
--header 'Authorization: Bearer eyJhbGciOi...' \
--header 'Content-Type: application/json' \
--data '{
    "old_password": "secret123",
    "new_password": "newsecret456"
}'
```

**响应**: `{ "success": true, "message": "Password changed successfully" }`

**错误**（400）:

| `error.details`       | 含义                         |
| --------------------- | ---------------------------- |
| `invalid_old_password` | 当前密码不正确               |
| `password_policy`      | 新密码不满足长度/复杂度要求  |
| `same_password`        | 新密码与当前密码相同         |
