---
title: Agent技能系统
tags: [核心功能, Agent, Skills, 技能, 沙箱]
aliases: [Agent Skills, 技能系统, agent-skills]
source: agent-skills.md
---

# Agent 技能系统

## 概述

Agent Skills 是一种让 Agent 通过阅读"使用说明书"来学习新能力的扩展机制。与传统的硬编码工具不同，Skills 通过注入到 System Prompt 来扩展 Agent 的能力，遵循 **Progressive Disclosure（渐进式披露）** 的设计理念。目前仅支持带**智能推理**能力的智能体使用。

### 核心特性

- **非侵入式扩展**：不影响原有 Agent ReAct 流程
- **按需加载**：三级渐进式加载，优化 Token 使用
- **沙箱执行**：脚本在隔离环境中安全执行
- **灵活配置**：支持多目录、白名单过滤

> Skills 与 [MCP](../核心功能/MCP功能使用说明.md) 是两种不同的 Agent 扩展机制：Skills 通过 Prompt 注入，MCP 通过协议调用外部工具。

## 设计理念

### Progressive Disclosure（渐进式披露）

```
┌─────────────────────────────────────────────────────────────────┐
│ Level 1: 元数据 (Metadata)                                      │
│ • 始终加载到 System Prompt • 约 100 tokens/skill                  │
│ • 包含：技能名称 + 简短描述                                       │
└─────────────────────────────────────────────────────────────────┘
                              ↓ 用户请求匹配时
┌─────────────────────────────────────────────────────────────────┐
│ Level 2: 指令 (Instructions)                                    │
│ • 通过 read_skill 工具按需加载 • SKILL.md 的指令内容              │
│ • 包含：详细指令、代码示例、使用方法                               │
└─────────────────────────────────────────────────────────────────┘
                              ↓ 需要更多信息时
┌─────────────────────────────────────────────────────────────────┐
│ Level 3: 附加资源 (Resources)                                   │
│ • 通过 read_skill 工具加载特定文件                               │
│ • 通过 execute_skill_script 执行脚本                            │
└─────────────────────────────────────────────────────────────────┘
```

## Skill 目录结构

```
my-skill/
├── SKILL.md           # 必需：主文件（含 YAML frontmatter）
├── REFERENCE.md       # 可选：补充文档
├── templates/         # 可选：模板文件
└── scripts/           # 可选：可执行脚本
```

## 预加载技能

系统内置了以下 5 个预加载技能：

| 技能 | 用途 |
|------|------|
| citation-generator | 自动生成规范引用格式 |
| data-processor | 数据处理与分析 |
| doc-coauthoring | 引导用户完成结构化文档创作 |
| document-analyzer | 深度分析文档结构和内容 |
| summary-generator | 内容摘要生成 |

预加载技能位于 `skills/preloaded/` 目录下。

## 沙箱安全机制

### 脚本安全校验

执行前进行多层安全校验：危险命令检测、危险模式匹配、网络访问检测、反向 Shell 检测、参数注入检测等。

### Sandbox 模式

| 模式 | 说明 |
|------|------|
| `docker` | 使用 Docker 容器隔离（推荐） |
| `local` | 本地进程执行（基础安全限制） |
| `disabled` | 禁用脚本执行 |

通过环境变量 `WEKNORA_SANDBOX_MODE` 配置。

## 配置示例

```json
{
  "skills_enabled": true,
  "skill_dirs": ["/path/to/project/skills"],
  "allowed_skills": ["pdf-processing", "code-review"]
}
```

## 相关主题

- [MCP功能使用说明](MCP功能使用说明.md) — 另一种 Agent 扩展机制
- [IM集成开发](../集成扩展/IM集成开发.md) — Agent 可通过 IM 渠道使用技能
- [开发指南](../开发部署/开发指南.md) — 沙箱镜像的构建

---

## 反向链接

- [Home](../Home.md) — Wiki 首页导航
- [MCP功能使用说明](MCP功能使用说明.md) — 与 Skills 并列的 Agent 扩展机制
- [IM集成开发](../集成扩展/IM集成开发.md) — Agent 在 IM 渠道中可使用技能
- [版本路线图](../项目概述/版本路线图.md) — 路线图中的 Skills 社区扩展方向
