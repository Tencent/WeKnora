# 内置 MCP 服务管理指南

## 概述

内置 MCP 服务是系统级别的 MCP（Model Context Protocol）服务配置，对所有租户可见，但敏感信息会被隐藏，且不可编辑或删除。内置 MCP 服务通常用于提供系统默认的外部工具和资源接入，确保所有租户都能使用统一的 MCP 服务。

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

## 如何添加内置 MCP 服务

内置 MCP 服务需要通过数据库直接插入。以下是添加内置 MCP 服务的步骤：

### 1. 准备服务数据

首先，确保你已经有了要设置为内置 MCP 服务的配置信息，包括：
- 服务名称（name）
- 服务描述（description）
- 传输方式（transport_type）：`sse` 或 `http-streamable`
- 服务地址（url）：SSE / HTTP Streamable 必填
- 认证配置（auth_config）：可选，包括 api_key、token 等
- 高级配置（advanced_config）：可选，包括超时、重试策略等
- 租户ID（tenant_id）：建议使用小于 10000 的租户ID，避免冲突

**支持的传输方式**：
- `sse`：Server-Sent Events，推荐用于流式体验
- `http-streamable`：HTTP Streamable，标准 HTTP 兼容

> 注意：出于安全考虑，`stdio` 传输方式在服务端已被禁用。

### 2. 执行 SQL 插入语句

使用以下 SQL 语句插入内置 MCP 服务：

```sql
-- 示例：插入一个 SSE 传输方式的内置 MCP 服务
INSERT INTO mcp_services (
    id,
    tenant_id,
    name,
    description,
    enabled,
    transport_type,
    url,
    auth_config,
    advanced_config,
    is_builtin
) VALUES (
    'builtin-mcp-001',                                -- 使用固定ID，建议使用 builtin-mcp- 前缀
    10000,                                             -- 租户ID（使用第一个租户）
    'Web Search',                                      -- 服务名称
    '内置 Web 搜索 MCP 服务',                            -- 描述
    true,                                              -- 启用状态
    'sse',                                             -- 传输方式
    'https://mcp.example.com/sse',                     -- 服务地址
    '{"api_key": "your-api-key"}'::jsonb,              -- 认证配置
    '{"timeout": 30, "retry_count": 3, "retry_delay": 1}'::jsonb,  -- 高级配置
    true                                               -- 标记为内置服务
) ON CONFLICT (id) DO NOTHING;

-- 示例：插入一个 HTTP Streamable 传输方式的内置 MCP 服务
INSERT INTO mcp_services (
    id,
    tenant_id,
    name,
    description,
    enabled,
    transport_type,
    url,
    headers,
    auth_config,
    advanced_config,
    is_builtin
) VALUES (
    'builtin-mcp-002',
    10000,
    'Code Interpreter',
    '内置代码解释器 MCP 服务',
    true,
    'http-streamable',
    'https://mcp.example.com/stream',
    '{"X-Custom-Header": "value"}'::jsonb,
    '{"token": "your-bearer-token"}'::jsonb,
    '{"timeout": 60, "retry_count": 2, "retry_delay": 2}'::jsonb,
    true
) ON CONFLICT (id) DO NOTHING;
```

### 3. 验证插入结果

执行以下 SQL 查询验证内置 MCP 服务是否成功插入：

```sql
SELECT id, name, transport_type, enabled, is_builtin
FROM mcp_services
WHERE is_builtin = true
ORDER BY created_at;
```

## 注意事项

1. **ID 命名规范**：建议使用 `builtin-mcp-{序号}` 的格式，例如 `builtin-mcp-001`、`builtin-mcp-002`
2. **租户ID**：内置 MCP 服务可以属于任意租户，但建议使用第一个租户ID（通常是 10000）
3. **JSON 格式**：`auth_config`、`advanced_config`、`headers` 等字段必须是有效的 JSON 格式
4. **幂等性**：使用 `ON CONFLICT (id) DO NOTHING` 确保重复执行不会报错
5. **安全性**：内置 MCP 服务的 URL、认证信息在前端会被自动隐藏，但数据库中的原始数据仍然存在，请妥善保管数据库访问权限
6. **传输方式限制**：仅支持 `sse` 和 `http-streamable`，`stdio` 已被禁用

## 将现有 MCP 服务设置为内置服务

如果你已经有一个 MCP 服务，想将其设置为内置服务，可以使用 UPDATE 语句：

```sql
UPDATE mcp_services
SET is_builtin = true
WHERE id = '服务ID' AND name = '服务名称';
```

## 移除内置 MCP 服务

如果需要移除内置标记（恢复为普通 MCP 服务），执行：

```sql
UPDATE mcp_services
SET is_builtin = false
WHERE id = '服务ID';
```

注意：移除内置标记后，该 MCP 服务将恢复为普通服务，可以被编辑和删除。
