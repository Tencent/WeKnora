# WeKnora API 文档

## 目录

- [概述](#概述)
- [基础信息](#基础信息)
- [认证机制](#认证机制)
- [错误处理](#错误处理)
- [API 概览](#api-概览)

## 概述

WeKnora 提供了一系列 RESTful API，用于创建和管理知识库、检索知识，以及进行基于知识的问答。本文档详细描述了这些 API 的使用方式。

## 基础信息

- **基础 URL**: `/api/v1`
- **响应格式**: JSON
- **认证方式**: API Key

## 认证机制

所有 API 请求需要在 HTTP 请求头中包含 `X-API-Key` 进行身份认证：

```
X-API-Key: your_api_key
```

为便于问题追踪和调试，建议每个请求的 HTTP 请求头中添加 `X-Request-ID`：

```
X-Request-ID: unique_request_id
```

### 获取 API Key

在 web 页面完成账户注册后，请前往账户信息页面获取您的 API Key。

请妥善保管您的 API Key，避免泄露。API Key 代表您的账户身份，拥有完整的 API 访问权限。

## 错误处理

所有 API 使用标准的 HTTP 状态码表示请求状态，并返回统一的错误响应格式：

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

## API 概览

WeKnora API 按功能分为以下几类：

| 分类 | 描述 | 文档链接 |
|------|------|----------|
| 租户管理 | 创建和管理租户账户 | [tenant.md](./tenant.md) |
| 知识库管理 | 创建、查询和管理知识库 | [knowledge-base.md](./knowledge-base.md) |
| 知识管理 | 上传、检索和管理知识内容 | [knowledge.md](./knowledge.md) |
| 模型管理 | 配置和管理各种AI模型 | [model.md](./model.md) |
| 分块管理 | 管理知识的分块内容 | [chunk.md](./chunk.md) |
| 标签管理 | 管理知识库的标签分类 | [tag.md](./tag.md) |
| FAQ管理 | 管理FAQ问答对 | [faq.md](./faq.md) |
| 智能体管理 | 创建和管理自定义智能体 | [agent.md](./agent.md) |
| 会话管理 | 创建和管理对话会话 | [session.md](./session.md) |
| 知识搜索 | 在知识库中搜索内容 | [knowledge-search.md](./knowledge-search.md) |
| 聊天功能 | 基于知识库和 Agent 进行问答 | [chat.md](./chat.md) |
| 消息管理 | 获取和管理对话消息 | [message.md](./message.md) |
| 评估功能 | 评估模型性能 | [evaluation.md](./evaluation.md) |
