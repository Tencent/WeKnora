---
title: 空间RBAC说明
tags: [安全认证, RBAC, 权限, 多空间, 角色]
aliases: [RBAC, 角色权限, 空间角色, TenantRBAC]
source: RBAC说明.md
---

# 空间 RBAC 说明

本文档介绍 WeKnora 的**空间内权限控制（Workspace RBAC）**，包括角色矩阵、资源归属模型，以及它与 [共享空间](./共享空间说明.md) 的关系。

> 状态：已发布；由配置项 `tenant.enable_rbac` 控制，默认 `true`（强制鉴权）。
> 完整说明、灰度方案、Schema、路由守卫等参见 [`docs/RBAC说明.md`](../../RBAC说明.md)。

## 解决的问题

RBAC 引入前，只要通过 `X-API-Key` 或 JWT 认证成功，调用方在空间内基本等同管理员。一旦一个空间出现多名真人成员，就需要区分：

- 谁可以删除知识库、撤销 API Key；
- 谁可以编辑「自己」的 KB / Agent；
- 谁只读。

## 角色矩阵

| 角色 | 标识 | 关键能力 |
|------|------|----------|
| 只读 | `viewer` | 仅读 |
| 贡献者 | `contributor` | 可变更 `creator_id == 自己` 的资源；他人资源按 Viewer |
| 管理员 | `admin` | 可变更空间内任意资源；管理成员、共享基础设施 |
| Owner | `owner` | Admin + 可删空间；每个空间唯一 |

层级 `viewer < contributor < admin < owner`，高角色继承低角色。

### 鉴权层的例外

- **跨空间超管**：`enable_cross_tenant_access` 打开且账号 `CanAccessAllTenants=true`，通过 `X-Tenant-ID` 切换后等同 Admin。
- **API Key**：合成虚拟用户在所属空间内固定 Admin（删空间除外）。
- **孤儿空间自愈**：首位认证的真人自动晋升 Owner，避免 API Key-only 空间锁死。

## 资源归属

迁移 `000043` 在关键表加上 `creator_id`：

- `knowledge_bases.creator_id` —— 老数据回填为该空间的 Owner；
- `custom_agents.creator_id` + `runnable_by_viewer`（默认 `true`，允许 Viewer 在对话中调用）。

子资源沿 `chunk → knowledge → kb → creator_id` 链回溯。

由此得到两类守卫：

- **角色守卫**：只看角色，用于空间级基础设施（模型、向量库、IM 通道等）。
- **归属守卫**`OwnedXxxOrAdmin`：creator 或 Admin+ 二者其一即放行，用于具体资源写操作。

## 与共享空间的关系

| 维度 | 解决什么 | 主键 |
|------|---------|------|
| **空间 RBAC** | 同一空间内「你能对自己/别人/共享基础设施做什么」 | `tenant_members(user_id, tenant_id, role)` |
| **共享空间** | 跨空间「让别的空间的人也能用我的 KB / Agent」 | `organization_members` + 共享关系 |

两者**正交**：

- 共享空间不持有 KB / Agent，只记录「以何种权限共享到了哪个空间」；资源归属与 `creator_id` 不变；
- 一次对**他人共享过来**的 KB 的写操作，需要同时满足：共享时设了「可写」 + 你在该空间不是 Viewer + 源空间的 RBAC 仍然放行；
- API Key 跨空间访问**不会**带 Admin 光环——共享路径由 `organization_members.role` 决定，与 API Key 无关。

判定顺序见 `internal/middleware/kb_access.go`：

```text
KB 属于我当前空间？─是─► 走空间 RBAC（角色 + creator_id）
            └─否─► KB 共享给我所在空间？─是─► 取 min(共享权限, 空间角色)
                                       └─否─► 403 / 404
```

一句话：**空间 RBAC 是纵向的纵深防御，共享空间是横向的协作通道；跨空间写动作必须同时穿过两道闸口。**

## 配置

```yaml
tenant:
  enable_rbac: true          # false 则进入「仅记录不拦截」灰度窗口
  enable_cross_tenant_access: false
auth:
  registration_mode: self_serve   # 或 invite_only
audit:
  retention_days: 90              # 0 表示不清理
```

环境变量 `WEKNORA_TENANT_ENABLE_RBAC` / `WEKNORA_AUDIT_RETENTION_DAYS` 覆盖 YAML。`DISABLE_REGISTRATION=true` 等价于把 `registration_mode` 强制设为 `invite_only`。

## 审计

`audit_logs` 表记录：

- `rbac.member_added` / `removed` / `role_changed` / `left`
- `rbac.access_denied`（仅强制鉴权时；1 分钟滑动窗口去重）

每日后台 goroutine 清理超过 `audit.retention_days` 的旧行。

## 相关主题

- [共享空间说明](./共享空间说明.md) — 跨空间协作与共享，与 RBAC 正交
- [OIDC认证调用流程](./OIDC认证调用流程.md) — 多空间用户体系的认证入口
- [Lite与标准版区别](../项目概述/Lite与标准版区别.md) — Lite 单用户场景下 RBAC 实际不发挥作用

---

## 反向链接

- [Home](../Home.md) — Wiki 首页导航
- [共享空间说明](./共享空间说明.md) — 共享空间访问最终落到空间 RBAC 校验
- [OIDC认证调用流程](./OIDC认证调用流程.md) — JWT 解析后即进入 RBAC 角色匹配
