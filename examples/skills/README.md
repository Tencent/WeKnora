# Skills 示例

本目录包含 Agent Skills 功能的示例。

## 目录结构

```
skills/
├── README.md              # 本文件
├── pdf-processing/        # PDF 处理技能示例
│   ├── SKILL.md           # 主文件（Level 2）
│   ├── FORMS.md           # 补充文档（Level 3）
│   └── scripts/           # 可执行脚本
│       ├── analyze_form.py
│       └── extract_text.py
└── research-handoff/      # 跨会话研究交接示例（无需额外脚本）
    ├── SKILL.md
    ├── FORMAT.md
    └── example-handoff.json
```

## 快速开始

### 运行 Demo

```bash
go run ./cmd/skills-demo/main.go
```

### 创建新 Skill

1. 在本目录创建新文件夹：

```bash
mkdir my-new-skill
```

2. 创建 `SKILL.md`：

```markdown
---
name: my-new-skill
description: Description of what this skill does and when to use it.
---

# My New Skill

Instructions for the agent...
```

3. 添加脚本（可选）：

```bash
mkdir my-new-skill/scripts
# 添加你的脚本
```

## 详细文档

完整文档请参阅：[Agent Skills 文档](../../website-docs/03-features/22-skills-sandbox.md)

## 示例：research-handoff

[research-handoff](research-handoff/SKILL.md) 用于在切换对话或执行器时导出、恢复研究进度，
保留目标、证据定位、实际阅读范围、待办和结果不明的操作。它使用现有知识检索和文件工具，
不增加服务端 API，也不自动迁移会话、知识库权限或沙箱。

将整个 `research-handoff` 文件夹打包为 ZIP 安装，并为智能体启用该技能；需要保存文件时配置沙箱。
请求“把当前研究整理成交接文件”，下载生成的 JSON，再在新对话中附上文件并请求“根据交接文件继续研究”。
新对话需要重新定位、核验证据，不能复用旧请求的 `bN/dN/cN` 临时句柄。
macOS Lite 本机沙箱直接保存到实际项目目录；没有文件工具时技能返回可手动保存的 JSON。

文件约定、合成样例和手工验收场景见 [FORMAT.md](research-handoff/FORMAT.md)。
样例不是真实研究数据，交接中的待办也不代表新的操作授权。

## 示例：pdf-processing

这是一个功能完整的示例技能，展示了：

- **SKILL.md**: 包含 YAML frontmatter 的主文件
- **FORMS.md**: 补充参考文档
- **scripts/**: 可在沙箱中执行的 Python 脚本

### 技能描述

```yaml
name: pdf-processing
description: Extract text and tables from PDF files, fill forms, merge documents.
```

### 包含的脚本

| 脚本 | 功能 |
|------|------|
| `analyze_form.py` | 分析 PDF 表单字段 |
| `extract_text.py` | 从 PDF 提取文本 |

### 使用示例

Agent 会根据用户请求自动调用：

```
用户: "分析一下这个 PDF 表单有哪些字段"

Agent: 
  1. 识别匹配 pdf-processing 技能
  2. 调用 read_file(path="skill://pdf-processing/SKILL.md") 加载技能内容
  3. 调用 shell_exec(skill_name="pdf-processing", command=...) 执行 analyze_form.py
  4. 返回表单字段分析结果
```
