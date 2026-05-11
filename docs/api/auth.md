# 认证管理 API

[返回目录](./README.md)

OIDC 完整调用流程见 [../OIDC认证调用流程.md](../OIDC认证调用流程.md)。本文档作为端点参考。

## 说明

WeKnora 的 `/auth/*` 端点本身**不需要 X-API-Key**，但部分端点需要在 `Authorization: Bearer <token>` 头中携带由 `/auth/login` 或 `/auth/oidc/callback` 返回的 JWT：

| 端点 | 鉴权方式 |
| --- | --- |
| `/auth/register` `/auth/login` | 无 |
| `/auth/oidc/config` `/auth/oidc/url` `/auth/oidc/callback` | 无 |
| `/auth/refresh` | refresh_token（请求体携带） |
| `/auth/validate` `/auth/me` `/auth/logout` `/auth/change-password` | Bearer JWT |

注册接口可通过环境变量 `DISABLE_REGISTRATION=true` 关闭。

## 端点一览

| 方法 | 路径                       | 描述                                       |
| ---- | -------------------------- | ------------------------------------------ |
| POST | `/auth/register`           | 用户注册                                   |
| POST | `/auth/login`              | 用户登录                                   |
| GET  | `/auth/oidc/config`        | 获取 OIDC 配置元数据                       |
| GET  | `/auth/oidc/url`           | 获取 OIDC 授权链接                         |
| GET  | `/auth/oidc/callback`      | OIDC 授权回调（由 IdP 重定向触发）         |
| POST | `/auth/refresh`            | 用 refresh_token 换新的 access_token       |
| GET  | `/auth/validate`           | 验证 JWT 有效性                            |
| POST | `/auth/logout`             | 退出登录                                   |
| GET  | `/auth/me`                 | 获取当前用户信息                           |
| POST | `/auth/change-password`    | 修改密码                                   |

---

## POST `/auth/register` - 用户注册

**参数说明（请求体）**:

| 字段     | 类型   | 必填 | 校验                       | 说明      |
| -------- | ------ | ---- | -------------------------- | --------- |
| username | string | 是   | 长度 2-50                   | 用户名    |
| email    | string | 是   | 邮箱格式                   | 邮箱      |
| password | string | 是   | 最少 6 位                   | 密码      |

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
    }
}
```

---

## POST `/auth/change-password` - 修改密码

**参数说明（请求体）**:

| 字段          | 类型   | 必填 | 校验    | 说明      |
| ------------- | ------ | ---- | ------- | --------- |
| old_password  | string | 是   |          | 旧密码    |
| new_password  | string | 是   | 最少 6 位 | 新密码    |

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

**错误**: 旧密码不匹配或新密码不满足校验 → 400。
