---
title: IM集成开发
tags: [集成扩展, IM, 企微, 飞书, Slack, Telegram, 钉钉, Mattermost]
aliases: [IM集成, IM开发, 企业微信, 即时通讯]
source: IM集成开发文档.md
---

# IM 集成开发

WeKnora 的 IM 集成模块将企业即时通讯平台（企业微信、飞书、Slack、Telegram、钉钉、Mattermost）接入 WeKnora 知识问答管道，支持在 IM 中直接向 AI 提问并获得实时流式回答。

IM 渠道绑定到 Agent，一个 Agent 可接入多个 IM 渠道。

> IM 渠道中的 Agent 可以使用 [MCP 工具](../核心功能/MCP功能使用说明.md) 和 [Skills 技能](../核心功能/Agent技能系统.md)

## 支持的平台

| 平台 | WebSocket 模式 | Webhook 模式 | 流式输出 |
|------|:-:|:-:|:-:|
| 企业微信 | ✅ | ✅ | ✅ |
| 飞书 | ✅ | ✅ | ✅ (CardKit) |
| Slack | ✅ (Socket Mode) | ✅ (Events API) | ✅ |
| Telegram | ✅ (长轮询) | ✅ | ✅ |
| 钉钉 | ✅ (Stream) | ✅ | ✅ (AI 卡片) |
| Mattermost | — | ✅ | ✅ |

## 快速接入指南

### 前置条件

- WeKnora 已部署并运行
- 已创建至少一个 Agent（自定义智能体）
- Agent 已配置好模型和知识库

> Agent 配置模型参见 [内置模型管理](../核心功能/内置模型管理.md)

### 企业微信接入

提供两种模式：
- **WebSocket 模式**（智能机器人，推荐）— 无需公网域名
- **Webhook 模式**（自建应用）— 需要公网回调地址

### 飞书接入

- **WebSocket 模式**（推荐）— 无需公网域名
- **Webhook 模式** — 需要公网回调地址

> 飞书同时也是数据源导入的支持平台，参见 [数据源导入开发](数据源导入开发.md)

### Slack 接入

- **Socket Mode**（推荐）— 无需公网域名
- **Events API** — 需要公网回调地址

### Telegram 接入

- **长轮询模式**（推荐）— 无需公网域名
- **Webhook 模式** — 需要 HTTPS 公网回调

### 钉钉接入

- **Stream 模式**（推荐）— 无需公网域名
- **Webhook 模式** — 需要公网回调地址

### Mattermost 接入

- 仅支持 **Webhook 模式**（出站 Webhook + REST API v4）

## 架构设计

系统采用 **Adapter Pattern**，每个平台实现 `im.Adapter` 接口，通过 `AdapterFactory` 动态创建。核心设计模式包括：

| 模式 | 用途 |
|------|------|
| Adapter Pattern | 统一不同 IM 平台的差异 |
| Factory Pattern | 从数据库渠道配置动态创建 Adapter |
| Command Pattern | 可插拔的斜杠指令系统 |
| Producer-Consumer | QA 队列 + Worker Pool |

## 斜杠指令系统

| 指令 | 说明 |
|------|------|
| `/help` | 显示所有可用指令 |
| `/info` | 查看当前绑定智能体信息 |
| `/search` | 对知识库执行混合检索 |
| `/stop` | 取消当前 QA 请求 |
| `/clear` | 清空当前对话记忆 |

## 扩展新平台

接入新的 IM 平台只需 3 步：

1. 实现 `im.Adapter` 接口（可选 `StreamSender`、`FileDownloader`）
2. 注册适配器工厂
3. 前端添加平台选项

> 扩展开发模式与 [添加网络搜索引擎](添加网络搜索引擎.md) 和 [集成向量数据库](集成向量数据库.md) 类似

## 相关主题

- [数据源导入开发](../集成扩展/数据源导入开发.md) — 飞书数据源同步（共享飞书应用凭证）
- [MCP功能使用说明](../核心功能/MCP功能使用说明.md) — Agent 可调用 MCP 工具
- [Agent技能系统](../核心功能/Agent技能系统.md) — Agent 可使用 Skills 技能
- [开发指南](../开发部署/开发指南.md) — 开发环境搭建

---

## 反向链接

- [Home](../Home.md) — Wiki 首页导航
- [数据源导入开发](../集成扩展/数据源导入开发.md) — 同样涉及飞书集成，可共享应用凭证
- [MCP功能使用说明](../核心功能/MCP功能使用说明.md) — IM 中 Agent 可调用 MCP 工具
- [Agent技能系统](../核心功能/Agent技能系统.md) — IM 中 Agent 可使用 Skills
- [版本路线图](../项目概述/版本路线图.md) — IM 集成已完成的里程碑
