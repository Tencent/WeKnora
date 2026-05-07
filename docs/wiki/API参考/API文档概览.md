---
title: API文档概览
tags: [API参考, REST, 认证, 接口]
aliases: [API概览, API文档, API参考]
source: api/README.md
---

# API 文档概览

WeKnora 提供了一系列 RESTful API，用于创建和管理知识库、检索知识，以及进行基于知识的问答。

## 基础信息

- **基础 URL**: `/api/v1`
- **响应格式**: JSON
- **认证方式**: API Key

> API 认证使用 WeKnora 本地 JWT，OIDC 认证流程参见 [OIDC认证调用流程](../安全认证/OIDC认证调用流程.md)

## 认证机制

所有 API 请求需要在 HTTP 请求头中包含 `X-API-Key`：

```
X-API-Key: your_api_key
X-Request-ID: unique_request_id  # 建议，便于追踪
```

API Key 在 Web 页面完成账户注册后，前往账户信息页面获取。

## 错误处理

```json
{
  "success": false,
  "error": {
    "code": "错误代码",
    "message": "错误信息",
    "details": "错误详情"
  }
}
```

## API 分类

| 分类 | 描述 | 详细文档 |
|------|------|----------|
| 认证管理 | 用户注册、登录、令牌管理 | [OIDC认证调用流程.md](../安全认证/OIDC认证调用流程.md) |
| 租户管理 | 创建和管理租户账户 | [tenant.md](../../api/tenant.md) |
| 知识库管理 | 创建、查询和管理知识库 | [knowledge-base.md](../../api/knowledge-base.md) |
| 知识管理 | 上传、检索和管理知识内容 | [knowledge.md](../../api/knowledge.md) |
| 模型管理 | 配置和管理各种AI模型 | [model.md](../../api/model.md) |
| 分块管理 | 管理知识的分块内容 | [chunk.md](../../api/chunk.md) |
| 标签管理 | 管理知识库的标签分类 | [tag.md](../../api/tag.md) |
| FAQ管理 | 管理FAQ问答对 | [faq.md](../../api/faq.md) |
| 智能体管理 | 创建和管理自定义智能体 | [agent.md](../../api/agent.md) |
| 会话管理 | 创建和管理对话会话 | [session.md](../../api/session.md) |
| 知识搜索 | 在知识库中搜索内容 | [knowledge-search.md](../../api/knowledge-search.md) |
| 聊天功能 | 基于知识库和 Agent 进行问答 | [chat.md](../../api/chat.md) |
| 消息管理 | 获取和管理对话消息 | [message.md](../../api/message.md) |
| 评估功能 | 评估模型性能 | [evaluation.md](../../api/evaluation.md) |
| 初始化管理 | 知识库模型配置与 Ollama 管理 | [initialization.md](../../api/initialization.md) |
| 系统管理 | 系统信息、解析引擎、存储引擎 | [system.md](../../api/system.md) |
| MCP 服务 | MCP 工具服务管理 | [mcp-service.md](../../api/mcp-service.md) |
| 组织管理 | 组织、成员、知识库/智能体共享 | [organization.md](../../api/organization.md) |
| Skills | 预装智能体技能 | [skill.md](../../api/skill.md) |
| 网络搜索 | 网络搜索服务商 | [web-search.md](../../api/web-search.md) |
| 向量存储 | 向量数据库连接管理 | [vector-store.md](../../api/vector-store.md) |

> 各 API 的详细说明参见 `docs/api/` 目录下的对应文档

## 相关主题

- [OIDC认证调用流程](../安全认证/OIDC认证调用流程.md) — API 认证的 OIDC 流程
- [内置模型管理](../核心功能/内置模型管理.md) — 模型管理 API 的配置参考
- [MCP功能使用说明](../核心功能/MCP功能使用说明.md) — MCP 服务管理 API 的使用
- [共享空间说明](../安全认证/共享空间说明.md) — 组织管理 API 的业务逻辑
- [IM集成开发](../集成扩展/IM集成开发.md) — IM 渠道管理 API
- [数据源导入开发](../集成扩展/数据源导入开发.md) — 数据源管理 API

---

## 反向链接

- [Home](../Home.md) — Wiki 首页导航
- [OIDC认证调用流程](../安全认证/OIDC认证调用流程.md) — API 认证机制与 OIDC 相关
- [内置模型管理](../核心功能/内置模型管理.md) — 模型管理 API 的底层配置
- [MCP功能使用说明](../核心功能/MCP功能使用说明.md) — MCP 服务 API 的使用场景
- [共享空间说明](../安全认证/共享空间说明.md) — 组织管理 API 的业务逻辑
- [IM集成开发](../集成扩展/IM集成开发.md) — IM 渠道 API 的使用场景
- [数据源导入开发](../集成扩展/数据源导入开发.md) — 数据源 API 的使用场景
