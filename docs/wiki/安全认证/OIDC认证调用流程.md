---
title: OIDC认证调用流程
tags: [安全认证, OIDC, 认证, 登录, SSO]
aliases: [OIDC, OIDC认证, SSO登录, 第三方登录]
source: OIDC认证调用流程.md
---

# OIDC 认证调用流程

本文档说明 WeKnora 当前 OIDC 登录能力的实际调用过程，覆盖前后端完整链路。

> OIDC 认证是标准版多租户场景下的登录方式，[Lite 版](../项目概述/Lite与标准版区别.md)不需要

## 整体设计说明

本项目的 OIDC 登录采用 **后端发起授权参数生成、后端接收回调并完成 code 换 token、前端通过 URL hash 接收最终登录结果** 的模式。

核心特点：

1. **前端只负责发起跳转**，不直接和 OIDC Provider 交换 token
2. **后端负责用授权码 `code` 向 OIDC Provider 换取 token**
3. 后端拿到 OIDC 用户信息后，会查找本地用户；若不存在则自动创建本地账号和默认租户
4. 最终签发 WeKnora 自己的本地 JWT，OIDC token 只用于后端换取用户身份

> 自动创建用户与租户的逻辑与 [共享空间说明](../安全认证/共享空间说明.md) 的多租户模型相关

## 相关接口

| 接口 | 说明 |
|------|------|
| `GET /api/v1/auth/oidc/config` | 获取 OIDC 是否启用及 Provider 展示名称 |
| `GET /api/v1/auth/oidc/url` | 生成第三方登录跳转地址 |
| `GET /api/v1/auth/oidc/callback` | OIDC Provider 回调地址 |

## 调用流程（4 个阶段）

### 阶段一：前端发现能力

前端调用 `/auth/oidc/config`，决定是否展示第三方登录入口。

### 阶段二：浏览器跳转授权

前端调用 `/auth/oidc/url` 获取授权地址，然后跳转到 OIDC Provider。

### 阶段三：后端完成身份兑换

Provider 回调后端 `/auth/oidc/callback`，后端用 `code` 换 token、拉取用户信息、关联或创建本地用户，并签发 WeKnora JWT。

### 阶段四：前端接收最终结果

后端 302 回前端，通过 `#oidc_result` 传递登录结果；前端在 `App.vue` 中统一解析。

## 关键配置项

| 配置项 | 说明 |
|--------|------|
| `OIDC_AUTH_ENABLE` | 是否启用 OIDC 登录 |
| `OIDC_AUTH_CLIENT_ID` | OIDC Client ID |
| `OIDC_AUTH_CLIENT_SECRET` | OIDC Client Secret |
| `OIDC_AUTH_DISCOVERY_URL` | OIDC Discovery 地址 |
| `OIDC_AUTH_SCOPES` | Scope 列表，默认 `openid profile email` |

启用时的最小要求：`client_id` + `client_secret` + (`discovery_url` 或 `authorization_endpoint + token_endpoint`)

## 本地联调示例（Dex）

项目中已提供 Dex 示例配置：`misc/dex-config.yaml`。

> 除 Dex 外，也可以使用 KeyCloak 等其他符合 OpenID Connect 协议的 Provider

## 注意事项

1. **`redirect_uri` 必须严格匹配** Provider 客户端配置
2. **邮箱是本地账号关联主键** — 若 Provider 没返回 email，将无法完成登录
3. **首次 OIDC 登录会自动创建用户和默认租户**
4. **真正用于访问 API 的是本地 JWT**，不是 OIDC access token

## 相关主题

- [共享空间说明](../安全认证/共享空间说明.md) — 多租户场景下的用户与组织管理
- [Lite与标准版区别](../项目概述/Lite与标准版区别.md) — Lite 版无需 OIDC（单租户）
- [API文档概览](../API参考/API文档概览.md) — API 认证机制

---

## 反向链接

- [Home](../Home.md) — Wiki 首页导航
- [共享空间说明](../安全认证/共享空间说明.md) — OIDC 创建的用户与租户可用于共享空间
- [Lite与标准版区别](../项目概述/Lite与标准版区别.md) — Lite 不需要 OIDC（单租户无需注册）
- [API文档概览](../API参考/API文档概览.md) — API 的认证机制与 OIDC JWT 相关
