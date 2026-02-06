# Skills 示例

本目录包含 Agent Skills 功能的示例。

## 目录结构

```
skills/
├── README.md              # 本文件
└── pdf-processing/        # PDF 处理技能示例
    ├── SKILL.md           # 主文件（Level 2）
    ├── FORMS.md           # 补充文档（Level 3）
    └── scripts/           # 可执行脚本
        ├── analyze_form.py
        └── extract_text.py
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

完整文档请参阅：[Agent Skills 文档](../../docs/agent-skills.md)

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
  2. 调用 read_skill 加载技能内容
  3. 调用 execute_skill_script 执行 analyze_form.py
  4. 返回表单字段分析结果
```
