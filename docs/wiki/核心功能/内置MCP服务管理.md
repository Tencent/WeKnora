---
title: 内置MCP服务管理
tags: [核心功能, MCP, 系统管理, 内置服务]
aliases: [内置MCP, BuiltinMCP, BUILTIN_MCP_SERVICES]
source: BUILTIN_MCP_SERVICES.md
---

# 内置 MCP 服务管理指南

## 概述

内置 MCP 服务是系统级别的 MCP（Model Context Protocol）服务配置，对所有租户可见，但敏感信息会被隐藏，且不可编辑或删除。内置 MCP 服务通常用于提供系统默认的外部工具和资源接入，确保所有租户都能使用统一的 MCP 服务。

> 用户视角的 MCP 服务操作参见 [MCP功能使用说明](MCP功能使用说明.md)

## 内置 MCP 服务特性

- **所有租户可见**：内置 MCP 服务对所有租户都可见，无需单独配置
- **安全保护**：内置 MCP 服务的敏感信息（URL、认证配置、Headers、环境变量）会被隐藏，无法查看详情
- **只读保护**：内置 MCP 服务不能被编辑或删除，仅支持测试连接
- **统一管理**：由系统管理员统一维护，确保配置一致性和安全性

## 与内置模型的对比

| 特性 | 内置模型 | 内置 MCP 服务 |
|------|---------|--------------|
| 标识字段 | `is_builtin` | `is_builtin` |
| 可见范围 | 所有租户 | 所有租户 |
| 隐藏信息 | API Key、Base URL | URL、认证配置、Headers、环境变量 |
| 编辑保护 | 不可编辑/删除 | 不可编辑/删除 |
| 前端标签 | 显示"内置"标签 | 显示"内置"标签 |
| 启停控制 | — | 禁用开关（始终启用） |

> 内置模型的详细管理参见 [内置模型管理](内置模型管理.md)

## 如何添加内置 MCP 服务

内置 MCP 服务需要通过数据库直接插入。

### 1. 准备服务数据

- 服务名称（name）
- 服务描述（description）
- 传输方式（transport_type）：`sse` 或 `http-streamable`
- 服务地址（url）
- 认证配置（auth_config）
- 高级配置（advanced_config）
- 租户ID（tenant_id）：建议使用小于 10000 的租户ID

**支持的传输方式**：
- `sse`：Server-Sent Events，推荐用于流式体验
- `http-streamable`：HTTP Streamable，标准 HTTP 兼容

> 注意：出于安全考虑，`stdio` 传输方式在服务端已被禁用。

### 2. 执行 SQL 插入语句

```sql
INSERT INTO mcp_services (
    id, tenant_id, name, description, enabled,
    transport_type, url, auth_config, advanced_config, is_builtin
) VALUES (
    'builtin-mcp-001', 10000, 'Web Search', '内置 Web 搜索 MCP 服务',
    true, 'sse', 'https://mcp.example.com/sse',
    '{"api_key": "your-api-key"}'::jsonb,
    '{"timeout": 30, "retry_count": 3, "retry_delay": 1}'::jsonb,
    true
) ON CONFLICT (id) DO NOTHING;
```

### 3. 验证插入结果

```sql
SELECT id, name, transport_type, enabled, is_builtin
FROM mcp_services WHERE is_builtin = true ORDER BY created_at;
```

## 注意事项

1. **ID 命名规范**：建议使用 `builtin-mcp-{序号}` 格式
2. **租户ID**：建议使用第一个租户ID（通常是 10000）
3. **JSON 格式**：`auth_config`、`advanced_config`、`headers` 必须是有效的 JSON
4. **幂等性**：使用 `ON CONFLICT (id) DO NOTHING` 确保重复执行不会报错
5. **安全性**：内置 MCP 服务的 URL、认证信息在前端会被自动隐藏
6. **传输方式限制**：仅支持 `sse` 和 `http-streamable`

## 将现有 MCP 服务设置为内置服务

```sql
UPDATE mcp_services SET is_builtin = true WHERE id = '服务ID' AND name = '服务名称';
```

## 移除内置 MCP 服务

```sql
UPDATE mcp_services SET is_builtin = false WHERE id = '服务ID';
```

---

## 反向链接

- [Home](../Home.md) — Wiki 首页导航
- [MCP功能使用说明](MCP功能使用说明.md) — 用户视角的 MCP 服务操作
- [内置模型管理](内置模型管理.md) — 同为内置系统配置，模式相似
