---
title: MCP功能使用说明
tags: [核心功能, MCP, 工具集成]
aliases: [MCP使用, MCP功能]
source: MCP功能使用说明.md
---

# MCP 功能使用说明

## 功能概述

- MCP（Model Context Protocol）让 WeKnora 可以安全地连接外部工具或数据源，扩展 Agent 在推理时可调用的能力。
- 在前端 `设置 > MCP 服务`（`frontend/src/views/settings/McpSettings.vue`）中集中管理所有服务，无需手动改配置文件。
- 每个服务都包含名称、传输方式（SSE / HTTP Streamable / Stdio）、连接地址或命令、认证信息以及高级超时与重试策略。

> 关于系统级的内置 MCP 服务管理，参见 [内置MCP服务管理](内置MCP服务管理.md)

## 入口与界面

- 打开控制台左侧菜单 `设置 -> MCP 服务`，即可看到当前租户下的所有 MCP 服务列表。
- 列表中可快速启停服务、查看描述，并通过右侧菜单执行"测试 / 编辑 / 删除"。
- "添加服务"按钮会弹出 `McpServiceDialog`，用于创建或修改服务。

## 常用操作流程

### 1. 新建服务

- 点击"添加服务"，填写名称与描述，选择传输方式。
- SSE / HTTP Streamable 需提供可访问的服务 URL；Stdio 需配置 `uvx`/`npx` 命令与参数，可附加环境变量。
- 根据需要填写 API Key、Bearer Token、超时与重试策略，保存后服务会出现在列表中。

### 2. 启停服务

- 在列表开关中切换启用状态，系统会即时调用后端 `updateMCPService`，失败时会自动回滚状态并弹出提示。

### 3. 连接测试

- 通过更多菜单选择"测试"，前端会调用 `/api/v1/mcp-services/{id}/test` 并弹出 `McpTestResult`。
- 成功时会展示服务可用的工具清单（含输入 schema）和资源列表；失败时会显示错误信息，方便排查网络或鉴权问题。

### 4. 编辑 / 删除

- "编辑"会带出原有配置，修改后保存即可。
- "删除"需要在弹窗中确认，完成后列表自动刷新。

## 使用建议

- **传输方式选择**：优先使用 SSE 获取流式体验；需要标准 HTTP Streamable 兼容时再切换；本地调试或离线环境适合使用 Stdio 并在同机启动 MCP Server。
- **鉴权管理**：将 API Key / Token 保存在"认证配置"中，生产环境建议单独创建最小权限 Key，并定期轮换。
- **重试策略**：对公网或第三方服务适当提高 `retry_count` 与 `retry_delay`，避免间歇性超时导致 Agent 中断

## 相关主题

- [内置MCP服务管理](../核心功能/内置MCP服务管理.md) — 系统管理员视角的内置 MCP 服务配置
- [Agent技能系统](Agent技能系统.md) — 另一种 Agent 扩展机制
- [IM集成开发](../集成扩展/IM集成开发.md) — IM 渠道中 Agent 使用 MCP 工具
- [添加网络搜索引擎](../集成扩展/添加网络搜索引擎.md) — 扩展搜索能力的另一种方式

---

## 反向链接

- [Home](../Home.md) — Wiki 首页导航
- [内置MCP服务管理](内置MCP服务管理.md) — MCP 的系统级管理（管理员视角）
- [Agent技能系统](Agent技能系统.md) — 与 MCP 并列的 Agent 扩展机制
- [IM集成开发](../集成扩展/IM集成开发.md) — Agent 在 IM 渠道中可调用 MCP 工具
